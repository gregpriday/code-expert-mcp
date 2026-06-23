package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gregpriday/codeexpert/internal/evidence"
	"github.com/gregpriday/codeexpert/internal/prompts"
	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// TestEvidenceCatalogDedupesAcrossCandidates proves the catalog renders a record
// cited by several candidates exactly once — the core token-waste fix for the
// verifier prompt.
func TestEvidenceCatalogDedupesAcrossCandidates(t *testing.T) {
	store := evidence.NewStore("snap")
	store.Add(schema.EvidenceRecord{ID: "E-1", Kind: schema.EvidenceKindFile, Path: "main.go", StartLine: 1, EndLine: 2, Summary: "shared record"})
	cands := []candidateFinding{
		mkCandidate("main.go", 1, 2, schema.CategoryCorrectness, "fix", "E-1"),
		mkCandidate("main.go", 2, 3, schema.CategoryCorrectness, "fix", "E-1"),
		mkCandidate("main.go", 3, 4, schema.CategoryCorrectness, "fix", "E-1"),
	}
	out := renderEvidenceForVerifier(store, cands)
	if got := strings.Count(out, "shared record"); got != 1 {
		t.Errorf("record cited by 3 candidates should render once, rendered %d times:\n%s", got, out)
	}
	if got := strings.Count(out, "E-1"); got != 1 {
		t.Errorf("evidence ID should appear once in the catalog, appeared %d times:\n%s", got, out)
	}
}

// TestEvidenceCatalogFlagsMissingOnce proves a fabricated ID cited by multiple
// candidates is flagged exactly once, not once per citation.
func TestEvidenceCatalogFlagsMissingOnce(t *testing.T) {
	store := evidence.NewStore("snap")
	cands := []candidateFinding{
		mkCandidate("main.go", 1, 2, schema.CategoryCorrectness, "fix", "E-missing"),
		mkCandidate("main.go", 2, 3, schema.CategoryCorrectness, "fix", "E-missing"),
	}
	out := renderEvidenceForVerifier(store, cands)
	if got := strings.Count(out, "NOT FOUND"); got != 1 {
		t.Errorf("missing ID should be flagged once, flagged %d times:\n%s", got, out)
	}
	if got := strings.Count(out, "E-missing"); got != 1 {
		t.Errorf("missing ID should be listed once, listed %d times:\n%s", got, out)
	}
}

// TestEvidenceCatalogTrimsIDs proves a whitespace-padded ID resolves to its
// record (deduped against the clean form) instead of being flagged NOT FOUND.
func TestEvidenceCatalogTrimsIDs(t *testing.T) {
	store := evidence.NewStore("snap")
	store.Add(schema.EvidenceRecord{ID: "E-1", Kind: schema.EvidenceKindFile, Path: "main.go", StartLine: 1, EndLine: 2, Summary: "padded record"})
	cands := []candidateFinding{
		mkCandidate("main.go", 1, 2, schema.CategoryCorrectness, "fix", " E-1 "),
		mkCandidate("main.go", 2, 3, schema.CategoryCorrectness, "fix", "E-1"),
		mkCandidate("main.go", 3, 4, schema.CategoryCorrectness, "fix", "E-missing"),
	}
	out := renderEvidenceForVerifier(store, cands)
	if strings.Contains(out, "NOT FOUND in evidence store (do not trust): E-1") {
		t.Errorf("padded ' E-1 ' should resolve to its record, not be flagged missing:\n%s", out)
	}
	if got := strings.Count(out, "padded record"); got != 1 {
		t.Errorf("the record should render exactly once across padded + clean citations, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "E-missing") || strings.Count(out, "NOT FOUND") != 1 {
		t.Errorf("the genuinely missing ID should be flagged once:\n%s", out)
	}
}

// TestRenderCandidateEvidenceRefs proves per-candidate lines stay compact: IDs
// only when cited, an explicit note when not.
func TestRenderCandidateEvidenceRefs(t *testing.T) {
	if got := renderCandidateEvidenceRefs([]string{"E-1", "E-2"}); !strings.Contains(got, "E-1, E-2") || strings.Contains(got, "—") {
		t.Errorf("cited refs should be a compact ID list, got: %q", got)
	}
	if got := renderCandidateEvidenceRefs(nil); !strings.Contains(got, "none cited") {
		t.Errorf("no-evidence refs should say 'none cited', got: %q", got)
	}
	if got := renderCandidateEvidenceRefs([]string{"  "}); !strings.Contains(got, "none cited") {
		t.Errorf("blank ids should be treated as none cited, got: %q", got)
	}
}

// recordingProvider wraps another provider and captures every request so a test
// can assert on the exact prompt shape sent to the model.
type recordingProvider struct {
	inner provider.Provider
	mu    sync.Mutex
	calls []provider.GenerationRequest
}

func (r *recordingProvider) Generate(ctx context.Context, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req)
	r.mu.Unlock()
	return r.inner.Generate(ctx, req)
}
func (r *recordingProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return r.inner.ListModels(ctx)
}
func (r *recordingProvider) Capabilities(ctx context.Context) provider.ProviderCapabilities {
	return r.inner.Capabilities(ctx)
}

