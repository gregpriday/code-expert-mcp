package openaicompat

import (
	"context"
	"encoding/json"
	"io"

	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/schema"
)

type chatRequest struct {
	Model           string            `json:"model"`
	Messages        []map[string]any  `json:"messages"`
	Tools           []map[string]any  `json:"tools,omitempty"`
	ToolChoice      any               `json:"tool_choice,omitempty"`
	ResponseFormat  map[string]any    `json:"response_format,omitempty"`
	MaxTokens       int               `json:"max_completion_tokens,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	Stream          bool              `json:"stream,omitempty"`
	StreamOptions   map[string]any    `json:"stream_options,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

func (c *Client) buildChatRequest(req provider.GenerationRequest) chatRequest {
	msgs := make([]map[string]any, 0, len(req.Input)+1)
	if req.Instructions != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.Instructions})
	}
	for _, m := range req.Input {
		switch m.Role {
		case provider.RoleTool:
			msgs = append(msgs, map[string]any{
				"role":         "tool",
				"tool_call_id": m.ToolCallID,
				"content":      m.Content,
			})
		case provider.RoleAssistant:
			am := map[string]any{"role": "assistant"}
			if m.Content != "" {
				am["content"] = m.Content
			}
			if len(m.ToolCalls) > 0 {
				tcs := make([]map[string]any, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					tcs = append(tcs, map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Name,
							"arguments": tc.Arguments,
						},
					})
				}
				am["tool_calls"] = tcs
			}
			msgs = append(msgs, am)
		default:
			msgs = append(msgs, map[string]any{"role": string(m.Role), "content": m.Content})
		}
	}
	cr := chatRequest{
		Model:           req.Model,
		Messages:        msgs,
		MaxTokens:       req.MaxOutputTokens,
		ReasoningEffort: req.ReasoningEffort,
		Stream:          req.Stream,
		Metadata:        req.Metadata,
	}
	if req.Stream {
		// Ask the server to emit a final usage-only chunk so streaming runs are
		// still accounted for.
		cr.StreamOptions = map[string]any{"include_usage": true}
	}
	for _, t := range req.Tools {
		cr.Tools = append(cr.Tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  json.RawMessage(t.Parameters),
			},
		})
	}
	switch req.ToolChoice.Mode {
	case provider.ToolChoiceNone:
		cr.ToolChoice = "none"
	case provider.ToolChoiceRequired:
		cr.ToolChoice = "required"
	case provider.ToolChoiceTool:
		if req.ToolChoice.ToolName != "" {
			cr.ToolChoice = map[string]any{"type": "function", "function": map[string]any{"name": req.ToolChoice.ToolName}}
		} else {
			cr.ToolChoice = "required"
		}
	case provider.ToolChoiceAuto:
		cr.ToolChoice = "auto"
	}
	if req.OutputSchema != nil {
		cr.ResponseFormat = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   req.OutputSchema.Name,
				"schema": json.RawMessage(req.OutputSchema.Schema),
				"strict": req.OutputSchema.Strict,
			},
		}
	}
	return cr
}

type chatEnvelope struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string      `json:"finish_reason"`
		Message      chatMessage `json:"message"`
	} `json:"choices"`
	Usage chatUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type chatMessage struct {
	Content   string `json:"content"`
	ToolCalls []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

type chatUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (c *Client) generateChat(ctx context.Context, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	body := c.buildChatRequest(req)
	var result provider.GenerationResponse
	err := c.retry.Do(ctx, func() error {
		resp, err := c.doPost(ctx, "/chat/completions", body, req.Stream)
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
			r, serr := c.consumeChatStream(body, req)
			if serr != nil {
				return serr
			}
			r.RequestID = headerRequestID(resp.Header, r.RequestID)
			result = r
			return nil
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if cerr := classify(resp.StatusCode, string(raw), resp.Header); cerr != nil {
			return cerr
		}
		var env chatEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return errBody("chat payload not parseable", raw)
		}
		if env.Error != nil {
			return schema.NewError(schema.CodeProviderUnsupported, "provider error: %s", env.Error.Message)
		}
		result = parseChatEnvelope(&env, req)
		result.RequestID = headerRequestID(resp.Header, env.ID)
		return nil
	})
	return result, err
}

func parseChatEnvelope(env *chatEnvelope, req provider.GenerationRequest) provider.GenerationResponse {
	out := provider.GenerationResponse{
		ModelID:   env.Model,
		RequestID: env.ID,
		Usage: provider.Usage{
			InputTokens:       env.Usage.PromptTokens,
			CachedInputTokens: env.Usage.PromptTokensDetails.CachedTokens,
			OutputTokens:      env.Usage.CompletionTokens,
		},
	}
	if len(env.Choices) > 0 {
		ch := env.Choices[0]
		out.Text = ch.Message.Content
		out.FinishReason = ch.FinishReason
		for _, tc := range ch.Message.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, provider.ToolCall{
				ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
			})
		}
	}
	if req.OutputSchema != nil && out.Text != "" {
		out.StructuredJSON = json.RawMessage(out.Text)
	}
	return out
}

// consumeChatStream accumulates content and tool-call deltas from an SSE stream.
func (c *Client) consumeChatStream(r io.Reader, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	var (
		text     string
		model    string
		id       string
		finish   string
		usage    chatUsage
		hasUsage bool
		toolAcc  = map[int]*provider.ToolCall{}
		toolKeys []int
	)
	err := scanSSE(r, func(ev sseEvent) bool {
		data := ev.Data
		if data == "[DONE]" {
			return false
		}
		var chunk struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *chatUsage `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			return true
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
			hasUsage = true
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.ID != "" {
			id = chunk.ID
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				text += ch.Delta.Content
				if req.OnTextDelta != nil {
					req.OnTextDelta(ch.Delta.Content)
				}
			}
			if ch.FinishReason != "" {
				finish = ch.FinishReason
			}
			for _, tc := range ch.Delta.ToolCalls {
				acc, ok := toolAcc[tc.Index]
				if !ok {
					acc = &provider.ToolCall{}
					toolAcc[tc.Index] = acc
					toolKeys = append(toolKeys, tc.Index)
				}
				if tc.ID != "" {
					acc.ID = tc.ID
				}
				if tc.Function.Name != "" {
					acc.Name = tc.Function.Name
				}
				acc.Arguments += tc.Function.Arguments
			}
		}
		return true
	})
	if err != nil {
		return provider.GenerationResponse{}, schema.NewError(schema.CodeProviderTimeout, "stream read error: %v", err)
	}
	out := provider.GenerationResponse{Text: text, ModelID: model, RequestID: id, FinishReason: finish}
	if hasUsage {
		out.Usage = provider.Usage{
			InputTokens:       usage.PromptTokens,
			CachedInputTokens: usage.PromptTokensDetails.CachedTokens,
			OutputTokens:      usage.CompletionTokens,
		}
	}
	for _, k := range toolKeys {
		out.ToolCalls = append(out.ToolCalls, *toolAcc[k])
	}
	if req.OutputSchema != nil && text != "" {
		out.StructuredJSON = json.RawMessage(text)
	}
	return out, nil
}
