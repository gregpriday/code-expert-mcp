// Package openaicompat implements the provider.Provider interface against any
// OpenAI-compatible endpoint, supporting both the Responses API (primary) and
// Chat Completions (fallback). All dialect translation is confined here.
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/telemetry"
)

// Options configures the OpenAI-compatible client.
type Options struct {
	BaseURL           string
	APIKey            string
	Dialect           string // "responses" | "chat-completions"
	ConnectTimeout    time.Duration
	RequestTimeout    time.Duration
	StreamIdleTimeout time.Duration
	MaxRetries        int
	UserAgent         string
	HTTPClient        *http.Client
	Logger            *telemetry.Logger
}

// Client is a provider.Provider backed by an OpenAI-compatible HTTP API.
type Client struct {
	opts  Options
	httpc *http.Client
	retry provider.RetryPolicy
	log   *telemetry.Logger
}

// New constructs a Client. BaseURL and Dialect are required.
func New(opts Options) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, schema.NewError(schema.CodeConfigInvalid, "provider base URL is required")
	}
	if opts.Dialect == "" {
		opts.Dialect = "responses"
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "codeexpert/0.1"
	}
	httpc := opts.HTTPClient
	if httpc == nil {
		// RequestTimeout is enforced per-call via context, not the http.Client
		// timeout, so long streaming calls are not killed prematurely. The
		// connect timeout bounds only TCP dial + TLS handshake.
		ct := opts.ConnectTimeout
		if ct <= 0 {
			ct = 30 * time.Second
		}
		httpc = &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: ct}).DialContext,
				TLSHandshakeTimeout:   ct,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		}
	}
	log := opts.Logger
	if log == nil {
		log = telemetry.Nop()
	}
	return &Client{
		opts:  opts,
		httpc: httpc,
		retry: provider.DefaultRetryPolicy(opts.MaxRetries),
		log:   log,
	}, nil
}

var _ provider.Provider = (*Client)(nil)

// Generate runs a single model call, dispatching to the configured dialect.
func (c *Client) Generate(ctx context.Context, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	if c.opts.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.opts.RequestTimeout)
		defer cancel()
	}
	switch c.opts.Dialect {
	case "chat-completions":
		return c.generateChat(ctx, req)
	default:
		return c.generateResponses(ctx, req)
	}
}

// ListModels queries the /models endpoint.
func (c *Client) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	if c.opts.ConnectTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, max(c.opts.ConnectTimeout, 15*time.Second))
		defer cancel()
	}
	var out []provider.ModelInfo
	err := c.retry.Do(ctx, func() error {
		resp, err := c.doGet(ctx, "/models")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if cerr := classify(resp.StatusCode, string(body), resp.Header); cerr != nil {
			return cerr
		}
		var parsed struct {
			Data []provider.ModelInfo `json:"data"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return schema.NewError(schema.CodeProviderUnsupported, "models response not parseable: %v", err)
		}
		out = parsed.Data
		return nil
	})
	return out, err
}

// Capabilities reports the static capabilities of the configured dialect.
func (c *Client) Capabilities(ctx context.Context) provider.ProviderCapabilities {
	return provider.ProviderCapabilities{
		Dialect:            c.opts.Dialect,
		SupportsTools:      true,
		SupportsStructured: true,
		SupportsStreaming:  true,
		SupportsReasoning:  c.opts.Dialect == "responses",
	}
}

// doPost issues a POST with auth and returns the live response. Caller closes
// the body. Streaming callers set stream=true to request SSE.
func (c *Client) doPost(ctx context.Context, path string, payload any, stream bool) (*http.Response, error) {
	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, schema.NewError(schema.CodeInternal, "marshal request: %v", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(path), bytes.NewReader(buf))
	if err != nil {
		return nil, schema.NewError(schema.CodeInternal, "build request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", c.opts.UserAgent)
	if c.opts.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.opts.APIKey)
	}
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}
	resp, err := c.httpc.Do(httpReq)
	if err != nil {
		return nil, &provider.RetryableError{Err: schema.NewError(schema.CodeProviderTimeout, "transport error: %v", err)}
	}
	return resp, nil
}

func (c *Client) doGet(ctx context.Context, path string) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(path), nil)
	if err != nil {
		return nil, schema.NewError(schema.CodeInternal, "build request: %v", err)
	}
	httpReq.Header.Set("User-Agent", c.opts.UserAgent)
	if c.opts.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.opts.APIKey)
	}
	resp, err := c.httpc.Do(httpReq)
	if err != nil {
		return nil, &provider.RetryableError{Err: schema.NewError(schema.CodeProviderTimeout, "transport error: %v", err)}
	}
	return resp, nil
}

func (c *Client) url(path string) string {
	base := strings.TrimRight(c.opts.BaseURL, "/")
	return base + "/" + strings.TrimLeft(path, "/")
}

// classify is the package-local wrapper exposing retry classification.
func classify(status int, body string, header http.Header) error {
	return provider.ClassifyHTTPStatus(status, body, header)
}

func max(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// reasoningPayload returns the provider reasoning object if an effort is set.
func reasoningPayload(effort string) map[string]any {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return nil
	}
	return map[string]any{"effort": effort}
}

func errBody(prefix string, body []byte) error {
	return schema.NewError(schema.CodeProviderUnsupported, "%s: %s", prefix, provider.Truncate(string(body), 400))
}

var _ = fmt.Sprintf
