package report

import (
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// bannedPhrases are merge-approval claims the review renderer must never emit.
// The precision-first invariant: a review reports findings and leaves the merge
// decision to the reader.
var bannedPhrases = []string{"safe to merge", "approved", "lgtm", "looks good to me"}

func assertNoApproval(t *testing.T, md string) {
	t.Helper()
	lower := strings.ToLower(md)
	for _, p := range bannedPhrases {
		if strings.Contains(lower, p) {
			t.Errorf("rendered review contains banned approval phrase %q:\n%s", p, md)
		}
	}
}

func TestReviewMarkdownNoFindings(t *testing.T) {
	res := schema.ReviewResult{
		RunID:    "review-1",
		Status:   schema.StatusComplete,
		Snapshot: schema.ReviewSnapshot{SnapshotID: "snap-abc", Root: "/tmp/repo", Target: "working_tree"},
		Summary: schema.ReviewSummary{
			Headline:      "No blocking findings were found within the automated review scope.",
			TotalFindings: 0,
		},
	}
	md := ReviewMarkdown(res)

	if !strings.Contains(md, "# Code review") {
		t.Errorf("missing review header:\n%s", md)
	}
	if !strings.Contains(md, "No blocking findings were found within the automated review scope.") {
		t.Errorf("zero-findings message missing:\n%s", md)
	}
	if !strings.Contains(md, "## Limitations") {
		t.Errorf("Limitations section must always render:\n%s", md)
	}
	if !strings.Contains(md, "None reported.") {
		t.Errorf("empty limitations should render 'None reported.':\n%s", md)
	}
	assertNoApproval(t, md)
}

func TestReviewMarkdownBlockingAndNonBlocking(t *testing.T) {
	res := schema.ReviewResult{
		RunID:  "review-2",
		Status: schema.StatusComplete,
		Snapshot: schema.ReviewSnapshot{
			SnapshotID: "snap-xyz", Root: "/tmp/repo", Target: "working_tree",
			BaseLabel: "main", HeadLabel: "feature",
		},
		Summary: schema.ReviewSummary{
			Headline:        "2 findings (1 blocking)",
			TotalFindings:   2,
			BlockingCount:   1,
			FilesReviewed:   3,
			FilesChanged:    5,
			HighestSeverity: schema.SeverityHigh,
			Conclusion:      "Review surfaced 2 findings (1 blocking). This is an automated, non-exhaustive review, not a merge approval.",
		},
		Findings: []schema.ReviewFinding{
			{
				ID: "F1", Title: "Possible nil dereference",
				Category: schema.CategoryCorrectness, Severity: schema.SeverityHigh,
				Blocking: true, EvidenceLevel: schema.EvidenceCodePath,
				Location: schema.SourceLocation{Path: "main.go", StartLine: 10, EndLine: 12},
				Claim:    "x may be nil", Trigger: "when input empty", Impact: "panic",
				Evidence: []schema.EvidenceRef{
					{ID: "E-1", Kind: schema.EvidenceKindFile, Path: "main.go", StartLine: 10, EndLine: 12, Summary: "deref site"},
				},
				Assumptions:    []string{"caller does not pre-check"},
				Recommendation: "check nil before deref",
			},
			{
				ID: "F2", Title: "Style nit",
				Category: schema.CategoryStyle, Severity: schema.SeverityLow,
				Blocking: false, EvidenceLevel: schema.EvidenceSpeculative,
				Location: schema.SourceLocation{Path: "util.go", StartLine: 4},
				Claim:    "naming could be clearer",
			},
		},
		RiskMap: []schema.RiskArea{
			{Category: schema.CategoryCorrectness, Rationale: "baseline correctness", Paths: []string{"main.go"}, Priority: 3},
		},
		Coverage: schema.ReviewCoverage{
			ReviewedFiles:       []string{"main.go", "util.go"},
			ChangedLineEstimate: 42,
			SpecialistPasses:    []string{"correctness"},
			SkippedFiles:        []schema.SkippedFile{{Path: "gen.go", Reason: "generated"}},
		},
		Checks: []schema.CheckResult{
			{CheckID: "build", Name: "go build", ExitCode: 0, Passed: true, Summary: "ok"},
			{CheckID: "vet", ExitCode: 1, Passed: false, Summary: "1 issue"},
		},
		Limitations: []schema.Limitation{
			{Stage: "budget", Message: "time budget reached"},
			{Message: "no stage"},
		},
	}
	md := ReviewMarkdown(res)

	for _, want := range []string{
		"## Summary",
		"2 findings (1 blocking)",
		"- Total findings: 2 (blocking: 1)",
		"- Files reviewed: 3 of 5 changed",
		"- Highest severity: high",
		"### [high] [C] Possible nil dereference",
		"## Non-blocking findings",
		"### [low] [D] Style nit",
		"## Risk map",
		"## Coverage",
		"## Checks",
		"**go build** — passed (exit 0)",
		"**vet** — failed (exit 1)", // falls back to CheckID when Name is empty
		"## Limitations",
		"- **budget:** time budget reached",
		"- no stage",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered review missing %q:\n%s", want, md)
		}
	}

	// The blocking finding must appear before the non-blocking section header.
	blockingIdx := strings.Index(md, "Possible nil dereference")
	nonBlockingHdr := strings.Index(md, "## Non-blocking findings")
	if blockingIdx < 0 || nonBlockingHdr < 0 || blockingIdx > nonBlockingHdr {
		t.Errorf("blocking finding should render before the non-blocking section header")
	}
	assertNoApproval(t, md)
}

