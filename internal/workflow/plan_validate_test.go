package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/evidence"
	"github.com/gregpriday/codeexpert/internal/schema"
)

func baseValidPlan(rel string) schema.ImplementationPlan {
	return schema.ImplementationPlan{
		Goal: "do the thing",
		Steps: []schema.PlanStep{{
			ID:         "P1",
			Title:      "edit",
			Objective:  "change it",
			Files:      []schema.FileTarget{{Path: rel, Action: "modify"}},
			Validation: []schema.ValidationStep{{Description: "run tests", Expectation: "they pass"}},
		}},
	}
}

func hasIssue(issues []string, sub string) bool {
	for _, i := range issues {
		if strings.Contains(i, sub) {
			return true
		}
	}
	return false
}

// TestValidatePlanGates exercises each deterministic plan gate, including the new
// symbol-target and impacted-area path validation.
func TestValidatePlanGates(t *testing.T) {
	snap, rel := testSnapshot(t)
	cfg := config.Defaults()
	ctx := context.Background()

	t.Run("valid plan has no issues", func(t *testing.T) {
		evid := evidence.NewStore(snap.ID())
		if issues := validatePlan(ctx, ptr(baseValidPlan(rel)), nil, snap, evid, cfg); len(issues) != 0 {
			t.Fatalf("expected no issues, got %v", issues)
		}
	})

	t.Run("too many steps", func(t *testing.T) {
		tight := config.Defaults()
		tight.Plan.MaxSteps = 1
		p := baseValidPlan(rel)
		p.Steps = append(p.Steps, schema.PlanStep{
			ID: "P2", Title: "second", Files: p.Steps[0].Files, Validation: p.Steps[0].Validation,
		})
		issues := validatePlan(ctx, &p, nil, snap, evidence.NewStore(snap.ID()), tight)
		if !hasIssue(issues, "exceeding the configured maximum") {
			t.Errorf("want max-steps issue, got %v", issues)
		}
	})

	t.Run("empty goal", func(t *testing.T) {
		p := baseValidPlan(rel)
		p.Goal = "  "
		issues := validatePlan(ctx, &p, nil, snap, evidence.NewStore(snap.ID()), cfg)
		if !hasIssue(issues, "goal is empty") {
			t.Errorf("want goal-empty issue, got %v", issues)
		}
	})

	t.Run("duplicate step id", func(t *testing.T) {
		p := baseValidPlan(rel)
		p.Steps = append(p.Steps, p.Steps[0])
		issues := validatePlan(ctx, &p, nil, snap, evidence.NewStore(snap.ID()), cfg)
		if !hasIssue(issues, "duplicate step id") {
			t.Errorf("want duplicate-id issue, got %v", issues)
		}
	})

	t.Run("modify non-existent file", func(t *testing.T) {
		p := baseValidPlan(rel)
		p.Steps[0].Files = []schema.FileTarget{{Path: "nope.go", Action: "modify"}}
		issues := validatePlan(ctx, &p, nil, snap, evidence.NewStore(snap.ID()), cfg)
		if !hasIssue(issues, "non-existent file") {
			t.Errorf("want non-existent-file issue, got %v", issues)
		}
	})

	t.Run("unknown dependency", func(t *testing.T) {
		p := baseValidPlan(rel)
		p.Steps[0].DependsOn = []string{"PX"}
		issues := validatePlan(ctx, &p, nil, snap, evidence.NewStore(snap.ID()), cfg)
		if !hasIssue(issues, "depends on unknown step") {
			t.Errorf("want unknown-dependency issue, got %v", issues)
		}
	})

	t.Run("dependency cycle", func(t *testing.T) {
		p := baseValidPlan(rel)
		p.Steps = []schema.PlanStep{
			{ID: "P1", Title: "a", Files: []schema.FileTarget{{Path: rel, Action: "modify"}}, Validation: p.Steps[0].Validation, DependsOn: []string{"P2"}},
			{ID: "P2", Title: "b", Files: []schema.FileTarget{{Path: rel, Action: "modify"}}, Validation: p.Steps[0].Validation, DependsOn: []string{"P1"}},
		}
		issues := validatePlan(ctx, &p, nil, snap, evidence.NewStore(snap.ID()), cfg)
		if !hasIssue(issues, "cycle") {
			t.Errorf("want cycle issue, got %v", issues)
		}
	})

	t.Run("ungrounded evidence id", func(t *testing.T) {
		p := baseValidPlan(rel)
		p.Steps[0].EvidenceIDs = []string{"E-bogus"}
		issues := validatePlan(ctx, &p, nil, snap, evidence.NewStore(snap.ID()), cfg)
		if !hasIssue(issues, "unknown evidence id") {
			t.Errorf("want unknown-evidence issue, got %v", issues)
		}
	})

	t.Run("symbol in non-existent file", func(t *testing.T) {
		p := baseValidPlan(rel)
		p.Steps[0].Symbols = []schema.SymbolTarget{{Name: "Foo", Path: "nope.go"}}
		issues := validatePlan(ctx, &p, nil, snap, evidence.NewStore(snap.ID()), cfg)
		if !hasIssue(issues, "non-existent file") {
			t.Errorf("want symbol-path issue, got %v", issues)
		}
	})

	t.Run("symbol in created file is allowed", func(t *testing.T) {
		p := baseValidPlan(rel)
		p.Steps[0].Files = []schema.FileTarget{{Path: "brand_new.go", Action: "create"}}
		p.Steps[0].Symbols = []schema.SymbolTarget{{Name: "Foo", Path: "brand_new.go"}}
		issues := validatePlan(ctx, &p, nil, snap, evidence.NewStore(snap.ID()), cfg)
		if hasIssue(issues, "non-existent file") {
			t.Errorf("symbol in a created file should not be flagged, got %v", issues)
		}
	})

	t.Run("impacted area non-existent file", func(t *testing.T) {
		p := baseValidPlan(rel)
		p.ImpactedAreas = []schema.ImpactedArea{{Area: "core", Paths: []string{"nope.go"}}}
		issues := validatePlan(ctx, &p, nil, snap, evidence.NewStore(snap.ID()), cfg)
		if !hasIssue(issues, "impacted area") {
			t.Errorf("want impacted-area issue, got %v", issues)
		}
	})

	t.Run("impacted area directory path tolerated", func(t *testing.T) {
		p := baseValidPlan(rel)
		p.ImpactedAreas = []schema.ImpactedArea{{Area: "core", Paths: []string{"internal/workflow"}}}
		issues := validatePlan(ctx, &p, nil, snap, evidence.NewStore(snap.ID()), cfg)
		if hasIssue(issues, "impacted area") {
			t.Errorf("directory-style area path should be tolerated, got %v", issues)
		}
	})
}

