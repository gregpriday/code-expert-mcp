// Package provider defines the provider-neutral model interface and types used
// by the orchestrators. Concrete dialects (OpenAI-compatible Responses and Chat
// Completions) live in subpackages so provider-specific behavior never leaks
// into repository or workflow code.
package provider

import (
	"context"
	"encoding/json"
)

// Role identifies the author of a message in a conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one provider-neutral conversation turn.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content,omitempty"`
	// ToolCalls is set on assistant turns that request tool execution.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID and Name are set on tool-result turns (Role == RoleTool).
	ToolCallID string `json:"tool_call_id,omitempty"`
	Name       string `json:"name,omitempty"`
}

// ToolCall is a model request to invoke a function tool.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON object as a string
}

// FunctionTool is a function exposed to the model. Parameters is a JSON Schema.
type FunctionTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolChoiceMode controls whether/which tools the model may call.
type ToolChoiceMode string

const (
	ToolChoiceAuto     ToolChoiceMode = "auto"
	ToolChoiceNone     ToolChoiceMode = "none"
	ToolChoiceRequired ToolChoiceMode = "required"
	ToolChoiceTool     ToolChoiceMode = "tool" // force a specific tool by name
)

// ToolChoice selects the tool-calling policy for a request.
type ToolChoice struct {
	Mode     ToolChoiceMode `json:"mode"`
	ToolName string         `json:"tool_name,omitempty"`
}

// JSONSchema describes a strict structured-output contract.
type JSONSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}

// GenerationRequest is a single provider-neutral model call.
type GenerationRequest struct {
	Model           string
	Instructions    string // system-level rules
	Input           []Message
	Tools           []FunctionTool
	ToolChoice      ToolChoice
	OutputSchema    *JSONSchema
	MaxOutputTokens int
	ReasoningEffort string
	Stream          bool
	Metadata        map[string]string
	// OnTextDelta, if set and Stream is true, receives incremental output text
	// for progress reporting. Partial text must never be published as final.
	OnTextDelta func(string)
}

// Usage records token accounting from a single response.
type Usage struct {
	InputTokens         int            `json:"input_tokens"`
	CachedInputTokens   int            `json:"cached_input_tokens"`
	OutputTokens        int            `json:"output_tokens"`
	OrchestrationInput  int            `json:"orchestration_input_tokens"`
	OrchestrationOutput int            `json:"orchestration_output_tokens"`
	Extra               map[string]int `json:"extra,omitempty"`
}

// GenerationResponse is the provider-neutral result of a model call.
type GenerationResponse struct {
	Text           string          `json:"text"`
	ToolCalls      []ToolCall      `json:"tool_calls,omitempty"`
	StructuredJSON json.RawMessage `json:"structured_json,omitempty"`
	FinishReason   string          `json:"finish_reason,omitempty"`
	ModelID        string          `json:"model_id"`
	RequestID      string          `json:"request_id,omitempty"`
	Usage          Usage           `json:"usage"`
	Raw            map[string]any  `json:"-"`
}

// ModelInfo is a discovered model from the provider's /models endpoint.
type ModelInfo struct {
	ID      string `json:"id"`
	Created int64  `json:"created,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// ProviderCapabilities reports what a provider supports, used by doctor and the
// router to avoid unsupported requests.
type ProviderCapabilities struct {
	Dialect            string `json:"dialect"`
	SupportsTools      bool   `json:"supports_tools"`
	SupportsStructured bool   `json:"supports_structured_output"`
	SupportsStreaming  bool   `json:"supports_streaming"`
	SupportsReasoning  bool   `json:"supports_reasoning"`
}

// Provider is the model backend abstraction. Implementations must be safe for
// concurrent use.
type Provider interface {
	Generate(ctx context.Context, req GenerationRequest) (GenerationResponse, error)
	ListModels(ctx context.Context) ([]ModelInfo, error)
	Capabilities(ctx context.Context) ProviderCapabilities
}