// TestCandidatePassesShareStablePrefix proves the restructuring that enables
// prompt caching: every candidate pass sends an identical system prompt
// (CommonSystem alone) and an identical first user message (the frozen diff),
// with only the second message — the per-pass brief — diverging.
func TestCandidatePassesShareStablePrefix(t *testing.T) {
	dir, rel := tempGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("package main\n\nfunc main() {\n\tprintln(\"changed\")\n\tvar x *int\n\t_ = *x\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(rel)
	rec := &recordingProvider{inner: eng.Provider}
	eng.Provider = rec
	// Balanced profile runs all three candidate passes.
	if _, err := eng.Review(context.Background(), schema.ReviewRequest{
		Root: dir, Profile: schema.ProfileBalanced, Target: schema.ReviewTarget{Type: schema.TargetWorkingTree},
	}, RunOptions{}); err != nil {
		t.Fatalf("review: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()

	const diffMarker = "# Frozen diff (UNTRUSTED content)"
	var sharedInstr, sharedFirst string
	briefs := map[string]bool{}
	n := 0
	for _, c := range rec.calls {
		// Count only the per-pass synthesis calls (one per pass), not exploration
		// rounds — both carry the shared prefix, but the synthesis call is the unit
		// of "one pass" we are asserting parity across.
		if c.OutputSchema == nil || c.OutputSchema.Name != "review_candidates" || len(c.Input) < 2 {
			continue
		}
		if !strings.Contains(c.Input[0].Content, diffMarker) {
			t.Fatalf("candidate-pass first user message should carry the frozen diff, got:\n%s", c.Input[0].Content)
		}
		if n == 0 {
			sharedInstr = c.Instructions
			sharedFirst = c.Input[0].Content
		}
		n++
		if c.Instructions != sharedInstr {
			t.Errorf("candidate passes must share one system prompt;\n got: %q\nwant: %q", c.Instructions, sharedInstr)
		}
		if c.Input[0].Content != sharedFirst {
			t.Error("candidate passes must share a byte-identical first user message (the frozen diff)")
		}
		briefs[c.Input[1].Content] = true
	}
	if n != 3 {
		t.Fatalf("expected exactly 3 candidate-pass synthesis calls (3 passes), got %d", n)
	}
	if sharedInstr != prompts.MustGet(prompts.CommonSystem) {
		t.Errorf("candidate system prompt should be exactly CommonSystem, got:\n%s", sharedInstr)
	}
	if len(briefs) < 3 {
		t.Errorf("expected 3 distinct per-pass briefs in the second user message, got %d", len(briefs))
	}
}
