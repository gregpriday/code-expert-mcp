package openaicompat

import (
	"context"
	"encoding/json"
	"io"

	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// --- request shapes ---

type responsesRequest struct {
	Model              string            `json:"model"`
	Instructions       string            `json:"instructions,omitempty"`
	Input              []map[string]any  `json:"input"`
	Tools              []map[string]any  `json:"tools,omitempty"`
	ToolChoice         any               `json:"tool_choice,omitempty"`
	Text               map[string]any    `json:"text,omitempty"`
	MaxOutputTokens    int               `json:"max_output_tokens,omitempty"`
	Reasoning          map[string]any    `json:"reasoning,omitempty"`
	Stream             bool              `json:"stream,omitempty"`
	Store              bool              `json:"store"`
	PreviousResponseID string            `json:"previous_response_id,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

func (c *Client) buildResponsesRequest(req provider.GenerationRequest) responsesRequest {
	rr := responsesRequest{
		Model:           req.Model,
		Instructions:    req.Instructions,
		Input:           translateInput(req.Input),
		MaxOutputTokens: req.MaxOutputTokens,
		Reasoning:       reasoningPayload(req.ReasoningEffort),
		Stream:          req.Stream,
		// Stateless by default: we replay the prior output items locally so this
		// works for Sakana (no previous_response_id) and privacy-sensitive use.
		// StoreState opts into provider-side state for OpenAI.
		Store:    req.StoreState,
		Metadata: req.Metadata,
	}
	if req.StoreState && req.PreviousResponseID != "" {
		rr.PreviousResponseID = req.PreviousResponseID
	}
	for _, t := range req.Tools {
		rr.Tools = append(rr.Tools, map[string]any{
			"type":        "function",
			"name":        t.Name,
			"description": t.Description,
			"parameters":  json.RawMessage(t.Parameters),
		})
	}
	if tc := translateToolChoice(req.ToolChoice); tc != nil {
		rr.ToolChoice = tc
	}
	if req.OutputSchema != nil {
		rr.Text = map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   req.OutputSchema.Name,
				"schema": json.RawMessage(req.OutputSchema.Schema),
				"strict": req.OutputSchema.Strict,
			},
		}
	}
	return rr
}

// translateInput converts provider-neutral messages into Responses input items.
func translateInput(msgs []provider.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleTool:
			out = append(out, map[string]any{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  m.Content,
			})
		case provider.RoleAssistant:
			switch {
			case len(m.ProviderItems) > 0:
				// Replay the provider's exact output items (reasoning items,
				// function calls, messages) verbatim and in order. This is the
				// canonical Responses continuation and preserves the ordering the
				// API requires (a reasoning item immediately before the
				// function_call it justifies).
				for _, raw := range m.ProviderItems {
					if item := rawObject(raw); item != nil {
						out = append(out, item)
					}
				}
			case len(m.ToolCalls) > 0:
				for _, tc := range m.ToolCalls {
					out = append(out, map[string]any{
						"type":      "function_call",
						"call_id":   tc.ID,
						"name":      tc.Name,
						"arguments": tc.Arguments,
					})
				}
				if m.Content != "" {
					out = append(out, message("assistant", m.Content))
				}
			default:
				out = append(out, message("assistant", m.Content))
			}
		default:
			out = append(out, message(string(m.Role), m.Content))
		}
	}
	return out
}

func message(role, content string) map[string]any {
	return map[string]any{"role": role, "content": content}
}

// rawObject decodes a stored opaque output item into a generic object for replay
// as a Responses input item. Returns nil if the item is not a JSON object.
func rawObject(raw json.RawMessage) map[string]any {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func translateToolChoice(tc provider.ToolChoice) any {
	switch tc.Mode {
	case provider.ToolChoiceNone:
		return "none"
	case provider.ToolChoiceRequired:
		return "required"
	case provider.ToolChoiceTool:
		if tc.ToolName != "" {
			return map[string]any{"type": "function", "name": tc.ToolName}
		}
		return "required"
	case provider.ToolChoiceAuto:
		return "auto"
	default:
		return nil
	}
}

// --- response shapes ---

type responsesEnvelope struct {
	ID     string            `json:"id"`
	Model  string            `json:"model"`
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
	Usage  responsesUsage    `json:"usage"`
	Error  *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type responsesItem struct {
	Type      string `json:"type"`
	Role      string `json:"role"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
}

// responsesUsage tolerates both the legacy top-level orchestration token fields
// and the current Sakana shape that nests them inside input_tokens_details /
// output_tokens_details.
type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens        int `json:"cached_tokens"`
		OrchestrationTokens int `json:"orchestration_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		OrchestrationTokens int `json:"orchestration_tokens"`
	} `json:"output_tokens_details"`
	OrchestrationInputTokens  int `json:"orchestration_input_tokens"`
	OrchestrationOutputTokens int `json:"orchestration_output_tokens"`
}

func (c *Client) generateResponses(ctx context.Context, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	body := c.buildResponsesRequest(req)
	var result provider.GenerationResponse
	err := c.retry.Do(ctx, func() error {
		resp, err := c.doPost(ctx, "/responses", body, req.Stream)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if req.Stream {
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
				return classify(resp.StatusCode, string(raw), resp.Header)
			}
			var body io.Reader = resp.Body
			if c.opts.StreamIdleTimeout > 0 {
				ir := newIdleReader(resp.Body, c.opts.StreamIdleTimeout)
				defer ir.Stop()
				body = ir
			}
			env, serr := c.consumeResponsesStream(body, req.OnTextDelta)
			if serr != nil {
				return serr
			}
			result = parseResponsesEnvelope(env, req)
			result.RequestID = headerRequestID(resp.Header, env.ID)
			return nil
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if cerr := classify(resp.StatusCode, string(raw), resp.Header); cerr != nil {
			return cerr
		}
		var env responsesEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return errBody("responses payload not parseable", raw)
		}
		if env.Error != nil {
			return schema.NewError(schema.CodeProviderUnsupported, "provider error: %s", env.Error.Message)
		}
		result = parseResponsesEnvelope(&env, req)
		result.RequestID = headerRequestID(resp.Header, env.ID)
		return nil
	})
	return result, err
}

// consumeResponsesStream reads SSE events, forwarding text deltas to onDelta and
// returning the final response envelope from the completed event.
func (c *Client) consumeResponsesStream(r io.Reader, onDelta func(string)) (*responsesEnvelope, error) {
	var final *responsesEnvelope
	var streamErr error
	err := scanSSE(r, func(ev sseEvent) bool {
		switch ev.Type {
		case "response.output_text.delta":
			var d struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal([]byte(ev.Data), &d) == nil && onDelta != nil && d.Delta != "" {
				onDelta(d.Delta)
			}
		case "response.completed", "response.incomplete":
			var w struct {
				Response responsesEnvelope `json:"response"`
			}
			if json.Unmarshal([]byte(ev.Data), &w) == nil {
				final = &w.Response
			}
		case "response.failed", "error":
			var w struct {
				Response responsesEnvelope `json:"response"`
				Message  string            `json:"message"`
			}
			_ = json.Unmarshal([]byte(ev.Data), &w)
			msg := w.Message
			if w.Response.Error != nil {
				msg = w.Response.Error.Message
			}
			streamErr = schema.NewError(schema.CodeProviderUnsupported, "provider stream error: %s", msg)
			return false
		}
		return true
	})
	if streamErr != nil {
		return nil, streamErr
	}
	if err != nil {
		return nil, schema.NewError(schema.CodeProviderTimeout, "stream read error: %v", err)
	}
	if final == nil {
		return nil, schema.NewError(schema.CodeProviderUnsupported, "stream ended without a completed response")
	}
	return final, nil
}

func parseResponsesEnvelope(env *responsesEnvelope, req provider.GenerationRequest) provider.GenerationResponse {
	var text string
	var refusal string
	var calls []provider.ToolCall
	// Capture every output item verbatim so reasoning items (and anything else the
	// dialect emits) survive the round trip and can be replayed on the next
	// request. Order is preserved.
	items := make([]json.RawMessage, 0, len(env.Output))
	for _, raw := range env.Output {
		items = append(items, raw)
		var item responsesItem
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		switch item.Type {
		case "message":
			for _, ct := range item.Content {
				switch ct.Type {
				case "output_text", "text":
					text += ct.Text
				case "refusal":
					refusal += ct.Refusal
				}
			}
		case "function_call":
			calls = append(calls, provider.ToolCall{ID: item.CallID, Name: item.Name, Arguments: item.Arguments})
		}
	}
	finish := env.Status
	if refusal != "" {
		if text == "" {
			text = refusal
		}
		finish = "refusal"
	}
	orchIn := env.Usage.InputTokensDetails.OrchestrationTokens
	if orchIn == 0 {
		orchIn = env.Usage.OrchestrationInputTokens
	}
	orchOut := env.Usage.OutputTokensDetails.OrchestrationTokens
	if orchOut == 0 {
		orchOut = env.Usage.OrchestrationOutputTokens
	}
	out := provider.GenerationResponse{
		Text:          text,
		ToolCalls:     calls,
		ProviderItems: items,
		ModelID:       env.Model,
		RequestID:     env.ID,
		FinishReason:  finish,
		Usage: provider.Usage{
			InputTokens:         env.Usage.InputTokens,
			CachedInputTokens:   env.Usage.InputTokensDetails.CachedTokens,
			OutputTokens:        env.Usage.OutputTokens,
			OrchestrationInput:  orchIn,
			OrchestrationOutput: orchOut,
		},
	}
	if req.OutputSchema != nil && text != "" {
		out.StructuredJSON = json.RawMessage(text)
	}
	return out
}

func headerRequestID(h interface{ Get(string) string }, fallback string) string {
	if h == nil {
		return fallback
	}
	if id := h.Get("X-Request-Id"); id != "" {
		return id
	}
	if id := h.Get("X-Request-ID"); id != "" {
		return id
	}
	return fallback
}
