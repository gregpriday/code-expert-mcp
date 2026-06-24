package eval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/telemetry"
	"github.com/gregpriday/codeexpert/internal/workflow"
)

// stubProvider returns deterministic, valid structured output so the harness can
// be exercised end-to-end in CI without a live model.
type stubProvider struct{ file string }

func (stubProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ID: "fugu"}}, nil
}
func (stubProvider) Capabilities(context.Context) provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Dialect: "responses", SupportsTools: true, SupportsStructured: true}
}
func (s stubProvider) Generate(_ context.Context, req provider.GenerationRequest) (provider.GenerationResponse, error) {
	if len(req.Tools) > 0 {
		return provider.GenerationResponse{Text: "done", ModelID: req.Model}, nil
	}
	name := ""
	if req.OutputSchema != nil {
		name = req.OutputSchema.Name
	}
	var body string
	switch {
	case strings.Contains(name, "implementation_plan"):
		body = `{"goal":"Goal","current_behavior":[],"constraints":[],"assumptions":[],"impacted_areas":[],"recommended_approach":"x","steps":[{"id":"P1","title":"Edit","objective":"o","depends_on":[],"files":[{"path":"` + s.file + `","action":"modify"}],"detailed_changes":["c"],"invariants":[],"validation":[{"description":"go test"}],"risks":[],"evidence_ids":[]}],"test_plan":[{"description":"go test"}],"compatibility":[],"risks":[],"alternatives":[],"open_questions":[],"definition_of_done":["done"],"agent_handoff":"go"}`
	case strings.Contains(name, "review_candidates"):
		body = `{"findings":[{"title":"Nil deref","category":"correctness","severity":"high","location":{"path":"` + s.file + `","start_line":1,"end_line":2},"claim":"c","trigger":"t","impact":"i","recommendation":"fix","assumptions":[],"evidence_ids":[]}]}`
	case strings.Contains(name, "review_verdicts"):
		body = `{"verdicts":[{"index":0,"keep":true,"evidence_level":"C","severity":"high","blocking":true,"reason":"ok"}]}`
	default:
		body = `{}`
	}
	return provider.GenerationResponse{Text: body, StructuredJSON: []byte(body), ModelID: req.Model}, nil
}

func tempRepo(t *testing.T) (string, string) {
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
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir, rel
}

func newEngine(file string) *workflow.Engine {
	cfg := config.Defaults()
	cfg.Cache.Enabled = false
	return &workflow.Engine{Cfg: cfg, Provider: stubProvider{file: file}, Log: telemetry.Nop()}
}

// TestHarnessRunsAndGrades proves the eval harness runs cases end-to-end and the
// deterministic graders all pass on a well-formed run — including the no-write
// invariant — and that an adversarial invalid request is graded as a correct
// expected-error.
func TestHarnessRunsAndGrades(t *testing.T) {
	dir, rel := tempRepo(t)
	eng := newEngine(rel)
	// A working-tree change for the review case.
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("package main\n\nfunc main() {\n\tvar x *int\n\t_ = *x\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []Case{
		{
			ID: "plan-basic", Capability: CapPlan,
			Plan:   &schema.PlanRequest{Root: dir, Instructions: "Add a flag"},
			Expect: Expectation{MustComplete: true, MinSteps: 1},
		},
		{
			ID: "review-finds-bug", Capability: CapReview,
			Review: &schema.ReviewRequest{Root: dir, Target: schema.ReviewTarget{Type: schema.TargetWorkingTree}},
			Expect: Expectation{MinFindings: 1, ForbidApprovalLang: true, ExpectFindingPaths: []string{rel}},
		},
		{
			ID: "adversarial-empty-instructions", Capability: CapPlan,
			Plan:   &schema.PlanRequest{Instructions: ""},
			Expect: Expectation{MustErrorCode: schema.CodeInvalidArgument},
		},
	}

	rep := RunCases(context.Background(), eng, cases, CodeGraders())
	if rep.Total != 3 {
		t.Fatalf("expected 3 cases, got %d", rep.Total)
	}
	for _, cr := range rep.Cases {
		if !cr.Passed {
			t.Errorf("case %s failed: %+v", cr.CaseID, cr.Grades)
		}
	}
	if rep.Passed != 3 {
		t.Errorf("expected all 3 cases to pass, got %d", rep.Passed)
	}
	if rep.ByGrader["no_write"] != 1 {
		t.Errorf("no_write grader pass-rate = %v, want 1", rep.ByGrader["no_write"])
	}
}

// TestNoWriteGraderCatchesMutation proves the no-write grader actually fails when
// the tree changes between fingerprints.
func TestNoWriteGraderCatchesMutation(t *testing.T) {
	g := noWriteGrader{}
	if r := g.Grade(context.Background(), Outcome{RepoBefore: "a", RepoAfter: "b"}); r.Pass {
		t.Error("no_write grader must fail when the tree changed")
	}
	if r := g.Grade(context.Background(), Outcome{RepoBefore: "a", RepoAfter: "a"}); !r.Pass {
		t.Error("no_write grader must pass when the tree is unchanged")
	}
}

func TestLoadCases(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c1.json"),
		[]byte(`{"id":"c1","capability":"help","help":{"question":"why?"},"expect":{"require_direct_answer":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cases) != 1 || cases[0].ID != "c1" || cases[0].Capability != CapHelp {
		t.Fatalf("unexpected loaded cases: %+v", cases)
	}
}

// TestSeedCorpusParses guards the shipped example cases against JSON rot.
func TestSeedCorpusParses(t *testing.T) {
	cases, err := LoadCases("corpus")
	if err != nil {
		t.Fatalf("loading seed corpus: %v", err)
	}
	if len(cases) < 4 {
		t.Errorf("expected at least 4 seed cases, got %d", len(cases))
	}
	for _, c := range cases {
		if c.ID == "" || c.Capability == "" {
			t.Errorf("seed case missing id/capability: %+v", c)
		}
	}
}
