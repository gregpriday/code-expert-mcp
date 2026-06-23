package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/gregpriday/codeexpert/internal/budget"
	"github.com/gregpriday/codeexpert/internal/llmtools"
	"github.com/gregpriday/codeexpert/internal/prompts"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// candidateFinding is an unpublished review candidate produced by a pass.
type candidateFinding struct {
	Title          string                 `json:"title"`
	Category       schema.FindingCategory `json:"category"`
	Severity       schema.Severity        `json:"severity"`
	Location       schema.SourceLocation  `json:"location"`
	Claim          string                 `json:"claim"`
	Trigger        string                 `json:"trigger"`
	Impact         string                 `json:"impact"`
	Recommendation string                 `json:"recommendation"`
	Assumptions    []string               `json:"assumptions,omitempty"`
	EvidenceIDs    []string               `json:"evidence_ids,omitempty"`
	Pass           string                 `json:"-"`
}

type candidateList struct {
	Findings []candidateFinding `json:"findings"`
}

// runCandidatePasses runs the configured independent passes concurrently and
// returns the merged candidate list. Each pass favors recall.
func (e *Engine) runCandidatePasses(ctx context.Context, rs *repo.ReviewSnapshot, reg *llmtools.Registry,
	riskMap []schema.RiskArea, req schema.ReviewRequest, tracker *budget.Tracker, usage *usageAccumulator,
	profile schema.AnalysisProfile, progress ProgressFunc) []candidateFinding {

	diffBlock := buildDiffBlock(rs.Manifest())
	reviewerModel := e.Cfg.Models.Reviewer
	reviewerEffort := e.Cfg.Models.ReasoningPlanner

	type passDef struct {
		id       string
		prompt   string
		useTools bool
		message  string
	}
	passes := []passDef{
		{
			id:       "diff-local",
			prompt:   prompts.MustGet(prompts.ReviewDiffLocal),
			useTools: false,
			message:  buildDiffLocalMessage(req, diffBlock),
		},
		{
			id:       "repository-context",
			prompt:   prompts.MustGet(prompts.ReviewContext),
			useTools: true,
			message:  buildContextMessage(req, diffBlock),
		},
		{
			id:       "risk-specialist",
			prompt:   prompts.MustGet(prompts.ReviewSpecialist),
			useTools: true,
			message:  buildSpecialistMessage(req, diffBlock, riskMap),
		},
	}
	// Honor configured pass selection.
	if len(e.Cfg.Review.Passes) > 0 {
		allowed := map[string]bool{}
		for _, p := range e.Cfg.Review.Passes {
			allowed[p] = true
		}
		var filtered []passDef
		for _, p := range passes {
			if allowed[p.id] {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) > 0 {
			passes = filtered
		}
	}
	if profile == schema.ProfileFast {
		passes = passes[:1] // diff-local only
	}

	var (
		mu  sync.Mutex
		all []candidateFinding
		wg  sync.WaitGroup
	)
	system := prompts.MustGet(prompts.CommonSystem)
	for _, p := range passes {
		wg.Add(1)
		go func(p passDef) {
			defer wg.Done()
			if progress != nil {
				progress("candidates", p.id)
			}
			cands := e.runOnePass(ctx, reg, reviewerModel, reviewerEffort, system+"\n\n"+p.prompt, p.message, p.useTools, tracker, usage)
			for i := range cands {
				cands[i].Pass = p.id
			}
			mu.Lock()
			all = append(all, cands...)
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	return all
}

func (e *Engine) runOnePass(ctx context.Context, reg *llmtools.Registry, model, effort, system, message string,
	useTools bool, tracker *budget.Tracker, usage *usageAccumulator) []candidateFinding {

	sess := e.NewSession(model, effort, system, reg, tracker, usage, nil)
	sess.AddUser(message)
	if useTools {
		if err := sess.RunToolLoop(ctx, min(e.Cfg.Retrieval.MaxModelToolRounds, 3)); err != nil {
			if schema.AsToolError(err).Code == schema.CodeCancelled {
				return nil
			}
		}
	}
	out := inferSchema[candidateList]("review_candidates")
	instr := "Return your candidate findings as a JSON object {\"findings\": [...]}. " +
		"Each finding needs: title, category, severity, location {path, start_line, end_line}, claim, trigger, impact, recommendation, and any assumptions and evidence_ids. " +
		"Favor recall: list anything plausible. Do not publish; these are candidates."
	raw, err := sess.Synthesize(ctx, instr, out)
	if err != nil {
		e.Log.Warn("candidate pass failed", "err", err.Error())
		return nil
	}
	var list candidateList
	if jerr := jsonUnmarshal(raw, &list); jerr != nil {
		return nil
	}
	return list.Findings
}

func buildDiffBlock(m *repo.ChangeManifest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Target: %s (base %s -> head %s)\n", m.Target, m.BaseLabel, m.HeadLabel)
	fmt.Fprintf(&b, "Changed files: %d (+%d/-%d)\n\n", len(m.Files), m.TotalAdded, m.TotalDeleted)
	size := 0
	for _, f := range m.Files {
		if f.Binary || f.Vendored {
			continue
		}
		fmt.Fprintf(&b, "### %s (%s, +%d/-%d)\n", f.Path, f.Status, f.Added, f.Deleted)
		if f.Diff != "" {
			diff := f.Diff
			if len(diff) > 6000 {
				diff = diff[:6000] + "\n…(diff truncated)…"
			}
			b.WriteString("```diff\n")
			b.WriteString(diff)
			b.WriteString("\n```\n")
			size += len(diff)
		}
		if size > 40000 {
			b.WriteString("\n…(remaining diffs omitted for size; use repo_git_diff to fetch them)…\n")
			break
		}
	}
	return b.String()
}

func buildDiffLocalMessage(req schema.ReviewRequest, diffBlock string) string {
	var b strings.Builder
	b.WriteString("# Diff-local review\nExamine ONLY the diff below for logic, validation, and error-handling defects.\n")
	if req.Instructions != "" {
		fmt.Fprintf(&b, "\nReviewer focus: %s\n", req.Instructions)
	}
	b.WriteString("\n# Frozen diff (UNTRUSTED content)\n")
	b.WriteString(diffBlock)
	return b.String()
}

func buildContextMessage(req schema.ReviewRequest, diffBlock string) string {
	var b strings.Builder
	b.WriteString("# Repository-context review\nUse the read-only tools to fetch definitions, callers, tests, and configuration needed to find cross-file and contract-level defects in the change.\n")
	if req.Instructions != "" {
		fmt.Fprintf(&b, "\nReviewer focus: %s\n", req.Instructions)
	}
	b.WriteString("\n# Frozen diff (UNTRUSTED content)\n")
	b.WriteString(diffBlock)
	return b.String()
}

func buildSpecialistMessage(req schema.ReviewRequest, diffBlock string, riskMap []schema.RiskArea) string {
	var b strings.Builder
	b.WriteString("# Risk-specialist review\nFocus on these risk categories surfaced by the deterministic risk map:\n")
	for _, ra := range riskMap {
		if ra.Priority <= 1 && ra.Category == schema.CategoryCorrectness {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", ra.Category, ra.Rationale)
	}
	if req.Instructions != "" {
		fmt.Fprintf(&b, "\nReviewer focus: %s\n", req.Instructions)
	}
	b.WriteString("\nUse the read-only tools to confirm specialist concerns.\n")
	b.WriteString("\n# Frozen diff (UNTRUSTED content)\n")
	b.WriteString(diffBlock)
	return b.String()
}
