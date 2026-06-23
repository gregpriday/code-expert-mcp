package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/schema"
)

func TestChatForwardsReasoningEffortAndStreamUsage(t *testing.T) {
	c, err := New(Options{BaseURL: "https://example.com/v1", Dialect: "chat-completions"})
	if err != nil {
		t.Fatal(err)
	}
	cr := c.buildChatRequest(provider.GenerationRequest{
		Model: "m", Stream: true, ReasoningEffort: "high",
		Input: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	b, _ := json.Marshal(cr)
	s := string(b)
	if !strings.Contains(s, `"reasoning_effort":"high"`) {
		t.Errorf("chat request missing reasoning_effort: %s", s)
	}
	if !strings.Contains(s, `"stream_options":{"include_usage":true}`) {
		t.Errorf("chat request missing stream_options.include_usage: %s", s)
	}

	// Non-streaming requests must not carry stream_options.
	cr2 := c.buildChatRequest(provider.GenerationRequest{Model: "m"})
	b2, _ := json.Marshal(cr2)
	if strings.Contains(string(b2), "stream_options") {
		t.Errorf("non-stream request should not include stream_options: %s", b2)
	}
}

func TestChatStreamUsageParsed(t *testing.T) {
	sse := "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":4}}\n\n" +
		"data: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer srv.Close()
	c, err := New(Options{BaseURL: srv.URL, Dialect: "chat-completions", MaxRetries: 0})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Generate(context.Background(), provider.GenerationRequest{
		Model: "m", Stream: true,
		Input: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Text != "hi" {
		t.Errorf("text = %q, want hi", resp.Text)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 4 {
		t.Errorf("streaming usage not parsed: in=%d out=%d", resp.Usage.InputTokens, resp.Usage.OutputTokens)
	}
}

func TestStreamIdleTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		if fl != nil {
			fl.Flush()
		}
		time.Sleep(1 * time.Second) // stall well beyond the idle timeout
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c, err := New(Options{BaseURL: srv.URL, Dialect: "chat-completions", StreamIdleTimeout: 100 * time.Millisecond, MaxRetries: 0})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Generate(context.Background(), provider.GenerationRequest{
		Model: "m", Stream: true,
		Input: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an idle-timeout error from a stalled stream")
	}
	if code := schema.AsToolError(err).Code; code != schema.CodeProviderTimeout {
		t.Errorf("idle timeout error code = %s, want %s", code, schema.CodeProviderTimeout)
	}
}
