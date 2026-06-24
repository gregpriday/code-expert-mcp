package llmtools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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
	resp        provider.GenerationResponse
	err         error
	noWebSearch bool // when true, Capabilities reports SupportsWebSearch=false
	calls       int
	lastReq     provider.GenerationRequest
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
	return provider.ProviderCapabilities{Dialect: "responses", SupportsWebSearch: !f.noWebSearch}
}

// groundingRegistry builds a registry with grounding enabled, the given fake
// provider, and a budget tracker with the supplied limits.
func groundingRegistry(t *testing.T, prov provider.Provider, limits budget.Limits) (*Registry, *evidence.Store, config.Config, *budget.Tracker) {
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
	tracker := budget.New(limits)
	reg := New(Options{Snapshot: snap, Evidence: evid, Budget: tracker, Config: cfg, Provider: prov})
	return reg, evid, cfg, tracker
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
	reg, evid, cfg, _ := groundingRegistry(t, prov, budget.Limits{})
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
	reg, _, _, _ := groundingRegistry(t, prov, budget.Limits{})
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
	reg, _, _, _ := groundingRegistry(t, prov, budget.Limits{})
	m := execJSON(t, reg, "web_search", `{"query":"anything"}`)
	if m["error"] == nil {
		t.Errorf("expected an error payload when the provider fails, got %v", m)
	}
}

// TestWebSearchRejectsEmptyQuery confirms a blank query is rejected before any
// provider call.
func TestWebSearchRejectsEmptyQuery(t *testing.T) {
	prov := &fakeSearchProvider{}
	reg, _, _, _ := groundingRegistry(t, prov, budget.Limits{})
	m := execJSON(t, reg, "web_search", `{"query":"   "}`)
	if m["error"] == nil {
		t.Errorf("expected an error payload for an empty query, got %v", m)
	}
	if prov.calls != 0 {
		t.Errorf("empty query must not reach the provider, calls=%d", prov.calls)
	}
}

// TestGroundingNotRegisteredWhenProviderLacksWebSearch confirms the tool is not
// advertised when the provider cannot run a built-in web search (e.g. the
// chat-completions dialect), so the model never burns budget on a doomed call.
func TestGroundingNotRegisteredWhenProviderLacksWebSearch(t *testing.T) {
	prov := &fakeSearchProvider{noWebSearch: true}
	reg, _, _, _ := groundingRegistry(t, prov, budget.Limits{})
	if hasTool(reg, "web_search") {
		t.Error("web_search must not be registered when the provider lacks web-search support")
	}
}

// TestWebSearchTruncatesQueryAtRuneBoundary feeds an over-long query ending in a
// multi-byte rune and confirms the call succeeds with a valid UTF-8 query (no
// invalid bytes leak into the evidence record).
func TestWebSearchTruncatesQueryAtRuneBoundary(t *testing.T) {
	prov := &fakeSearchProvider{resp: provider.GenerationResponse{Text: "ok", ModelID: "fugu"}}
	reg, _, _, _ := groundingRegistry(t, prov, budget.Limits{})
	long := strings.Repeat("a", maxGroundingQuery-1) + "é" // last rune is 2 bytes, crossing the cap
	m := execJSON(t, reg, "web_search", `{"query":`+jsonString(long)+`}`)
	if m["error"] != nil {
		t.Fatalf("over-long query should not error: %v", m["error"])
	}
	if !utf8.ValidString(prov.lastReq.Input[0].Content) {
		t.Errorf("inner query is not valid UTF-8: %q", prov.lastReq.Input[0].Content)
	}
	if got := utf8.RuneCountInString(prov.lastReq.Input[0].Content); got > maxGroundingQuery {
		t.Errorf("inner query exceeds the rune cap: %d > %d", got, maxGroundingQuery)
	}
}

// TestWebSearchEmptyInnerResponse pins the behavior when the search returns no
// citations: a result (not an error) with an empty citation list and an explicit
// ungrounded warning in the note.
func TestWebSearchEmptyInnerResponse(t *testing.T) {
	prov := &fakeSearchProvider{resp: provider.GenerationResponse{Text: "", ModelID: "fugu"}}
	reg, _, _, _ := groundingRegistry(t, prov, budget.Limits{})
	m := execJSON(t, reg, "web_search", `{"query":"obscure thing"}`)
	if m["error"] != nil {
		t.Fatalf("empty inner response should not be an error: %v", m["error"])
	}
	cits, ok := m["citations"].([]any)
	if !ok || len(cits) != 0 {
		t.Errorf("expected an empty citation list, got %v", m["citations"])
	}
	if cs, _ := m["cited_sources"].(float64); cs != 0 {
		t.Errorf("cited_sources = %v, want 0", m["cited_sources"])
	}
	note := toString(m["note"])
	if !strings.Contains(note, "ungrounded") || !strings.Contains(note, "UNTRUSTED") {
		t.Errorf("empty-result note should warn about being ungrounded and untrusted: %q", note)
	}
}

// TestWebSearchForwardsDomainFilters confirms the configured allow/block domain
// filters reach the inner provider request.
func TestWebSearchForwardsDomainFilters(t *testing.T) {
	prov := &fakeSearchProvider{resp: provider.GenerationResponse{Text: "ok", ModelID: "fugu"}}
	reg, _, _, _ := groundingRegistry(t, prov, budget.Limits{})
	reg.cfg.Grounding.AllowedDomains = []string{"go.dev"}
	reg.cfg.Grounding.BlockedDomains = []string{"spam.example"}
	if m := execJSON(t, reg, "web_search", `{"query":"q"}`); m["error"] != nil {
		t.Fatalf("unexpected error: %v", m["error"])
	}
	bt := prov.lastReq.BuiltinTools
	if len(bt) != 1 {
		t.Fatalf("expected one built-in tool, got %+v", bt)
	}
	if len(bt[0].AllowedDomains) != 1 || bt[0].AllowedDomains[0] != "go.dev" {
		t.Errorf("allowed domains not forwarded: %v", bt[0].AllowedDomains)
	}
	if len(bt[0].BlockedDomains) != 1 || bt[0].BlockedDomains[0] != "spam.example" {
		t.Errorf("blocked domains not forwarded: %v", bt[0].BlockedDomains)
	}
}

// TestWebSearchRespectsModelCallBudget confirms an exhausted model-call budget
// blocks the grounding call before it reaches the provider.
func TestWebSearchRespectsModelCallBudget(t *testing.T) {
	prov := &fakeSearchProvider{resp: provider.GenerationResponse{Text: "ok", ModelID: "fugu"}}
	reg, _, _, tracker := groundingRegistry(t, prov, budget.Limits{MaxModelCalls: 1})
	if err := tracker.ChargeModelCall(); err != nil { // exhaust the single allowed call
		t.Fatalf("priming the budget should succeed: %v", err)
	}
	m := execJSON(t, reg, "web_search", `{"query":"q"}`)
	if m["error"] == nil {
		t.Errorf("expected a budget-exhaustion error payload, got %v", m)
	}
	if prov.calls != 0 {
		t.Errorf("an exhausted budget must not reach the provider, calls=%d", prov.calls)
	}
}

// jsonString encodes s as a JSON string literal (including quotes).
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