// TestReviewMarkdownNonBlockingOnly covers the branch where there are findings
// but none are blocking: a distinct notice must render.
func TestReviewMarkdownNonBlockingOnly(t *testing.T) {
	res := schema.ReviewResult{
		Status: schema.StatusComplete,
		Findings: []schema.ReviewFinding{
			{
				ID: "F1", Title: "Minor", Category: schema.CategoryStyle,
				Severity: schema.SeverityInfo, Blocking: false, EvidenceLevel: schema.EvidenceCodePath,
				Location: schema.SourceLocation{Path: "a.go"},
			},
		},
	}
	md := ReviewMarkdown(res)
	if !strings.Contains(md, "No blocking findings within the automated review scope. See non-blocking findings below.") {
		t.Errorf("expected non-blocking-only notice:\n%s", md)
	}
	assertNoApproval(t, md)
}

func TestPlanMarkdownEmptyPlan(t *testing.T) {
	// res.Plan == nil and res.Help == nil: header, snapshot, limitations only.
	res := schema.PlanResult{
		RunID:    "plan-1",
		Kind:     schema.PlanModePlan,
		Status:   schema.StatusComplete,
		Snapshot: schema.SnapshotRef{SnapshotID: "snap-1", Root: "/tmp/repo", FileCount: 7},
		Request:  schema.InterpretedTask{Goal: "Add a flag"},
	}
	md := PlanMarkdown(res)

	if !strings.Contains(md, "# Implementation plan") {
		t.Errorf("missing plan header:\n%s", md)
	}
	if !strings.Contains(md, "**Goal:** Add a flag") {
		t.Errorf("goal not rendered:\n%s", md)
	}
	if !strings.Contains(md, "## Snapshot") || !strings.Contains(md, "- Files: 7") {
		t.Errorf("snapshot info missing:\n%s", md)
	}
	if strings.Contains(md, "## Goal\n") || strings.Contains(md, "## Steps") {
		t.Errorf("nil plan must not render plan body sections:\n%s", md)
	}
	if !strings.Contains(md, "## Limitations") || !strings.Contains(md, "None reported.") {
		t.Errorf("limitations section missing:\n%s", md)
	}
}

func TestPlanMarkdownFullPlan(t *testing.T) {
	res := schema.PlanResult{
		RunID:    "plan-2",
		Kind:     schema.PlanModePlan,
		Status:   schema.StatusComplete,
		Snapshot: schema.SnapshotRef{SnapshotID: "snap-2", Root: "/tmp/repo", IsGit: true, Branch: "main", HeadSHA: "abcdef1234567890", Dirty: true, FileCount: 3},
		Request:  schema.InterpretedTask{Goal: "Implement feature"},
		Plan: &schema.ImplementationPlan{
			Goal:                "Implement feature",
			RecommendedApproach: "Refactor then add",
			Constraints:         []string{"keep CGO-free"},
			Steps: []schema.PlanStep{
				{
					ID: "P1", Title: "First step", Objective: "do the thing",
					Files:           []schema.FileTarget{{Path: "main.go", Action: "modify", Note: "add flag"}},
					Symbols:         []schema.SymbolTarget{{Name: "main", Path: "main.go"}},
					DetailedChanges: []string{"add --greet flag"},
					Validation:      []schema.ValidationStep{{Description: "run tests", Command: "go test ./..."}},
				},
			},
			TestPlan:         []schema.ValidationStep{{Description: "go test"}},
			DefinitionOfDone: []string{"tests pass"},
		},
		Limitations: []schema.Limitation{{Stage: "snapshot", Message: "truncated"}},
	}
	md := PlanMarkdown(res)

	for _, want := range []string{
		"# Implementation plan",
		"## Snapshot",
		"- Git: main @ abcdef123456 (dirty)",
		"## Goal",
		"## Recommended approach",
		"## Constraints",
		"## Steps",
		"### P1 — First step",
		"- Files:",
		"`main.go` (modify) — add flag",
		"## Test plan",
		"## Definition of done",
		"## Limitations",
		"- **snapshot:** truncated",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered plan missing %q:\n%s", want, md)
		}
	}
}

func TestPlanMarkdownHelpReport(t *testing.T) {
	res := schema.PlanResult{
		RunID:    "help-1",
		Kind:     schema.PlanModeHelp,
		Status:   schema.StatusComplete,
		Snapshot: schema.SnapshotRef{SnapshotID: "snap-3", Root: "/tmp/repo", FileCount: 9},
		Request:  schema.InterpretedTask{Goal: "Why does it crash?"},
		Help: &schema.HelpReport{
			ProblemRestatement:   "It panics on startup",
			RecommendedDirection: "guard the nil case",
			LikelyCauses: []schema.CauseHypothesis{
				{Hypothesis: "nil config", Likelihood: schema.ConfidenceHigh, Verified: false, Reasoning: "no default set"},
			},
			Confidence: schema.ConfidenceMedium,
		},
	}
	md := PlanMarkdown(res)

	for _, want := range []string{
		"# Engineering help",
		"**Request:** Why does it crash?",
		"## Problem restatement",
		"## Likely causes",
		"- **nil config** [inference, likelihood: high]",
		"## Recommended direction",
		"## Confidence",
		"## Limitations",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered help missing %q:\n%s", want, md)
		}
	}
	// Help mode must not render the plan header.
	if strings.Contains(md, "# Implementation plan") {
		t.Errorf("help report must not render the implementation-plan header:\n%s", md)
	}
}
