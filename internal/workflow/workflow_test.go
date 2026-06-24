package workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/telemetry"
)

// fakeProvider drives the pipeline deterministically by branching on the request
// shape (tools => exploration, output schema name => synthesis stage).
type fakeProvider struct{ existingFile string }

func (f *fakeProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ID: "fugu"}}, nil
}
func (f *fakeProvider) Capabilities(ctx context.Context) provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Dialect: "responses", SupportsTools: true, SupportsStructured: true}
}

func (f *fakeProvider) Generate(ctx context.Context, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	// Exploration round: end immediately with no tool calls.
	if len(req.Tools) > 0 {
		return provider.GenerationResponse{Text: "Exploration complete.", ModelID: req.Model}, nil
	}
	name := ""
	if req.OutputSchema != nil {
		name = req.OutputSchema.Name
	}
	var body string
	switch {
	case strings.Contains(name, "implementation_plan"):
		body = `{"goal":"Test goal","current_behavior":[],"constraints":[],"assumptions":[],"impacted_areas":[],"recommended_approach":"Do the thing","steps":[{"id":"P1","title":"Edit file","objective":"change it","depends_on":[],"files":[{"path":"` + f.existingFile + `","action":"modify"}],"detailed_changes":["change x"],"invariants":[],"validation":[{"description":"run tests"}],"risks":[],"evidence_ids":[]}],"test_plan":[{"description":"go test"}],"compatibility":[],"risks":[],"alternatives":[],"open_questions":[],"definition_of_done":["done"],"agent_handoff":"go"}`
	case strings.Contains(name, "help_report"):
		body = `{"direct_answer":"It crashes because a shared map is written from two goroutines without a lock.","answer_type":"diagnose","recommended_next_action":"add a mutex around the map writes","problem_restatement":"It breaks","verified_facts":[],"inferences":[],"likely_causes":[{"hypothesis":"race","likelihood":"medium","verified":false}],"recommended_direction":"add a lock","investigation_steps":[],"validation_steps":[],"alternatives":[],"risks":[],"assumptions":[],"confidence":"medium"}`
	case strings.Contains(name, "review_candidates"):
		body = `{"findings":[{"title":"Possible nil deref","category":"correctness","severity":"high","location":{"path":"` + f.existingFile + `","start_line":1,"end_line":2},"claim":"may panic","trigger":"when x is nil","impact":"crash","recommendation":"check nil first","assumptions":[],"evidence_ids":[]}]}`
	case strings.Contains(name, "review_verdicts"):
		body = `{"verdicts":[{"index":0,"keep":true,"evidence_level":"C","severity":"high","blocking":true,"reason":"plausible"}]}`
	default:
		body = `{}`
	}
	return provider.GenerationResponse{Text: body, StructuredJSON: []byte(body), ModelID: req.Model}, nil
}

func tempGitRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	rel := "main.go"
	content := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir, rel
}

func newTestEngine(existing string) *Engine {
	cfg := config.Defaults()
	cfg.Cache.Enabled = false
	return &Engine{Cfg: cfg, Provider: &fakeProvider{existingFile: existing}, Log: telemetry.Nop()}
}

func TestPlanEndToEnd(t *testing.T) {
	dir, rel := tempGitRepo(t)
	eng := newTestEngine(rel)
	res, err := eng.Plan(context.Background(), schema.PlanRequest{
		Root: dir, Instructions: "Add a greeting flag to main.go",
	}, RunOptions{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if res.Plan == nil {
		t.Fatal("expected a plan")
	}
	if res.Status != schema.StatusComplete {
		t.Errorf("status = %s, want complete", res.Status)
	}
	if len(res.Plan.Steps) != 1 || res.Plan.Steps[0].ID != "P1" {
		t.Errorf("unexpected steps: %+v", res.Plan.Steps)
	}
	if !strings.Contains(res.Markdown, "Implementation plan") {
		t.Errorf("markdown missing header: %s", res.Markdown[:min(120, len(res.Markdown))])
	}
}

func TestHelpEndToEnd(t *testing.T) {
	dir, rel := tempGitRepo(t)
	eng := newTestEngine(rel)
	res, err := eng.Help(context.Background(), schema.HelpRequest{
		Root: dir, Question: "Why does it crash?",
	}, RunOptions{})
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	if res.Help == nil {
		t.Fatal("expected a help report")
	}
	if len(res.Help.LikelyCauses) == 0 {
		t.Error("expected likely causes")
	}
}

// TestContextTokenLimitYieldsPartial proves the context-token ceiling is actually
// enforced: an absurdly low limit makes every model call exceed it, and the run
// returns a truthful partial result (not a hard error, not a false "complete").
func TestContextTokenLimitYieldsPartial(t *testing.T) {
	dir, rel := tempGitRepo(t)
	cfg := config.Defaults()
	cfg.Cache.Enabled = false
	cfg.Retrieval.MaxContextTokens = 1 // every request exceeds one token
	eng := &Engine{Cfg: cfg, Provider: &fakeProvider{existingFile: rel}, Log: telemetry.Nop()}

	res, err := eng.Plan(context.Background(), schema.PlanRequest{
		Root: dir, Instructions: "Add a greeting flag to main.go",
	}, RunOptions{})
	if err != nil {
		t.Fatalf("a context-budget overflow must yield a partial result, not an error: %v", err)
	}
	if res.Status != schema.StatusPartial {
		t.Errorf("status = %s, want partial under a 1-token context budget", res.Status)
	}
	if res.Plan != nil {
		t.Error("no plan should be synthesized when every model call is blocked by the context budget")
	}
	if len(res.Limitations) == 0 {
		t.Error("a partial result must carry a limitation explaining why")
	}
}

func TestReviewEndToEnd(t *testing.T) {
	dir, rel := tempGitRepo(t)
	// Make a working-tree change so there is a diff to review.
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("package main\n\nfunc main() {\n\tprintln(\"changed\")\n\tvar x *int\n\t_ = *x\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(rel)
	res, err := eng.Review(context.Background(), schema.ReviewRequest{
		Root: dir, Target: schema.ReviewTarget{Type: schema.TargetWorkingTree},
	}, RunOptions{})
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(res.Findings))
	}
	if res.Findings[0].Severity != schema.SeverityHigh || !res.Findings[0].Blocking {
		t.Errorf("unexpected finding: %+v", res.Findings[0])
	}
	if strings.Contains(strings.ToLower(res.Markdown), "safe to merge") || strings.Contains(strings.ToLower(res.Markdown), "approved") {
		t.Error("review must never claim safe-to-merge or approved")
	}
}

func TestReviewNoFindings(t *testing.T) {
	dir, rel := tempGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("package main\n\nfunc main() {\n\tprintln(\"changed only\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Provider that produces no candidates.
	cfg := config.Defaults()
	cfg.Cache.Enabled = false
	eng := &Engine{Cfg: cfg, Provider: &emptyProvider{}, Log: telemetry.Nop()}
	res, err := eng.Review(context.Background(), schema.ReviewRequest{Root: dir}, RunOptions{})
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Errorf("expected no findings, got %d", len(res.Findings))
	}
	if !strings.Contains(res.Markdown, "No blocking findings were found") {
		t.Errorf("expected no-findings message, got: %s", res.Markdown)
	}
}

type emptyProvider struct{}

func (emptyProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) { return nil, nil }
func (emptyProvider) Capabilities(ctx context.Context) provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
}
func (emptyProvider) Generate(ctx context.Context, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	if len(req.Tools) > 0 {
		return provider.GenerationResponse{ModelID: req.Model}, nil
	}
	body := `{"findings":[]}`
	return provider.GenerationResponse{Text: body, StructuredJSON: []byte(body), ModelID: req.Model}, nil
}

