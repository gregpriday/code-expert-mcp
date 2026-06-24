package llmtools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/budget"
	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/evidence"
	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// fakeSearchProvider is a minimal provider.Provider that records the inner request
// and returns a canned grounding response.
type fakeSearchProvider struct {
	resp    provider.GenerationResponse
	err     error
	calls   int
	lastReq provider.GenerationRequest
}

func (f *fakeSearchProvider) Generate(_ context.Context, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	f.calls++
	f.lastReq = req
	if f.err != nil {
		return provider.GenerationResponse{}, f.err
	}
	return f.resp, nil
}

func (f *fakeSearchProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}

func (f *fakeSearchProvider) Capabilities(context.Context) provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Dialect: "responses", SupportsWebSearch: true}
}

// groundingRegistry builds a registry with grounding enabled and the given fake
// provider wired in.
func groundingRegistry(t *testing.T, prov provider.Provider) (*Registry, *evidence.Store, config.Config) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Grounding.Enabled = true
	cfg.Grounding.SearchContextSize = "medium"
	root, err := repo.ResolveRoot(repo.DefaultRoot(dir), cfg.Repository.FollowSymlinks, nil)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	snap, err := repo.BuildSnapshot(context.Background(), root, cfg.Repository)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	evid := evidence.NewStore(snap.ID())
	reg := New(Options{Snapshot: snap, Evidence: evid, Budget: budget.New(budget.Limits{}), Config: cfg, Provider: prov})
	return reg, evid, cfg
}

func hasTool(reg *Registry, name string) bool {
	for _, n := range reg.Names() {
		if n == name {
			return true
		}
	}
	return false
}

// TestGroundingDisabledByDefault confirms the web_search tool is absent unless
// grounding is explicitly enabled.
func TestGroundingDisabledByDefault(t *testing.T) {
	reg, _ := testRegistry(t)
	if hasTool(reg, "web_search") {
		t.Error("web_search must not be registered when grounding is disabled")
	}
}

// TestGroundingRequiresProvider confirms enabling grounding without a provider
// does not register the tool (it cannot run without one).
func TestGroundingRequiresProvider(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Grounding.Enabled = true
	root, err := repo.ResolveRoot(repo.DefaultRoot(dir), cfg.Repository.FollowSymlinks, nil)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	snap, err := repo.BuildSnapshot(context.Background(), root, cfg.Repository)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	reg := New(Options{Snapshot: snap, Evidence: evidence.NewStore(snap.ID()), Budget: budget.New(budget.Limits{}), Config: cfg})
	if hasTool(reg, "web_search") {
		t.Error("web_search must not be registered without a provider")
	}
}

// TestWebSearchGroundingResult exercises the happy path: the tool runs the inner
// provider call, returns an untrusted summary with citations, and mints an
// untrusted grounding evidence record.
func TestWebSearchGroundingResult(t *testing.T) {
	prov := &fakeSearchProvider{resp: provider.GenerationResponse{
		Text:    "Go 1.23 was released in August 2024.",
		ModelID: "fugu",
		URLCitations: []provider.URLCitation{
			{URL: "https://go.dev/doc/go1.23", Title: "Go 1.23 Release Notes"},
		},
	}}
	reg, evid, cfg := groundingRegistry(t, prov)
	if !hasTool(reg, "web_search") {
		t.Fatal("web_search should be registered when grounding is enabled with a provider")
	}

	m := execJSON(t, reg, "web_search", `{"query":"go 1.23 release date"}`)
	if m["error"] != nil {
		t.Fatalf("web_search returned an error: %v", m["error"])
	}
	if !strings.Contains(toString(m["note"]), "UNTRUSTED") {
		t.Errorf("web_search result missing the untrusted note: %v", m["note"])
	}
	if !strings.Contains(toString(m["summary"]), "Go 1.23") {
		t.Errorf("summary not propagated: %v", m["summary"])
	}
	cits, ok := m["citations"].([]any)
	if !ok || len(cits) != 1 {
		t.Fatalf("expected 1 citation, got %v", m["citations"])
	}

	evID := toString(m["evidence_id"])
	if evID == "" || !evid.Has(evID) {
		t.Fatalf("evidence_id %q missing or not in store", evID)
	}
	rec, _ := evid.Get(evID)
	if !rec.Provenance.Untrusted {
		t.Errorf("grounding evidence must be flagged untrusted: %+v", rec.Provenance)
	}
	if rec.Provenance.Tool != "web_search" {
		t.Errorf("provenance tool = %q, want web_search", rec.Provenance.Tool)
	}
	if rec.Kind != schema.EvidenceKindGrounding {
		t.Errorf("evidence kind = %q, want grounding", rec.Kind)
	}

	// The inner call must request the web_search built-in on the scout model.
	if prov.calls != 1 {
		t.Errorf("expected exactly one inner provider call, got %d", prov.calls)
	}
	if prov.lastReq.Model != cfg.Models.Scout {
		t.Errorf("inner call model = %q, want scout %q", prov.lastReq.Model, cfg.Models.Scout)
	}
	if len(prov.lastReq.BuiltinTools) != 1 || prov.lastReq.BuiltinTools[0].Type != provider.BuiltinToolWebSearch {
		t.Errorf("inner call must request the web_search built-in: %+v", prov.lastReq.BuiltinTools)
	}
	if prov.lastReq.BuiltinTools[0].SearchContextSize != "medium" {
		t.Errorf("inner call search_context_size = %q, want medium", prov.lastReq.BuiltinTools[0].SearchContextSize)
	}
	if prov.lastReq.StoreState {
		t.Error("inner grounding call must be stateless")
	}
}

// TestWebSearchIsReadOnly confirms the grounding tool name still satisfies the
// read-only surface guard even when grounding is enabled.
func TestWebSearchIsReadOnly(t *testing.T) {
	prov := &fakeSearchProvider{}
	reg, _, _ := groundingRegistry(t, prov)
	banned := []string{"write", "exec", "delete", "remove", "commit", "checkout", "reset", "rebase", "merge", "mkdir", "chmod", "apply", "patch", "move", "rename"}
	for _, name := range reg.Names() {
		lower := strings.ToLower(name)
		for _, b := range banned {
			if strings.Contains(lower, b) {
				t.Errorf("tool %q looks like a mutation tool (matched %q)", name, b)
			}
		}
	}
}

// TestWebSearchPropagatesProviderError confirms a provider failure surfaces as a
// JSON error payload rather than aborting the run.
func TestWebSearchPropagatesProviderError(t *testing.T) {
	prov := &fakeSearchProvider{err: schema.NewError(schema.CodeProviderUnsupported, "web search unsupported")}
	reg, _, _ := groundingRegistry(t, prov)
	m := execJSON(t, reg, "web_search", `{"query":"anything"}`)
	if m["error"] == nil {
		t.Errorf("expected an error payload when the provider fails, got %v", m)
	}
}

// TestWebSearchRejectsEmptyQuery confirms a blank query is rejected before any
// provider call.
func TestWebSearchRejectsEmptyQuery(t *testing.T) {
	prov := &fakeSearchProvider{}
	reg, _, _ := groundingRegistry(t, prov)
	m := execJSON(t, reg, "web_search", `{"query":"   "}`)
	if m["error"] == nil {
		t.Errorf("expected an error payload for an empty query, got %v", m)
	}
	if prov.calls != 0 {
		t.Errorf("empty query must not reach the provider, calls=%d", prov.calls)
	}
}
