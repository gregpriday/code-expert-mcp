package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// probeFake is a provider that succeeds at every capability so runProbe exercises
// all of its branches.
type probeFake struct{}

func (probeFake) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ID: "small"}, {ID: "large"}}, nil
}
func (probeFake) Capabilities(ctx context.Context) provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Dialect: "responses", SupportsTools: true, SupportsStructured: true, SupportsStreaming: true, SupportsReasoning: true}
}
func (probeFake) Generate(ctx context.Context, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	if len(req.Tools) > 0 {
		return provider.GenerationResponse{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "ping"}}, ModelID: req.Model}, nil
	}
	if req.OutputSchema != nil {
		return provider.GenerationResponse{Text: `{"ok":true}`, StructuredJSON: []byte(`{"ok":true}`), ModelID: req.Model}, nil
	}
	return provider.GenerationResponse{Text: "ok", ModelID: req.Model, Usage: provider.Usage{InputTokens: 5, OutputTokens: 2}}, nil
}

func TestDoctorProbeSeparateCapabilities(t *testing.T) {
	cfg := config.Defaults() // small=fugu (scout), large=fugu-ultra (verifier)
	var buf bytes.Buffer
	if code := runProbe(context.Background(), probeFake{}, cfg, &buf); code != exitOK {
		t.Fatalf("probe exit code %d", code)
	}
	out := buf.String()
	for _, want := range []string{"dialect:", "reachable", "small-text", "large-text", "tool-call + continuation", "structured"} {
		if !strings.Contains(out, want) {
			t.Errorf("probe output missing %q:\n%s", want, out)
		}
	}
}

// failingProbe is unreachable; runProbe must hard-fail with a provider error.
type failingProbe struct{ probeFake }

func (failingProbe) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return nil, context.DeadlineExceeded
}

func TestDoctorProbeUnreachableFails(t *testing.T) {
	var buf bytes.Buffer
	if code := runProbe(context.Background(), failingProbe{}, config.Defaults(), &buf); code != exitProviderError {
		t.Errorf("unreachable probe exit = %d, want %d", code, exitProviderError)
	}
}

// authProbe is reachable but rejects the key on generation; runProbe must
// hard-fail.
type authProbe struct{ probeFake }

func (authProbe) Generate(ctx context.Context, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	return provider.GenerationResponse{}, schema.NewError(schema.CodeProviderAuth, "invalid api key")
}

func TestDoctorProbeAuthFails(t *testing.T) {
	var buf bytes.Buffer
	if code := runProbe(context.Background(), authProbe{}, config.Defaults(), &buf); code != exitProviderError {
		t.Errorf("auth failure should hard-fail: got %d, want %d\n%s", code, exitProviderError, buf.String())
	}
}
