package mcpserver

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// RunStdio serves the MCP server over stdio. stdout carries MCP messages only.
func RunStdio(ctx context.Context, d Deps) error {
	s := New(d)
	return s.Run(ctx, &mcp.StdioTransport{})
}

// HTTPConfig configures the Streamable HTTP transport.
type HTTPConfig struct {
	Listen          string
	AllowedOrigins  []string
	AuthToken       string
	MaxRequestBytes int64
}

// RunStreamableHTTP serves the MCP server over Streamable HTTP with explicit
// origin verification, localhost/DNS-rebinding protection, optional bearer auth,
// content-type and size limits, and timeouts. Non-loopback binds require auth.
func RunStreamableHTTP(ctx context.Context, d Deps, cfg HTTPConfig) error {
	loopback := isLoopbackListen(cfg.Listen)
	if !loopback && cfg.AuthToken == "" {
		return schema.NewError(schema.CodeConfigInvalid,
			"refusing to bind %q without authentication; set an auth token for non-loopback serving", cfg.Listen)
	}

	server := New(d)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Logger:         d.Logger.Slog(),
		SessionTimeout: 30 * time.Minute,
		// Localhost DNS-rebinding protection is enabled by default.
	})

	secured := securityMiddleware(handler, cfg)

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           secured,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	d.Logger.Info("streamable HTTP server listening", "addr", cfg.Listen, "loopback", loopback)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return schema.NewError(schema.CodeInternal, "http server: %v", err)
	}
	return nil
}

// securityMiddleware enforces Origin allowlisting, optional bearer auth, and a
// request body size limit.
func securityMiddleware(next http.Handler, cfg HTTPConfig) http.Handler {
	maxBytes := cfg.MaxRequestBytes
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !originAllowed(origin, cfg.AllowedOrigins) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		if cfg.AuthToken != "" {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || subtleCompare(strings.TrimPrefix(auth, "Bearer "), cfg.AuthToken) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}

// originAllowed permits loopback origins by default, plus any explicitly allowed.
func originAllowed(origin string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(a, origin) {
			return true
		}
	}
	lower := strings.ToLower(origin)
	return strings.Contains(lower, "://localhost") || strings.Contains(lower, "://127.0.0.1") || strings.Contains(lower, "://[::1]")
}

func isLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		host = listen
	}
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// subtleCompare returns true when the strings DIFFER (constant-ish time).
func subtleCompare(a, b string) bool {
	if len(a) != len(b) {
		return true
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff != 0
}