// errorProvider fails every Generate call, simulating a provider outage.
type errorProvider struct{}

func (errorProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) { return nil, nil }
func (errorProvider) Capabilities(ctx context.Context) provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Dialect: "responses", SupportsTools: true, SupportsStructured: true}
}
func (errorProvider) Generate(ctx context.Context, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	return provider.GenerationResponse{}, fmt.Errorf("simulated provider failure")
}

// TestPlanPropagatesProviderError confirms that a hard provider failure during
// synthesis surfaces as an error from Plan rather than a silent empty result.
func TestPlanPropagatesProviderError(t *testing.T) {
	dir, _ := tempGitRepo(t)
	cfg := config.Defaults()
	cfg.Cache.Enabled = false
	eng := &Engine{Cfg: cfg, Provider: errorProvider{}, Log: telemetry.Nop()}
	_, err := eng.Plan(context.Background(), schema.PlanRequest{
		Root: dir, Instructions: "Add a greeting flag",
	}, RunOptions{})
	if err == nil {
		t.Fatal("expected Plan to propagate the provider failure")
	}
}

// TestSessionRequestsAreStreamed verifies the session sets Stream=true on the
// provider requests it issues (NewSession always streams).
func TestSessionRequestsAreStreamed(t *testing.T) {
	dir, rel := tempGitRepo(t)
	rec := &recordingProvider{inner: &fakeProvider{existingFile: rel}}
	cfg := config.Defaults()
	cfg.Cache.Enabled = false
	eng := &Engine{Cfg: cfg, Provider: rec, Log: telemetry.Nop()}
	if _, err := eng.Plan(context.Background(), schema.PlanRequest{
		Root: dir, Instructions: "Add a greeting flag",
	}, RunOptions{}); err != nil {
		t.Fatalf("plan: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.calls) == 0 {
		t.Fatal("expected the session to issue at least one provider request")
	}
	streamed := false
	for _, req := range rec.calls {
		if req.Stream {
			streamed = true
			break
		}
	}
	if !streamed {
		t.Error("expected at least one streamed request from the session")
	}
}

// TestReviewEmptyChangeSet exercises the early-exit branch: a clean repository
// with no working-tree changes has nothing to review, so Review returns zero
// findings with an explicit scope limitation (and never claims approval).
func TestReviewEmptyChangeSet(t *testing.T) {
	dir, rel := tempGitRepo(t) // committed, clean working tree
	eng := newTestEngine(rel)
	res, err := eng.Review(context.Background(), schema.ReviewRequest{
		Root: dir, Target: schema.ReviewTarget{Type: schema.TargetWorkingTree},
	}, RunOptions{})
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Errorf("clean repo should yield no findings, got %d", len(res.Findings))
	}
	foundScope := false
	for _, l := range res.Limitations {
		if strings.Contains(l.Message, "no reviewable changed files") {
			foundScope = true
		}
	}
	if !foundScope {
		t.Errorf("expected a scope limitation for the empty change set, got %+v", res.Limitations)
	}
	if !strings.Contains(res.Markdown, "No blocking findings were found") {
		t.Errorf("expected the no-findings message, got: %s", res.Markdown)
	}
	if strings.Contains(strings.ToLower(res.Markdown), "safe to merge") || strings.Contains(strings.ToLower(res.Markdown), "approved") {
		t.Error("review must never claim safe-to-merge or approved")
	}
}
