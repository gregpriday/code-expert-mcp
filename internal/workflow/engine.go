// Package workflow contains the bounded orchestration around the deterministic
// repository tools: the plan/help pipeline and the review pipeline. It owns the
// model session loop, structured synthesis with independent Go validation, and
// the run lifecycle. The model interprets and synthesizes; it never owns
// canonical repository state.
package workflow

import (
	"context"
	"strconv"
	"sync"

	"github.com/gregpriday/codeexpert/internal/budget"
	"github.com/gregpriday/codeexpert/internal/cache"
	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/llmtools"
	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/telemetry"
)

// Engine holds the shared dependencies for all workflows.
type Engine struct {
	Cfg      config.Config
	Provider provider.Provider
	Cache    *cache.Cache
	Log      *telemetry.Logger
}

// RunOptions carry per-invocation context that is not part of the request.
type RunOptions struct {
	AllowedRoots []string
	Progress     ProgressFunc
	RunID        string
}

// ProgressFunc receives stage transitions for MCP progress notifications.
type ProgressFunc func(stage, detail string)

// usageAccumulator aggregates provider usage into a schema.RunUsage.
type usageAccumulator struct {
	mu sync.Mutex
	u  schema.RunUsage
}

func (a *usageAccumulator) add(model string, resp provider.GenerationResponse) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.u.ModelCalls++
	a.u.InputTokens += resp.Usage.InputTokens
	a.u.CachedInputTokens += resp.Usage.CachedInputTokens
	a.u.OutputTokens += resp.Usage.OutputTokens
	a.u.OrchestrationInTok += resp.Usage.OrchestrationInput
	a.u.OrchestrationOutTok += resp.Usage.OrchestrationOutput
	if id := resp.ModelID; id != "" && !contains(a.u.ModelsUsed, id) {
		a.u.ModelsUsed = append(a.u.ModelsUsed, id)
	} else if model != "" && !contains(a.u.ModelsUsed, model) {
		a.u.ModelsUsed = append(a.u.ModelsUsed, model)
	}
	if resp.RequestID != "" && !contains(a.u.ProviderRequestIDs, resp.RequestID) {
		a.u.ProviderRequestIDs = append(a.u.ProviderRequestIDs, resp.RequestID)
	}
}

func (a *usageAccumulator) snapshot() schema.RunUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.u
}

// Session is one model conversation with bounded tool use and local state
// management (the provider does not retain conversation IDs).
type Session struct {
	eng       *Engine
	model     string
	reasoning string
	system    string
	messages  []provider.Message
	reg       *llmtools.Registry
	tracker   *budget.Tracker
	usage     *usageAccumulator
	progress  ProgressFunc
	stream    bool
}

// NewSession starts a model session with a system prompt and registry.
func (e *Engine) NewSession(model, reasoning, system string, reg *llmtools.Registry, tracker *budget.Tracker, usage *usageAccumulator, progress ProgressFunc) *Session {
	return &Session{
		eng: e, model: model, reasoning: reasoning, system: system,
		reg: reg, tracker: tracker, usage: usage, progress: progress,
		stream: true,
	}
}

// AddUser appends a user message.
func (s *Session) AddUser(content string) {
	s.messages = append(s.messages, provider.Message{Role: provider.RoleUser, Content: content})
}

// generate performs one provider call, charging budget and recording usage.
func (s *Session) generate(ctx context.Context, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	if s.tracker != nil {
		if err := s.tracker.ChargeModelCall(); err != nil {
			return provider.GenerationResponse{}, err
		}
	}
	req.Model = s.model
	req.Instructions = s.system
	req.ReasoningEffort = s.reasoning
	if req.MaxOutputTokens == 0 {
		req.MaxOutputTokens = s.eng.Cfg.Models.MaxOutputTokens
	}
	resp, err := s.eng.Provider.Generate(ctx, req)
	if err != nil {
		return resp, err
	}
	if s.usage != nil {
		s.usage.add(s.model, resp)
	}
	return resp, nil
}

// RunToolLoop runs bounded exploration: the model may call read-only tools for
// up to maxRounds rounds. It stops when the model stops calling tools, the
// budget is exhausted, or the round limit is hit.
func (s *Session) RunToolLoop(ctx context.Context, maxRounds int) error {
	tools := s.reg.FunctionTools()
	for round := 0; round < maxRounds; round++ {
		if s.tracker != nil && s.tracker.TimedOut() {
			return schema.NewError(schema.CodeBudgetExhausted, "time budget exhausted during exploration")
		}
		if s.progress != nil {
			s.progress("exploration", "round "+itoa(round+1))
		}
		resp, err := s.generate(ctx, provider.GenerationRequest{
			Input:      s.messages,
			Tools:      tools,
			ToolChoice: provider.ToolChoice{Mode: provider.ToolChoiceAuto},
			Stream:     s.stream,
		})
		if err != nil {
			// Budget exhaustion mid-loop is non-fatal: return what we have.
			if schema.AsToolError(err).Code == schema.CodeBudgetExhausted {
				return nil
			}
			return err
		}
		// Record the assistant turn (text + any tool calls).
		s.messages = append(s.messages, provider.Message{
			Role: provider.RoleAssistant, Content: resp.Text, ToolCalls: resp.ToolCalls,
		})
		if len(resp.ToolCalls) == 0 {
			return nil // model is done exploring
		}
		for _, call := range resp.ToolCalls {
			result := s.reg.Execute(ctx, call)
			s.messages = append(s.messages, provider.Message{
				Role: provider.RoleTool, ToolCallID: call.ID, Name: call.Name, Content: result,
			})
		}
	}
	return nil
}

// Synthesize performs a final, tool-free model call constrained to a JSON
// schema, returning the raw JSON. If the provider rejects the schema, it retries
// once without it (relying on Go validation as the real gate).
func (s *Session) Synthesize(ctx context.Context, instruction string, out *provider.JSONSchema) ([]byte, error) {
	s.AddUser(instruction)
	req := provider.GenerationRequest{
		Input:        s.messages,
		ToolChoice:   provider.ToolChoice{Mode: provider.ToolChoiceNone},
		OutputSchema: out,
		Stream:       s.stream,
	}
	resp, err := s.generate(ctx, req)
	if err != nil {
		code := schema.AsToolError(err).Code
		if out != nil && (code == schema.CodeProviderSchema || code == schema.CodeProviderUnsupported) {
			req.OutputSchema = nil
			resp, err = s.generate(ctx, req)
		}
		if err != nil {
			return nil, err
		}
	}
	body := resp.StructuredJSON
	if len(body) == 0 {
		body = []byte(resp.Text)
	}
	clean := extractJSON(body)
	if len(clean) == 0 {
		return nil, schema.NewError(schema.CodeOutputInvalid, "model returned no JSON object")
	}
	// Keep the assistant turn so a repair call has the prior attempt in context.
	s.messages = append(s.messages, provider.Message{Role: provider.RoleAssistant, Content: string(clean)})
	return clean, nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func itoa(n int) string { return strconv.Itoa(n) }
