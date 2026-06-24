package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/telemetry"
)

// blockingProvider blocks every Generate call until the supplied context is
// cancelled, then reports a cancelled error. It counts how many calls it
// entered and how many unblocked via context cancellation so a test can assert
// that every in-flight pass observed the cancellation rather than hanging or
// running on. The first call on each invocation signals entered so the test can
// cancel only once a pass is genuinely in flight.
type blockingProvider struct {
	entered   chan struct{}
	inFlight  atomic.Int32
	cancelled atomic.Int32
}

func (*blockingProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ID: "fugu"}}, nil
}
func (*blockingProvider) Capabilities(ctx context.Context) provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Dialect: "responses", SupportsTools: true, SupportsStructured: true}
}
func (b *blockingProvider) Generate(ctx context.Context, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	b.inFlight.Add(1)
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	b.cancelled.Add(1)
	return provider.GenerationResponse{}, schema.NewError(schema.CodeCancelled, "context cancelled")
}

// TestRunCandidatePassesCancellation verifies the candidate passes observe
// context cancellation through the errgroup-derived context: once the parent
// context is cancelled mid-flight, every blocked pass unblocks and the review
// completes promptly instead of running on past Wait. The provider blocks on
// the context it receives, so a pass can only return if that context is the
// group's derived context (which Review's runCandidatePasses passes to
// runOnePass). The entered/cancelled counters confirm no in-flight pass leaks
// past cancellation.
func TestRunCandidatePassesCancellation(t *testing.T) {
	dir, rel := tempGitRepo(t)
	// Working-tree change so there is a diff to review (else Review returns early
	// before ever reaching the candidate passes).
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("package main\n\nfunc main() {\n\tprintln(\"changed\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.Cache.Enabled = false
	prov := &blockingProvider{entered: make(chan struct{}, 8)}
	eng := &Engine{Cfg: cfg, Provider: prov, Log: telemetry.Nop()}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		res schema.ReviewResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := eng.Review(ctx, schema.ReviewRequest{
			Root: dir, Target: schema.ReviewTarget{Type: schema.TargetWorkingTree},
		}, RunOptions{})
		done <- result{res, err}
	}()

	// Wait until at least one pass is blocked inside the provider, then cancel.
	select {
	case <-prov.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no candidate pass reached the provider within 5s")
	}
	cancel()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Review returned error after cancellation: %v", r.err)
		}
		if len(r.res.Findings) != 0 {
			t.Errorf("expected no findings after cancellation, got %d", len(r.res.Findings))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Review did not return within 5s of cancellation — passes did not observe gCtx cancellation")
	}

	// Every pass that entered the provider must have unblocked via cancellation;
	// none ran on or leaked. (Review has returned, so all goroutines are done.)
	if in, out := prov.inFlight.Load(), prov.cancelled.Load(); in != out || in == 0 {
		t.Errorf("in-flight passes = %d, cancelled-returns = %d; want equal and non-zero", in, out)
	}
}

// TestBuildDiffBlockKeepsEveryFile proves the per-file partition never silently
// drops a changed file: with many files each still appears with its header, even
// when the total diff budget is exhausted (the old single-string truncation
// broke out of the loop and lost every later file).
func TestBuildDiffBlockKeepsEveryFile(t *testing.T) {
	bigHunk := "@@ -1,1 +1,400 @@\n"
	for i := 0; i < 400; i++ {
		bigHunk += "+a line of added content that is reasonably long to consume budget\n"
	}
	m := &repo.ChangeManifest{Target: "working_tree", BaseLabel: "HEAD", HeadLabel: "wt"}
	for i := 0; i < 60; i++ {
		m.Files = append(m.Files, repo.ChangedFile{
			Path: fmt.Sprintf("pkg/file%02d.go", i), Status: "M", Added: 400, Diff: bigHunk,
		})
	}
	out := buildDiffBlock(m)
	for i := 0; i < 60; i++ {
		if !strings.Contains(out, fmt.Sprintf("pkg/file%02d.go", i)) {
			t.Fatalf("file%02d.go disappeared from the diff block — a changed file was silently dropped", i)
		}
	}
	// And the total stays bounded rather than embedding 60*~25KB of diff.
	if len(out) > diffBlockBudget*2 {
		t.Errorf("diff block size %d exceeds twice the budget %d", len(out), diffBlockBudget)
	}
}

// TestReviewableFilesIncludesDeletions proves deletions stay in review scope.
func TestReviewableFilesIncludesDeletions(t *testing.T) {
	m := &repo.ChangeManifest{Files: []repo.ChangedFile{
		{Path: "kept.go", Status: "M"},
		{Path: "removed.go", Status: "D"},
		{Path: "vendor/dep.go", Status: "D", Vendored: true},
		{Path: "image.png", Status: "A", Binary: true},
	}}
	got := reviewableFiles(m, config.Defaults(), schema.ReviewPolicy{})
	paths := map[string]bool{}
	for _, f := range got {
		paths[f.Path] = true
	}
	if !paths["removed.go"] {
		t.Error("a source deletion must remain reviewable")
	}
	if paths["vendor/dep.go"] || paths["image.png"] {
		t.Error("vendored/binary files must stay out of scope")
	}
}

// TestReviewRunsConfiguredCheckInIsolation proves verification modes are real: a
// configured safe check actually executes during review, surfaces as a
// CheckResult, and leaves the protected repository untouched.
func TestReviewRunsConfiguredCheckInIsolation(t *testing.T) {
	dir, rel := tempGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("package main\n\nfunc main() {\n\tvar x *int\n\t_ = *x\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Cache.Enabled = false
	cfg.Checks = config.ChecksConfig{
		Mode: "off",
		Command: []config.CheckCommand{
			{Name: "git-version", Argv: []string{"git", "--version"}, Enabled: true, Safe: true},
		},
	}
	eng := &Engine{Cfg: cfg, Provider: &fakeProvider{existingFile: rel}, Log: telemetry.Nop()}

	before := fingerprintTree(t, dir)
	res, err := eng.Review(context.Background(), schema.ReviewRequest{
		Root:         dir,
		Target:       schema.ReviewTarget{Type: schema.TargetWorkingTree},
		Verification: schema.VerifySafe,
	}, RunOptions{})
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if len(res.Checks) != 1 || !res.Checks[0].Passed {
		t.Fatalf("expected the configured safe check to run and pass, got %+v", res.Checks)
	}
	if after := fingerprintTree(t, dir); after != before {
		t.Fatal("review with verification mutated the repository — no-write invariant violated")
	}
}
