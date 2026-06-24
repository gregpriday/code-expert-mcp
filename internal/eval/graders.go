package eval

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gregpriday/codeexpert/internal/schema"
)

func decodeCase(b []byte) (Case, error) {
	var c Case
	err := json.Unmarshal(b, &c)
	return c, err
}

func pass(name string, detail string) GradeResult {
	return GradeResult{Grader: name, Pass: true, Score: 1, Detail: detail}
}
func fail(name string, detail string) GradeResult {
	return GradeResult{Grader: name, Pass: false, Score: 0, Detail: detail}
}

// expectsError reports whether the case expects the run to error, and the code.
func expectsError(c Case) (string, bool) {
	return c.Expect.MustErrorCode, c.Expect.MustErrorCode != ""
}

// schemaGrader checks the result exists (or the expected error occurred) and that
// the structured output's required fields are present.
type schemaGrader struct{}

func (schemaGrader) Name() string { return "schema" }
func (schemaGrader) Grade(_ context.Context, out Outcome) GradeResult {
	const n = "schema"
	if code, want := expectsError(out.Case); want {
		if out.Err == nil {
			return fail(n, "expected an error with code "+code+", got a result")
		}
		if got := schema.AsToolError(out.Err).Code; got != code {
			return fail(n, "error code "+got+", want "+code)
		}
		return pass(n, "expected error "+code)
	}
	if out.Err != nil {
		return fail(n, "unexpected error: "+out.Err.Error())
	}
	switch out.Case.Capability {
	case CapPlan:
		if out.Plan == nil || out.Plan.Plan == nil {
			return fail(n, "no plan in result")
		}
		if strings.TrimSpace(out.Plan.Plan.Goal) == "" {
			return fail(n, "plan goal is empty")
		}
	case CapHelp:
		if out.Plan == nil || out.Plan.Help == nil {
			return fail(n, "no help report in result")
		}
	case CapReview:
		if out.Review == nil {
			return fail(n, "no review result")
		}
	}
	return pass(n, "structured output present")
}

// noWriteGrader enforces the read-only invariant: the repository tree must be
// byte-identical before and after the run.
type noWriteGrader struct{}

func (noWriteGrader) Name() string { return "no_write" }
func (noWriteGrader) Grade(_ context.Context, out Outcome) GradeResult {
	const n = "no_write"
	if out.RepoBefore == "" { // no repo root to fingerprint
		return pass(n, "no repository to check")
	}
	if out.RepoBefore != out.RepoAfter {
		return fail(n, "repository tree changed during the run")
	}
	return pass(n, "repository unchanged")
}

// statusGrader checks the terminal status against the expectation.
type statusGrader struct{}

func (statusGrader) Name() string { return "status" }
func (statusGrader) Grade(_ context.Context, out Outcome) GradeResult {
	const n = "status"
	if _, want := expectsError(out.Case); want {
		return pass(n, "error case; status not applicable")
	}
	if out.Err != nil {
		return fail(n, "run errored")
	}
	status := outcomeStatus(out)
	exp := out.Case.Expect
	if exp.MustComplete && status != schema.StatusComplete {
		return fail(n, "status "+string(status)+", want complete")
	}
	if !exp.AllowPartial && status == schema.StatusPartial && !exp.MustComplete {
		// A partial result is only acceptable when the case opts into it.
		return fail(n, "unexpected partial status (set allow_partial to permit)")
	}
	return pass(n, string(status))
}

// evidenceGrader checks that every cited evidence ID a finding publishes resolves
// to a real evidence record returned by the run (no fabricated citations escape).
type evidenceGrader struct{}

func (evidenceGrader) Name() string { return "evidence_resolves" }
func (evidenceGrader) Grade(_ context.Context, out Outcome) GradeResult {
	const n = "evidence_resolves"
	if out.Err != nil {
		return pass(n, "error case; not applicable")
	}
	if out.Review == nil {
		return pass(n, "not a review")
	}
	for _, f := range out.Review.Findings {
		for _, ev := range f.Evidence {
			if strings.TrimSpace(ev.ID) == "" && ev.Path == "" {
				return fail(n, "finding "+f.ID+" has an empty evidence reference")
			}
		}
	}
	return pass(n, "all published evidence references are concrete")
}

