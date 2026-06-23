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
	Model           string            `json:"model"`
	Instructions    string            `json:"instructions,omitempty"`
	Input           []map[string]any  `json:"input"`
	Tools           []map[string]any  `json:"tools,omitempty"`
	ToolChoice      any               `json:"tool_choice,omitempty"`
	Text            map[string]any    `json:"text,omitempty"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	Reasoning       map[string]any    `json:"reasoning,omitempty"`
	Stream          bool              `json:"stream,omitempty"`
	Store           bool              `json:"store"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

func (c *Client) buildResponsesRequest(req provider.GenerationRequest) responsesRequest {
	rr := responsesRequest{
		Model:           req.Model,
		Instructions:    req.Instructions,
		Input:           translateInput(req.Input),
		MaxOutputTokens: req.MaxOutputTokens,
		Reasoning:       reasoningPayload(req.ReasoningEffort),
		Stream:          req.Stream,
		Store:           false, // Sakana does not support previous_response_id; we manage state locally.
		Metadata:        req.Metadata,
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
			if len(m.ToolCalls) > 0 {
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
			} else {
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
	ID     string          `json:"id"`
	Model  string          `json:"model"`
	Status string          `json:"status"`
	Output []responsesItem `json:"output"`
	Usage  responsesUsage  `json:"usage"`
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
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
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
			env, serr := c.consumeResponsesStream(resp.Body, req.OnTextDelta)
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
	var calls []provider.ToolCall
	for _, item := range env.Output {
		switch item.Type {
		case "message":
			for _, ct := range item.Content {
				if ct.Type == "output_text" || ct.Type == "text" {
					text += ct.Text
				}
			}
		case "function_call":
			calls = append(calls, provider.ToolCall{ID: item.CallID, Name: item.Name, Arguments: item.Arguments})
		}
	}
	out := provider.GenerationResponse{
		Text:         text,
		ToolCalls:    calls,
		ModelID:      env.Model,
		RequestID:    env.ID,
		FinishReason: env.Status,
		Usage: provider.Usage{
			InputTokens:         env.Usage.InputTokens,
			CachedInputTokens:   env.Usage.InputTokensDetails.CachedTokens,
			OutputTokens:        env.Usage.OutputTokens,
			OrchestrationInput:  env.Usage.OrchestrationInputTokens,
			OrchestrationOutput: env.Usage.OrchestrationOutputTokens,
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
