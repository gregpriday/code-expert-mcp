package workflow

import (
	"testing"

	"github.com/gregpriday/codeexpert/internal/schema"
)

func wantInvalid(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected a validation error, got nil", what)
	}
	if code := schema.AsToolError(err).Code; code != schema.CodeInvalidArgument {
		t.Fatalf("%s: error code = %s, want %s", what, code, schema.CodeInvalidArgument)
	}
}

func TestValidatePlanRequest(t *testing.T) {
	if err := validatePlanRequest(schema.PlanRequest{Instructions: "do it"}); err != nil {
		t.Fatalf("valid plan request rejected: %v", err)
	}
	wantInvalid(t, validatePlanRequest(schema.PlanRequest{Instructions: "  "}), "empty instructions")
	wantInvalid(t, validatePlanRequest(schema.PlanRequest{Instructions: "x", Profile: "turbo"}), "bad profile")
	wantInvalid(t, validatePlanRequest(schema.PlanRequest{Instructions: "x", Budget: schema.Budget{MaxModelCalls: -1}}), "negative budget")
	wantInvalid(t, validatePlanRequest(schema.PlanRequest{Instructions: "x", Output: schema.OutputOpts{Format: "xml"}}), "bad format")
}

// TestValidateRejectsInertFields proves the truthful-schema guarantee: request
// fields the runtime does not honor are rejected rather than silently ignored.
func TestValidateRejectsInertFields(t *testing.T) {
	base := schema.PlanRequest{Instructions: "x"}
	cases := map[string]schema.RetrievalOpts{
		"include":           {Include: []string{"*.go"}},
		"exclude":           {Exclude: []string{"vendor/**"}},
		"max_files":         {MaxFiles: 10},
		"enable_embeddings": {EnableEmbeddings: boolPtr(true)},
	}
	for name, ret := range cases {
		req := base
		req.Retrieval = ret
		wantInvalid(t, validatePlanRequest(req), "retrieval."+name)
	}
	// Honored retrieval overrides pass.
	req := base
	req.Retrieval = schema.RetrievalOpts{MaxFileReads: 5, MaxContextTokens: 1000}
	if err := validatePlanRequest(req); err != nil {
		t.Fatalf("honored retrieval overrides rejected: %v", err)
	}
}

func TestValidateHelpRequest(t *testing.T) {
	if err := validateHelpRequest(schema.HelpRequest{Question: "why?"}); err != nil {
		t.Fatalf("valid help request rejected: %v", err)
	}
	wantInvalid(t, validateHelpRequest(schema.HelpRequest{Question: ""}), "empty question")
	wantInvalid(t, validateHelpRequest(schema.HelpRequest{Question: "q", AnswerType: "guess"}), "bad answer type")
	for _, at := range []schema.HelpAnswerType{schema.AnswerAuto, schema.AnswerExplain, schema.AnswerDiagnose, schema.AnswerDecide, schema.AnswerUnblock} {
		if err := validateHelpRequest(schema.HelpRequest{Question: "q", AnswerType: at}); err != nil {
			t.Errorf("answer type %q rejected: %v", at, err)
		}
	}
}

func TestValidateReviewRequest(t *testing.T) {
	if err := validateReviewRequest(schema.ReviewRequest{}); err != nil {
		t.Fatalf("default review request rejected: %v", err)
	}
	wantInvalid(t, validateReviewRequest(schema.ReviewRequest{Verification: "paranoid"}), "bad verification")
	wantInvalid(t, validateReviewRequest(schema.ReviewRequest{Policy: schema.ReviewPolicy{IncludePraise: true}}), "unsupported include_praise")
	wantInvalid(t, validateReviewRequest(schema.ReviewRequest{Policy: schema.ReviewPolicy{MinimumEvidence: "guess"}}), "bad minimum_evidence")
	// Conflicting target params: a commit set on a non-commit target.
	wantInvalid(t, validateReviewRequest(schema.ReviewRequest{
		Target: schema.ReviewTarget{Type: schema.TargetWorkingTree, Commit: "abc123"},
	}), "commit on working_tree")
	// base_ref on working tree is meaningless.
	wantInvalid(t, validateReviewRequest(schema.ReviewRequest{
		Target: schema.ReviewTarget{Type: schema.TargetWorkingTree, BaseRef: "main"},
	}), "base_ref on working_tree")
	// A coherent range target passes.
	if err := validateReviewRequest(schema.ReviewRequest{
		Target: schema.ReviewTarget{Type: schema.TargetRange, BaseRef: "main", HeadRef: "feature"},
	}); err != nil {
		t.Fatalf("coherent range target rejected: %v", err)
	}
	// commit type without a commit fails.
	wantInvalid(t, validateReviewRequest(schema.ReviewRequest{
		Target: schema.ReviewTarget{Type: schema.TargetCommit},
	}), "commit type without commit")
}

func boolPtr(b bool) *bool { return &b }
