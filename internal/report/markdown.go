// Package report renders CodeExpert results (plans, help reports, and code
// reviews) into human-readable Markdown and pretty-printed JSON. It is a pure
// formatting layer: it reads schema types and produces strings, with no side
// effects and no dependencies beyond the schema package and the standard
// library.
package report

import (
	"fmt"
	"strings"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// PlanMarkdown renders a PlanResult as Markdown. It produces an implementation
// plan when res.Plan is set, an engineering help report when res.Help is set,
// and always emits a header, snapshot info, and a Limitations section.
func PlanMarkdown(res schema.PlanResult) string {
	var b strings.Builder

	if res.Help != nil {
		writeHelpHeader(&b, res)
		writeHelpBody(&b, *res.Help)
	} else {
		writePlanHeader(&b, res)
		if res.Plan != nil {
			writePlanBody(&b, *res.Plan)
		}
	}

	writeLimitations(&b, res.Limitations)
	return b.String()
}

// ReviewMarkdown renders a ReviewResult as Markdown. It never asserts that a
// change is safe to merge or approved; it reports findings, risk, coverage, and
// limitations and leaves the merge decision to the reader.
func ReviewMarkdown(res schema.ReviewResult) string {
	var b strings.Builder

	b.WriteString("# Code review\n\n")
	writeReviewScope(&b, res.Snapshot, res.Status)

	if res.Summary.Headline != "" || res.Summary.Conclusion != "" || res.Summary.TotalFindings > 0 {
		b.WriteString("## Summary\n\n")
		if res.Summary.Headline != "" {
			b.WriteString(res.Summary.Headline)
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "- Total findings: %d (blocking: %d)\n", res.Summary.TotalFindings, res.Summary.BlockingCount)
		fmt.Fprintf(&b, "- Files reviewed: %d", res.Summary.FilesReviewed)
		if res.Summary.FilesChanged > 0 {
			fmt.Fprintf(&b, " of %d changed", res.Summary.FilesChanged)
		}
		b.WriteString("\n")
		if res.Summary.HighestSeverity != "" {
			fmt.Fprintf(&b, "- Highest severity: %s\n", res.Summary.HighestSeverity)
		}
		if res.Summary.Conclusion != "" {
			fmt.Fprintf(&b, "\n%s\n", res.Summary.Conclusion)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Findings\n\n")
	if len(res.Findings) == 0 {
		b.WriteString("No blocking findings were found within the automated review scope.\n\n")
	} else {
		var blocking, nonBlocking []schema.ReviewFinding
		for _, f := range res.Findings {
			if f.Blocking {
				blocking = append(blocking, f)
			} else {
				nonBlocking = append(nonBlocking, f)
			}
		}
		if len(blocking) == 0 {
			b.WriteString("No blocking findings within the automated review scope. See non-blocking findings below.\n\n")
		}
		for _, f := range blocking {
			writeReviewFinding(&b, f)
		}
		if len(nonBlocking) > 0 {
			b.WriteString("## Non-blocking findings\n\n")
			for _, f := range nonBlocking {
				writeReviewFinding(&b, f)
			}
		}
	}

	if len(res.RiskMap) > 0 {
		b.WriteString("## Risk map\n\n")
		for _, r := range res.RiskMap {
			fmt.Fprintf(&b, "- **%s** (priority %d): %s\n", r.Category, r.Priority, r.Rationale)
			if len(r.Paths) > 0 {
				fmt.Fprintf(&b, "  - Paths: %s\n", joinCode(r.Paths))
			}
		}
		b.WriteString("\n")
	}

	writeReviewCoverage(&b, res.Coverage)

	if len(res.Checks) > 0 {
		b.WriteString("## Checks\n\n")
		for _, c := range res.Checks {
			status := "failed"
			if c.Passed {
				status = "passed"
			}
			name := c.Name
			if name == "" {
				name = c.CheckID
			}
			fmt.Fprintf(&b, "- **%s** — %s (exit %d)", name, status, c.ExitCode)
			if c.Summary != "" {
				fmt.Fprintf(&b, ": %s", c.Summary)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	writeLimitations(&b, res.Limitations)
	return b.String()
}

// writeReviewScope renders the snapshot/scope line for a review.
func writeReviewScope(b *strings.Builder, snap schema.ReviewSnapshot, status schema.RunStatus) {
	b.WriteString("## Scope\n\n")
	if snap.Target != "" {
		fmt.Fprintf(b, "- Target: %s\n", snap.Target)
	}
	base := firstNonEmpty(snap.BaseLabel, shortSHA(snap.BaseSHA))
	head := firstNonEmpty(snap.HeadLabel, shortSHA(snap.HeadSHA))
	if base != "" || head != "" {
		fmt.Fprintf(b, "- Range: %s → %s\n", base, head)
	}
	if snap.Root != "" {
		fmt.Fprintf(b, "- Root: `%s`\n", snap.Root)
	}
	if snap.SnapshotID != "" {
		fmt.Fprintf(b, "- Snapshot: `%s`\n", snap.SnapshotID)
	}
	if status != "" {
		fmt.Fprintf(b, "- Status: %s\n", status)
	}
	b.WriteString("\n")
}

// writeReviewFinding renders one review finding in detail.
func writeReviewFinding(b *strings.Builder, f schema.ReviewFinding) {
	fmt.Fprintf(b, "### [%s] [%s] %s\n\n", f.Severity, f.EvidenceLevel, f.Title)

	meta := fmt.Sprintf("Category: %s", f.Category)
	if f.Blocking {
		meta += " · blocking"
	}
	if loc := formatLocation(f.Location.Path, f.Location.StartLine, f.Location.EndLine); loc != "" {
		meta += " · " + loc
	}
	fmt.Fprintf(b, "_%s_\n\n", meta)

	if f.Claim != "" {
		fmt.Fprintf(b, "%s\n\n", f.Claim)
	}
	if f.Trigger != "" {
		fmt.Fprintf(b, "**Trigger:** %s\n\n", f.Trigger)
	}
	if f.Impact != "" {
		fmt.Fprintf(b, "**Impact:** %s\n\n", f.Impact)
	}

	if len(f.Evidence) > 0 {
		b.WriteString("**Evidence:**\n\n")
		for _, e := range f.Evidence {
			ref := formatLocation(e.Path, e.StartLine, e.EndLine)
			if ref == "" {
				ref = "`" + e.ID + "`"
			}
			fmt.Fprintf(b, "- %s", ref)
			if e.Summary != "" {
				fmt.Fprintf(b, " — %s", e.Summary)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(f.Assumptions) > 0 {
		b.WriteString("**Assumptions:**\n\n")
		writeBullets(b, f.Assumptions)
		b.WriteString("\n")
	}

	if f.Recommendation != "" {
		fmt.Fprintf(b, "Recommended direction: %s\n\n", f.Recommendation)
	}
}

// writeReviewCoverage renders what was and was not reviewed.
func writeReviewCoverage(b *strings.Builder, cov schema.ReviewCoverage) {
	if len(cov.ReviewedFiles) == 0 && len(cov.SkippedFiles) == 0 &&
		cov.ChangedLineEstimate == 0 && len(cov.SpecialistPasses) == 0 &&
		len(cov.ChecksRun) == 0 && len(cov.Unindexed) == 0 && !cov.BudgetLimited {
		return
	}

	b.WriteString("## Coverage\n\n")
	if cov.ChangedLineEstimate > 0 {
		fmt.Fprintf(b, "- Changed lines (est.): %d\n", cov.ChangedLineEstimate)
	}
	if len(cov.ReviewedFiles) > 0 {
		fmt.Fprintf(b, "- Reviewed files: %d\n", len(cov.ReviewedFiles))
	}
	if len(cov.SpecialistPasses) > 0 {
		fmt.Fprintf(b, "- Specialist passes: %s\n", strings.Join(cov.SpecialistPasses, ", "))
	}
	if len(cov.ChecksRun) > 0 {
		fmt.Fprintf(b, "- Checks run: %s\n", strings.Join(cov.ChecksRun, ", "))
	}
	if cov.BudgetLimited {
		b.WriteString("- Budget limited: review did not cover the full change set\n")
	}
	if len(cov.SkippedFiles) > 0 {
		b.WriteString("- Skipped files:\n")
		for _, sf := range cov.SkippedFiles {
			fmt.Fprintf(b, "  - `%s` — %s\n", sf.Path, sf.Reason)
		}
	}
	if len(cov.Unindexed) > 0 {
		fmt.Fprintf(b, "- Unindexed: %s\n", joinCode(cov.Unindexed))
	}
	b.WriteString("\n")
}

// formatLocation renders a path with an optional line range as a code span,
// e.g. `path:start-end`. Returns "" when there is no path.
func formatLocation(path string, start, end int) string {
	if path == "" {
		return ""
	}
	switch {
	case start > 0 && end > 0 && end != start:
		return fmt.Sprintf("`%s:%d-%d`", path, start, end)
	case start > 0:
		return fmt.Sprintf("`%s:%d`", path, start)
	default:
		return fmt.Sprintf("`%s`", path)
	}
}

// firstNonEmpty returns the first non-empty string of its arguments.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// writePlanHeader renders the top of an implementation plan document.
func writePlanHeader(b *strings.Builder, res schema.PlanResult) {
	b.WriteString("# Implementation plan\n\n")
	if res.Request.Goal != "" {
		fmt.Fprintf(b, "**Goal:** %s\n\n", res.Request.Goal)
	}
	writeSnapshotInfo(b, res.Snapshot, res.Status)
}

// writeHelpHeader renders the top of an engineering help document.
func writeHelpHeader(b *strings.Builder, res schema.PlanResult) {
	b.WriteString("# Engineering help\n\n")
	if res.Request.Goal != "" {
		fmt.Fprintf(b, "**Request:** %s\n\n", res.Request.Goal)
	}
	writeSnapshotInfo(b, res.Snapshot, res.Status)
}

// writeSnapshotInfo renders the immutable snapshot the run was frozen against.
func writeSnapshotInfo(b *strings.Builder, snap schema.SnapshotRef, status schema.RunStatus) {
	b.WriteString("## Snapshot\n\n")
	if snap.SnapshotID != "" {
		fmt.Fprintf(b, "- Snapshot: `%s`\n", snap.SnapshotID)
	}
	if snap.Root != "" {
		fmt.Fprintf(b, "- Root: `%s`\n", snap.Root)
	}
	if snap.IsGit {
		ref := snap.Branch
		if snap.HeadSHA != "" {
			if ref != "" {
				ref += " @ " + shortSHA(snap.HeadSHA)
			} else {
				ref = shortSHA(snap.HeadSHA)
			}
		}
		if ref != "" {
			fmt.Fprintf(b, "- Git: %s", ref)
			if snap.Dirty {
				b.WriteString(" (dirty)")
			}
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(b, "- Files: %d\n", snap.FileCount)
	if status != "" {
		fmt.Fprintf(b, "- Status: %s\n", status)
	}
	b.WriteString("\n")
}

// writePlanBody renders the body sections of an implementation plan.
func writePlanBody(b *strings.Builder, p schema.ImplementationPlan) {
	if p.Goal != "" {
		b.WriteString("## Goal\n\n")
		b.WriteString(p.Goal)
		b.WriteString("\n\n")
	}

	if len(p.CurrentBehavior) > 0 {
		b.WriteString("## Current behavior\n\n")
		for _, s := range p.CurrentBehavior {
			writeEvidenceStatement(b, s)
		}
		b.WriteString("\n")
	}

	if len(p.Constraints) > 0 {
		b.WriteString("## Constraints\n\n")
		writeBullets(b, p.Constraints)
		b.WriteString("\n")
	}

	if len(p.Assumptions) > 0 {
		b.WriteString("## Assumptions\n\n")
		for _, a := range p.Assumptions {
			fmt.Fprintf(b, "- %s", a.Statement)
			if a.Rationale != "" {
				fmt.Fprintf(b, " — %s", a.Rationale)
			}
			b.WriteString("\n")
			if a.Investigate != "" {
				fmt.Fprintf(b, "  - Investigate: %s\n", a.Investigate)
			}
		}
		b.WriteString("\n")
	}

	if len(p.ImpactedAreas) > 0 {
		b.WriteString("## Impacted areas\n\n")
		for _, area := range p.ImpactedAreas {
			fmt.Fprintf(b, "- **%s**", area.Area)
			if area.Description != "" {
				fmt.Fprintf(b, " — %s", area.Description)
			}
			b.WriteString("\n")
			if len(area.Paths) > 0 {
				fmt.Fprintf(b, "  - Paths: %s\n", joinCode(area.Paths))
			}
		}
		b.WriteString("\n")
	}

	if p.RecommendedApproach != "" {
		b.WriteString("## Recommended approach\n\n")
		b.WriteString(p.RecommendedApproach)
		b.WriteString("\n\n")
	}

	if len(p.Steps) > 0 {
		b.WriteString("## Steps\n\n")
		for _, s := range p.Steps {
			writePlanStep(b, s)
		}
	}

	if len(p.TestPlan) > 0 {
		b.WriteString("## Test plan\n\n")
		for _, v := range p.TestPlan {
			writeValidationStep(b, v)
		}
		b.WriteString("\n")
	}

	if len(p.Compatibility) > 0 || len(p.Risks) > 0 {
		b.WriteString("## Risks & compatibility\n\n")
		if len(p.Compatibility) > 0 {
			b.WriteString("**Compatibility:**\n\n")
			writeBullets(b, p.Compatibility)
			b.WriteString("\n")
		}
		if len(p.Risks) > 0 {
			b.WriteString("**Risks:**\n\n")
			for _, r := range p.Risks {
				writeRisk(b, r)
			}
			b.WriteString("\n")
		}
	}

	if len(p.Rollout) > 0 {
		b.WriteString("## Rollout\n\n")
		writeBullets(b, p.Rollout)
		b.WriteString("\n")
	}

	if len(p.Alternatives) > 0 {
		b.WriteString("## Alternatives\n\n")
		for _, alt := range p.Alternatives {
			fmt.Fprintf(b, "- %s", alt.Approach)
			if alt.Tradeoff != "" {
				fmt.Fprintf(b, " — %s", alt.Tradeoff)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(p.DefinitionOfDone) > 0 {
		b.WriteString("## Definition of done\n\n")
		writeBullets(b, p.DefinitionOfDone)
		b.WriteString("\n")
	}

	if len(p.OpenQuestions) > 0 {
		b.WriteString("## Open questions\n\n")
		for _, q := range p.OpenQuestions {
			fmt.Fprintf(b, "- %s\n", q.Question)
			if q.WhyItMatters != "" {
				fmt.Fprintf(b, "  - Why it matters: %s\n", q.WhyItMatters)
			}
			if q.Investigate != "" {
				fmt.Fprintf(b, "  - Investigate: %s\n", q.Investigate)
			}
		}
		b.WriteString("\n")
	}

	if p.AgentHandoff != "" {
		b.WriteString("## Agent handoff\n\n")
		b.WriteString(p.AgentHandoff)
		b.WriteString("\n\n")
	}
}

// writePlanStep renders one ordered implementation step.
func writePlanStep(b *strings.Builder, s schema.PlanStep) {
	title := s.Title
	if title == "" {
		title = s.Objective
	}
	if s.ID != "" {
		fmt.Fprintf(b, "### %s — %s\n\n", s.ID, title)
	} else {
		fmt.Fprintf(b, "### %s\n\n", title)
	}

	if s.Objective != "" && s.Objective != title {
		b.WriteString(s.Objective)
		b.WriteString("\n\n")
	}

	if len(s.DependsOn) > 0 {
		fmt.Fprintf(b, "- Depends on: %s\n", joinCode(s.DependsOn))
	}
	if len(s.Files) > 0 {
		b.WriteString("- Files:\n")
		for _, f := range s.Files {
			fmt.Fprintf(b, "  - `%s`", f.Path)
			if f.Action != "" {
				fmt.Fprintf(b, " (%s)", f.Action)
			}
			if f.Note != "" {
				fmt.Fprintf(b, " — %s", f.Note)
			}
			b.WriteString("\n")
		}
	}
	if len(s.Symbols) > 0 {
		b.WriteString("- Symbols:\n")
		for _, sym := range s.Symbols {
			fmt.Fprintf(b, "  - `%s`", sym.Name)
			if sym.Path != "" {
				fmt.Fprintf(b, " in `%s`", sym.Path)
			}
			if sym.Heuristic {
				b.WriteString(" (heuristic)")
			}
			b.WriteString("\n")
		}
	}
	if len(s.EvidenceIDs) > 0 {
		fmt.Fprintf(b, "- Evidence: %s\n", joinCode(s.EvidenceIDs))
	}

	if len(s.DetailedChanges) > 0 {
		b.WriteString("\n**Changes:**\n\n")
		writeBullets(b, s.DetailedChanges)
	}
	if len(s.Invariants) > 0 {
		b.WriteString("\n**Invariants:**\n\n")
		writeBullets(b, s.Invariants)
	}
	if len(s.Validation) > 0 {
		b.WriteString("\n**Validation:**\n\n")
		for _, v := range s.Validation {
			writeValidationStep(b, v)
		}
	}
	if len(s.Risks) > 0 {
		b.WriteString("\n**Risks:**\n\n")
		writeBullets(b, s.Risks)
	}
	b.WriteString("\n")
}

// writeHelpBody renders the body sections of an engineering help report.
func writeHelpBody(b *strings.Builder, h schema.HelpReport) {
	if h.ProblemRestatement != "" {
		b.WriteString("## Problem restatement\n\n")
		b.WriteString(h.ProblemRestatement)
		b.WriteString("\n\n")
	}

	if len(h.ObservedEvidence) > 0 {
		b.WriteString("## Observed evidence\n\n")
		for _, s := range h.ObservedEvidence {
			writeEvidenceStatement(b, s)
		}
		b.WriteString("\n")
	}

	if len(h.LikelyCauses) > 0 {
		b.WriteString("## Likely causes\n\n")
		for _, c := range h.LikelyCauses {
			status := "inference"
			if c.Verified {
				status = "verified"
			}
			fmt.Fprintf(b, "- **%s** [%s, likelihood: %s]\n", c.Hypothesis, status, c.Likelihood)
			if c.Reasoning != "" {
				fmt.Fprintf(b, "  - %s\n", c.Reasoning)
			}
			if len(c.EvidenceIDs) > 0 {
				fmt.Fprintf(b, "  - Evidence: %s\n", joinCode(c.EvidenceIDs))
			}
		}
		b.WriteString("\n")
	}

	if h.RecommendedDirection != "" {
		b.WriteString("## Recommended direction\n\n")
		b.WriteString(h.RecommendedDirection)
		b.WriteString("\n\n")
	}

	if len(h.InvestigationSteps) > 0 {
		b.WriteString("## Investigation steps\n\n")
		for _, s := range h.InvestigationSteps {
			fmt.Fprintf(b, "- %s", s.Action)
			if s.Where != "" {
				fmt.Fprintf(b, " (in `%s`)", s.Where)
			}
			b.WriteString("\n")
			if s.Expectation != "" {
				fmt.Fprintf(b, "  - Expect: %s\n", s.Expectation)
			}
		}
		b.WriteString("\n")
	}

	if len(h.ValidationSteps) > 0 {
		b.WriteString("## Validation steps\n\n")
		for _, v := range h.ValidationSteps {
			writeValidationStep(b, v)
		}
		b.WriteString("\n")
	}

	if len(h.Alternatives) > 0 {
		b.WriteString("## Alternatives\n\n")
		for _, alt := range h.Alternatives {
			fmt.Fprintf(b, "- %s", alt.Approach)
			if alt.Tradeoff != "" {
				fmt.Fprintf(b, " — %s", alt.Tradeoff)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(h.Risks) > 0 {
		b.WriteString("## Risks\n\n")
		for _, r := range h.Risks {
			writeRisk(b, r)
		}
		b.WriteString("\n")
	}

	if len(h.Assumptions) > 0 {
		b.WriteString("## Assumptions\n\n")
		for _, a := range h.Assumptions {
			fmt.Fprintf(b, "- %s", a.Statement)
			if a.Rationale != "" {
				fmt.Fprintf(b, " — %s", a.Rationale)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if h.Confidence != "" {
		fmt.Fprintf(b, "## Confidence\n\n%s\n\n", h.Confidence)
	}
}

// writeLimitations renders the always-present Limitations section.
func writeLimitations(b *strings.Builder, lims []schema.Limitation) {
	b.WriteString("## Limitations\n\n")
	if len(lims) == 0 {
		b.WriteString("None reported.\n")
		return
	}
	for _, l := range lims {
		if l.Stage != "" {
			fmt.Fprintf(b, "- **%s:** %s\n", l.Stage, l.Message)
		} else {
			fmt.Fprintf(b, "- %s\n", l.Message)
		}
	}
}

// writeEvidenceStatement renders a claim with its evidence references.
func writeEvidenceStatement(b *strings.Builder, s schema.EvidenceStatement) {
	fmt.Fprintf(b, "- %s", s.Statement)
	if len(s.EvidenceIDs) > 0 {
		fmt.Fprintf(b, " (%s)", joinCode(s.EvidenceIDs))
	}
	b.WriteString("\n")
}

// writeValidationStep renders a single validation step with optional command.
func writeValidationStep(b *strings.Builder, v schema.ValidationStep) {
	fmt.Fprintf(b, "- %s\n", v.Description)
	if v.Command != "" {
		fmt.Fprintf(b, "  - Command: `%s`\n", v.Command)
	}
	if v.Expectation != "" {
		fmt.Fprintf(b, "  - Expect: %s\n", v.Expectation)
	}
}

// writeRisk renders a single risk with optional severity and mitigation.
func writeRisk(b *strings.Builder, r schema.Risk) {
	fmt.Fprintf(b, "- %s", r.Description)
	if r.Severity != "" {
		fmt.Fprintf(b, " [%s]", r.Severity)
	}
	b.WriteString("\n")
	if r.Mitigation != "" {
		fmt.Fprintf(b, "  - Mitigation: %s\n", r.Mitigation)
	}
}

// writeBullets renders a slice of strings as a Markdown bullet list.
func writeBullets(b *strings.Builder, items []string) {
	for _, it := range items {
		fmt.Fprintf(b, "- %s\n", it)
	}
}

// joinCode renders a slice of identifiers as comma-separated inline code spans.
func joinCode(items []string) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = "`" + it + "`"
	}
	return strings.Join(parts, ", ")
}

// shortSHA truncates a commit SHA to a readable prefix.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
