package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/provider"
)

// reasoningEnvelope is a Responses payload with a reasoning item, a function
// call, and a message, plus usage with orchestration tokens nested in the
// *_tokens_details objects (the current Sakana shape).
const reasoningEnvelope = `{
  "id": "resp_1",
  "model": "fugu",
  "status": "completed",
  "output": [
    {"type": "reasoning", "id": "rs_123", "summary": [], "encrypted_content": "ENC"},
    {"type": "function_call", "id": "fc_1", "call_id": "call_abc", "name": "repo_search", "arguments": "{\"q\":\"x\"}"},
    {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "Looking into it."}]}
  ],
  "usage": {
    "input_tokens": 100,
    "output_tokens": 50,
    "input_tokens_details": {"cached_tokens": 10, "orchestration_tokens": 7},
    "output_tokens_details": {"orchestration_tokens": 3}
  }
}`

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, Dialect: "responses", MaxRetries: 0})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c, srv
}

func TestResponsesReasoningItemSurvivesToolRound(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reasoningEnvelope))
	})

	resp, err := c.Generate(context.Background(), provider.GenerationRequest{
		Model: "fugu",
		Input: []provider.Message{{Role: provider.RoleUser, Content: "search"}},
		Tools: []provider.FunctionTool{{Name: "repo_search"}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "repo_search" || resp.ToolCalls[0].ID != "call_abc" {
		t.Fatalf("function call not parsed: %+v", resp.ToolCalls)
	}
	if resp.Text != "Looking into it." {
		t.Errorf("message text not parsed: %q", resp.Text)
	}
	if len(resp.ProviderItems) != 3 {
		t.Fatalf("expected 3 captured output items, got %d", len(resp.ProviderItems))
	}

	// Replay: build the next request carrying the assistant turn's provider items
	// plus the tool result, and assert the reasoning item is sent back verbatim.
	follow := provider.GenerationRequest{
		Input: []provider.Message{
			{Role: provider.RoleUser, Content: "search"},
			{Role: provider.RoleAssistant, Content: resp.Text, ToolCalls: resp.ToolCalls, ProviderItems: resp.ProviderItems},
			{Role: provider.RoleTool, ToolCallID: "call_abc", Content: "results"},
		},
	}
	rr := c.buildResponsesRequest(follow)
	var sawReasoning, sawFnCall, sawToolOut bool
	for _, item := range rr.Input {
		switch item["type"] {
		case "reasoning":
			if item["id"] == "rs_123" && item["encrypted_content"] == "ENC" {
				sawReasoning = true
			}
		case "function_call":
			if item["call_id"] == "call_abc" {
				sawFnCall = true
			}
		case "function_call_output":
			if item["call_id"] == "call_abc" {
				sawToolOut = true
			}
		}
	}
	if !sawReasoning {
		t.Error("reasoning item was not replayed verbatim on the next request")
	}
	if !sawFnCall {
		t.Error("function_call item missing from replay")
	}
	if !sawToolOut {
		t.Error("function_call_output (tool result) missing from replay")
	}
}

func TestSakanaUsageNestedOrchestration(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(reasoningEnvelope))
	})
	resp, err := c.Generate(context.Background(), provider.GenerationRequest{Model: "fugu",
		Input: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Usage.OrchestrationInput != 7 || resp.Usage.OrchestrationOutput != 3 {
		t.Errorf("nested orchestration tokens not parsed: in=%d out=%d", resp.Usage.OrchestrationInput, resp.Usage.OrchestrationOutput)
	}
	if resp.Usage.CachedInputTokens != 10 {
		t.Errorf("cached tokens not parsed: %d", resp.Usage.CachedInputTokens)
	}
}

func TestSakanaUsageTopLevelOrchestrationBackCompat(t *testing.T) {
	const topLevel = `{"id":"r","model":"fugu","status":"completed","output":[],
	  "usage":{"input_tokens":1,"output_tokens":1,"orchestration_input_tokens":5,"orchestration_output_tokens":9}}`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(topLevel))
	})
	resp, err := c.Generate(context.Background(), provider.GenerationRequest{Model: "fugu",
		Input: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Usage.OrchestrationInput != 5 || resp.Usage.OrchestrationOutput != 9 {
		t.Errorf("top-level orchestration back-compat broken: in=%d out=%d", resp.Usage.OrchestrationInput, resp.Usage.OrchestrationOutput)
	}
}

func TestResponsesRefusalSurfaced(t *testing.T) {
	const refusalEnv = `{"id":"r","model":"fugu","status":"completed","output":[
	  {"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I can't help with that."}]}],
	  "usage":{"input_tokens":1,"output_tokens":1}}`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(refusalEnv))
	})
	resp, err := c.Generate(context.Background(), provider.GenerationRequest{Model: "fugu",
		Input: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.FinishReason != "refusal" {
		t.Errorf("refusal not surfaced as finish reason: %q", resp.FinishReason)
	}
	if !strings.Contains(resp.Text, "can't help") {
		t.Errorf("refusal text not captured: %q", resp.Text)
	}
}

// TestWebSearchBuiltinSerialized asserts a BuiltinToolWebSearch request appends a
// {"type":"web_search"} tool entry carrying the search-context size and domain
// filters, alongside any function tools.
func TestWebSearchBuiltinSerialized(t *testing.T) {
	c, err := New(Options{BaseURL: "https://example.com/v1", Dialect: "responses"})
	if err != nil {
		t.Fatal(err)
	}
	rr := c.buildResponsesRequest(provider.GenerationRequest{
		Model: "fugu",
		Input: []provider.Message{{Role: provider.RoleUser, Content: "go 1.23 release date"}},
		BuiltinTools: []provider.BuiltinTool{{
			Type:              provider.BuiltinToolWebSearch,
			SearchContextSize: "high",
			AllowedDomains:    []string{"go.dev"},
			BlockedDomains:    []string{"spam.example"},
		}},
	})
	var found map[string]any
	for _, tool := range rr.Tools {
		if tool["type"] == "web_search" {
			found = tool
		}
	}
	if found == nil {
		t.Fatalf("web_search tool not serialized into the request: %+v", rr.Tools)
	}
	if found["search_context_size"] != "high" {
		t.Errorf("search_context_size not serialized: %v", found["search_context_size"])
	}
	filters, ok := found["filters"].(map[string]any)
	if !ok {
		t.Fatalf("filters not serialized: %v", found["filters"])
	}
	if ad, _ := filters["allowed_domains"].([]string); len(ad) != 1 || ad[0] != "go.dev" {
		t.Errorf("allowed_domains not serialized: %v", filters["allowed_domains"])
	}
	if bd, _ := filters["blocked_domains"].([]string); len(bd) != 1 || bd[0] != "spam.example" {
		t.Errorf("blocked_domains not serialized: %v", filters["blocked_domains"])
	}
}

// TestWebSearchToolOmitsEmptyOptions confirms a built-in web_search with no tuning
// serializes a bare {"type":"web_search"} with no filters or context size.
func TestWebSearchToolOmitsEmptyOptions(t *testing.T) {
	c, err := New(Options{BaseURL: "https://example.com/v1", Dialect: "responses"})
	if err != nil {
		t.Fatal(err)
	}
	rr := c.buildResponsesRequest(provider.GenerationRequest{
		Model:        "fugu",
		BuiltinTools: []provider.BuiltinTool{{Type: provider.BuiltinToolWebSearch}},
	})
	if len(rr.Tools) != 1 {
		t.Fatalf("expected exactly one tool entry, got %+v", rr.Tools)
	}
	tool := rr.Tools[0]
	if tool["type"] != "web_search" {
		t.Fatalf("web_search not serialized: %+v", tool)
	}
	if _, ok := tool["search_context_size"]; ok {
		t.Errorf("empty search_context_size should be omitted: %+v", tool)
	}
	if _, ok := tool["filters"]; ok {
		t.Errorf("empty filters should be omitted: %+v", tool)
	}
}

// TestResponsesURLCitationsParsed checks that url_citation annotations on message
// content become URLCitations, and that a web_search_call output item is captured
// in ProviderItems without becoming a function ToolCall.
func TestResponsesURLCitationsParsed(t *testing.T) {
	const env = `{
	  "id": "resp_ws",
	  "model": "fugu",
	  "status": "completed",
	  "output": [
	    {"type": "web_search_call", "id": "ws_1", "status": "completed", "action": {"type": "search", "query": "go release"}},
	    {"type": "message", "role": "assistant", "content": [
	      {"type": "output_text", "text": "Go 1.23 shipped in August 2024.", "annotations": [
	        {"type": "url_citation", "start_index": 0, "end_index": 10, "title": "Go 1.23 Release Notes", "url": "https://go.dev/doc/go1.23"}
	      ]}
	    ]}
	  ],
	  "usage": {"input_tokens": 5, "output_tokens": 9}
	}`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(env))
	})
	resp, err := c.Generate(context.Background(), provider.GenerationRequest{
		Model:        "fugu",
		Input:        []provider.Message{{Role: provider.RoleUser, Content: "go release"}},
		BuiltinTools: []provider.BuiltinTool{{Type: provider.BuiltinToolWebSearch}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("web_search_call must not surface as a function ToolCall: %+v", resp.ToolCalls)
	}
	if len(resp.URLCitations) != 1 {
		t.Fatalf("expected 1 url citation, got %+v", resp.URLCitations)
	}
	cit := resp.URLCitations[0]
	if cit.URL != "https://go.dev/doc/go1.23" || cit.Title != "Go 1.23 Release Notes" {
		t.Errorf("citation not parsed: %+v", cit)
	}
	if !strings.Contains(resp.Text, "Go 1.23") {
		t.Errorf("summary text not parsed: %q", resp.Text)
	}
	if len(resp.ProviderItems) != 2 {
		t.Errorf("expected web_search_call + message captured in ProviderItems, got %d", len(resp.ProviderItems))
	}
}

// TestWebSearchSentOverWire confirms the built-in tool reaches the HTTP body.
func TestWebSearchSentOverWire(t *testing.T) {
	var gotBody string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"id":"r","model":"fugu","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`))
	})
	_, err := c.Generate(context.Background(), provider.GenerationRequest{
		Model:        "fugu",
		Input:        []provider.Message{{Role: provider.RoleUser, Content: "q"}},
		BuiltinTools: []provider.BuiltinTool{{Type: provider.BuiltinToolWebSearch, SearchContextSize: "medium"}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var sent responsesRequest
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
		t.Fatalf("request body not parseable: %v", err)
	}
	var ok bool
	for _, tool := range sent.Tools {
		if tool["type"] == "web_search" {
			ok = true
		}
	}
	if !ok {
		t.Errorf("web_search tool missing from wire body: %s", gotBody)
	}
}

// TestChatLeavesProviderItemsNil guards that the chat dialect (and therefore the
// existing fake-provider tests) never populate ProviderItems.
func TestChatLeavesProviderItemsNil(t *testing.T) {
	env := &chatEnvelope{Model: "m", Choices: []struct {
		FinishReason string      `json:"finish_reason"`
		Message      chatMessage `json:"message"`
	}{{FinishReason: "stop", Message: chatMessage{Content: "hello"}}}}
	out := parseChatEnvelope(env, provider.GenerationRequest{})
	if out.ProviderItems != nil {
		t.Errorf("chat dialect should not set ProviderItems, got %v", out.ProviderItems)
	}
}
