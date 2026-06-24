package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/evidence"
	"github.com/gregpriday/codeexpert/internal/schema"
)

func TestValidateHelpReport(t *testing.T) {
	// A diagnosis with no ranked hypotheses is invalid.
	d := &schema.HelpReport{DirectAnswer: "it races"}
	if issues := validateHelpReport(d, schema.AnswerDiagnose); len(issues) == 0 {
		t.Error("a diagnosis with no likely_causes should be flagged")
	}
	d.LikelyCauses = []schema.CauseHypothesis{{Hypothesis: "race"}}
	if issues := validateHelpReport(d, schema.AnswerDiagnose); len(issues) != 0 {
		t.Errorf("a diagnosis with a hypothesis should pass, got %v", issues)
	}
	// An explain answer needs no hypotheses, but still needs a direct answer.
	e := &schema.HelpReport{DirectAnswer: ""}
	if issues := validateHelpReport(e, schema.AnswerExplain); len(issues) == 0 {
		t.Error("an empty direct_answer should be flagged for any answer type")
	}
	e.DirectAnswer = "it works like so"
	if issues := validateHelpReport(e, schema.AnswerExplain); len(issues) != 0 {
		t.Errorf("an explain answer needs no hypotheses, got %v", issues)
	}
}

func TestPruneHelpEvidenceDowngradesUnverified(t *testing.T) {
	store := evidence.NewStore("snap")
	store.Add(schema.EvidenceRecord{ID: "E-real", Kind: schema.EvidenceKindFile, Path: "a.go", Summary: "x"})
	h := &schema.HelpReport{
		DirectAnswer: "x",
		LikelyCauses: []schema.CauseHypothesis{
			{Hypothesis: "real", Verified: true, EvidenceIDs: []string{"E-real", "E-fake"}},
			{Hypothesis: "unbacked", Verified: true, EvidenceIDs: []string{"E-fake"}},
		},
	}
	pruneHelpEvidence(h, store)
	if len(h.LikelyCauses[0].EvidenceIDs) != 1 || h.LikelyCauses[0].EvidenceIDs[0] != "E-real" {
		t.Errorf("fabricated evidence id should be dropped, got %v", h.LikelyCauses[0].EvidenceIDs)
	}
	if !h.LikelyCauses[0].Verified {
		t.Error("a cause with surviving evidence should stay verified")
	}
	if h.LikelyCauses[1].Verified {
		t.Error("a cause whose only evidence was fabricated must be downgraded to unverified")
	}
}

func TestHelpAnswerNeedsRepo(t *testing.T) {
	if !helpAnswerNeedsRepo(schema.AnswerDiagnose) || !helpAnswerNeedsRepo(schema.AnswerUnblock) {
		t.Error("diagnose/unblock should require a repository")
	}
	if helpAnswerNeedsRepo(schema.AnswerExplain) || helpAnswerNeedsRepo(schema.AnswerDecide) || helpAnswerNeedsRepo(schema.AnswerAuto) {
		t.Error("explain/decide/auto should be answerable without a repository")
	}
}

// TestHelpEndToEndDirectAnswer proves Help produces a direct-answer-first report
// and renders it leading with the answer.
func TestHelpEndToEndDirectAnswer(t *testing.T) {
	dir, rel := tempGitRepo(t)
	eng := newTestEngine(rel)
	res, err := eng.Help(context.Background(), schema.HelpRequest{
		Root: dir, Question: "Why does it crash?", AnswerType: schema.AnswerDiagnose,
	}, RunOptions{})
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	if res.Kind != schema.PlanModeHelp || res.Help == nil {
		t.Fatal("expected a help report")
	}
	if res.Help.DirectAnswer == "" {
		t.Error("help report must lead with a direct answer")
	}
	if i := strings.Index(res.Markdown, "## Answer"); i < 0 {
		t.Error("markdown should lead with an Answer section")
	}
}

// TestValidateHelpReportAutoDiagnose proves an auto request that returns a
// diagnosis with no hypotheses is still caught (validated against the returned
// answer type, not just the requested one).
func TestValidateHelpReportAutoDiagnose(t *testing.T) {
	h := &schema.HelpReport{DirectAnswer: "it races", AnswerType: schema.AnswerDiagnose}
	if issues := validateHelpReport(h, schema.AnswerAuto); len(issues) == 0 {
		t.Error("an auto run that returns a diagnosis with no hypotheses should be flagged")
	}
	h.LikelyCauses = []schema.CauseHypothesis{{Hypothesis: "race"}}
	if issues := validateHelpReport(h, schema.AnswerAuto); len(issues) != 0 {
		t.Errorf("auto diagnosis with a hypothesis should pass, got %v", issues)
	}
}