func TestLooksLikeFilePath(t *testing.T) {
	cases := map[string]bool{
		"internal/workflow/review.go": true,
		"main.go":                     true,
		"internal/workflow":           false,
		"cmd/":                        false,
		"README":                      false,
		"":                            false,
	}
	for in, want := range cases {
		if got := looksLikeFilePath(in); got != want {
			t.Errorf("looksLikeFilePath(%q) = %v, want %v", in, got, want)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// TestValidatePlanTraceability proves removing a criterion's coverage causes
// validation failure: every acceptance criterion must map to a real step.
func TestValidatePlanTraceability(t *testing.T) {
	snap, rel := testSnapshot(t)
	cfg := config.Defaults()
	ctx := context.Background()
	criteria := []string{"Retries are idempotent", "Errors are logged"}

	// No traceability map -> criteria are uncovered.
	p := baseValidPlan(rel)
	if issues := validatePlan(ctx, &p, criteria, snap, evidence.NewStore(snap.ID()), cfg); !hasIssue(issues, "not covered by any step") {
		t.Errorf("expected uncovered-criterion issue, got %v", issues)
	}

	// Full coverage mapping each criterion to a real step -> clean.
	p2 := baseValidPlan(rel)
	p2.Traceability = []schema.CriterionCoverage{
		{Criterion: "Retries are idempotent", StepIDs: []string{"P1"}, Tests: []string{"go test ./..."}},
		{Criterion: "Errors are logged", StepIDs: []string{"P1"}},
	}
	if issues := validatePlan(ctx, &p2, criteria, snap, evidence.NewStore(snap.ID()), cfg); len(issues) != 0 {
		t.Errorf("full traceability should be clean, got %v", issues)
	}

	// A criterion mapped to a non-existent step is flagged.
	p3 := baseValidPlan(rel)
	p3.Traceability = []schema.CriterionCoverage{
		{Criterion: "Retries are idempotent", StepIDs: []string{"P9"}},
		{Criterion: "Errors are logged", StepIDs: []string{"P1"}},
	}
	if issues := validatePlan(ctx, &p3, criteria, snap, evidence.NewStore(snap.ID()), cfg); !hasIssue(issues, "unknown step") {
		t.Errorf("expected unknown-step issue, got %v", issues)
	}

	// With no acceptance criteria, traceability is not required.
	p4 := baseValidPlan(rel)
	if issues := validatePlan(ctx, &p4, nil, snap, evidence.NewStore(snap.ID()), cfg); len(issues) != 0 {
		t.Errorf("no criteria should not require traceability, got %v", issues)
	}
}