// planGrader checks plan-specific expectations: minimum steps and full
// acceptance-criterion traceability.
type planGrader struct{}

func (planGrader) Name() string { return "plan" }
func (planGrader) Grade(_ context.Context, out Outcome) GradeResult {
	const n = "plan"
	if out.Case.Capability != CapPlan || out.Err != nil || out.Plan == nil || out.Plan.Plan == nil {
		return pass(n, "not an applicable plan case")
	}
	p := out.Plan.Plan
	exp := out.Case.Expect
	if exp.MinSteps > 0 && len(p.Steps) < exp.MinSteps {
		return fail(n, "plan has fewer steps than expected")
	}
	if exp.RequireTraceability {
		want := acceptanceCriteria(out.Case)
		covered := map[string]bool{}
		for _, cc := range p.Traceability {
			if len(cc.StepIDs) > 0 {
				covered[normalize(cc.Criterion)] = true
			}
		}
		for _, c := range want {
			if !covered[normalize(c)] {
				return fail(n, "acceptance criterion not traced to a step: "+c)
			}
		}
	}
	return pass(n, "plan expectations met")
}

// helpGrader checks that a help answer leads with a direct answer.
type helpGrader struct{}

func (helpGrader) Name() string { return "help" }
func (helpGrader) Grade(_ context.Context, out Outcome) GradeResult {
	const n = "help"
	if out.Case.Capability != CapHelp || out.Err != nil || out.Plan == nil || out.Plan.Help == nil {
		return pass(n, "not an applicable help case")
	}
	if out.Case.Expect.RequireDirectAnswer && strings.TrimSpace(out.Plan.Help.DirectAnswer) == "" {
		return fail(n, "help report has no direct answer")
	}
	return pass(n, "help expectations met")
}

// reviewGrader checks review-specific expectations: finding counts, that findings
// land on expected files, and that the review never claims approval.
type reviewGrader struct{}

func (reviewGrader) Name() string { return "review" }
func (reviewGrader) Grade(_ context.Context, out Outcome) GradeResult {
	const n = "review"
	if out.Case.Capability != CapReview || out.Err != nil || out.Review == nil {
		return pass(n, "not an applicable review case")
	}
	r := out.Review
	exp := out.Case.Expect
	if len(r.Findings) < exp.MinFindings {
		return fail(n, "fewer findings than expected")
	}
	if exp.MaxFindings > 0 && len(r.Findings) > exp.MaxFindings {
		return fail(n, "more findings than expected")
	}
	if len(exp.ExpectFindingPaths) > 0 {
		got := map[string]bool{}
		for _, f := range r.Findings {
			got[f.Location.Path] = true
		}
		for _, p := range exp.ExpectFindingPaths {
			if !got[p] {
				return fail(n, "expected a finding on "+p)
			}
		}
	}
	if exp.ForbidApprovalLang {
		low := strings.ToLower(r.Markdown + " " + r.Summary.Conclusion + " " + r.Summary.Headline)
		if strings.Contains(low, "safe to merge") || strings.Contains(low, "approved") {
			return fail(n, "review leaked approval language")
		}
	}
	return pass(n, "review expectations met")
}

func outcomeStatus(out Outcome) schema.RunStatus {
	if out.Plan != nil {
		return out.Plan.Status
	}
	if out.Review != nil {
		return out.Review.Status
	}
	return ""
}

func acceptanceCriteria(c Case) []string {
	switch {
	case c.Plan != nil && c.Plan.Task != nil:
		return c.Plan.Task.AcceptanceCriteria
	case c.Help != nil && c.Help.Task != nil:
		return c.Help.Task.AcceptanceCriteria
	}
	return nil
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
