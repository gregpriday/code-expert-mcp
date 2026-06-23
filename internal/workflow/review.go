package workflow

import (
	"context"
	"sort"
	"strings"

	"github.com/gregpriday/codeexpert/internal/budget"
	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/evidence"
	"github.com/gregpriday/codeexpert/internal/llmtools"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/report"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// Review runs the precision-first review workflow over a frozen Git change set.
func (e *Engine) Review(ctx context.Context, req schema.ReviewRequest, opts RunOptions) (schema.ReviewResult, error) {
	runID := opts.RunID
	if runID == "" {
		runID = newRunID("review")
	}
	progress := func(stage, detail string) {
		if opts.Progress != nil {
			opts.Progress(stage, detail)
		}
	}

	progress("snapshot", "freezing review target")
	snap, err := e.resolveAndSnapshot(ctx, req.Root, opts.AllowedRoots)
	if err != nil {
		return schema.ReviewResult{}, err
	}
	if !snap.IsGit() {
		return schema.ReviewResult{}, schema.NewError(schema.CodeNotGitRepository, "review requires a Git repository")
	}
	target := req.Target
	if target.Type == "" {
		target.Type = schema.TargetWorkingTree
	}
	rs, err := repo.BuildReviewSnapshot(ctx, snap, target, e.Cfg.Repository)
	if err != nil {
		return schema.ReviewResult{}, err
	}
	manifest := rs.Manifest()

	profile := resolveProfile(req.Profile, e.Cfg.Review.DefaultProfile)
	limits := resolveLimits(e.Cfg, profile, req.Budget)
	tracker := budget.New(limits)
	usage := &usageAccumulator{}
	evid := evidence.NewStore(snap.ID())
	guidance := e.guidanceList(ctx, snap)

	reg := llmtools.New(llmtools.Options{
		Snapshot: snap, Review: rs, Evidence: evid, Budget: tracker, Config: e.Cfg,
		Guidance: guidance, Logger: e.Log,
	})

	// Deterministic risk map.
	progress("risk-map", "routing review effort")
	riskMap := buildRiskMap(manifest)
	complexity, highRisk := reviewComplexity(manifest)

	// Empty change set: nothing to review.
	reviewable := reviewableFiles(manifest, e.Cfg, req.Policy)
	if len(reviewable) == 0 {
		return e.assembleReview(ctx, runID, rs, riskMap, nil, nil, evid, tracker, usage,
			[]schema.Limitation{{Stage: "scope", Message: "no reviewable changed files in the target scope"}}, profile), nil
	}

	// Enrich the manifest with enclosing changed symbols so the tool-less
	// diff-local pass receives what its prompt references.
	enrichEnclosingSymbols(ctx, snap, manifest)

	// Candidate passes (concurrent).
	progress("candidates", "running candidate passes")
	candidates := e.runCandidatePasses(ctx, rs, reg, riskMap, req, tracker, usage, profile, opts.Progress)

	// Deduplicate.
	deduped := dedupeCandidates(candidates)

	var findings []schema.ReviewFinding
	suppressed := schema.SuppressionStats{TotalCandidates: len(candidates), ByReason: map[string]int{}}
	var limitations []schema.Limitation

	if len(deduped) > 0 {
		// Deterministic changed-line attribution: map each changed file to its
		// frozen head-view hunks so the gates can confirm findings touch the change.
		changedRanges := map[string][]repo.LineRange{}
		for _, f := range manifest.Files {
			if len(f.NewRanges) > 0 {
				changedRanges[f.Path] = f.NewRanges
			}
		}

		// Verify and gate.
		progress("verification", "verifying candidates")
		model, effort := e.routedSynthesisModel("review_verify", profile, complexity, highRisk)
		verified := e.verifyCandidates(ctx, rs, deduped, evid, model, effort, tracker, usage)
		survivors := applyGates(verified, snap, evid, changedRanges, e.Cfg, req.Policy, &suppressed)

		// Finalize (rank/phrase only survivors).
		progress("synthesis", "finalizing findings")
		if len(survivors) > 0 {
			findings = e.finalizeFindings(survivors, evid)
		}
	}

	if tracker.TimedOut() {
		limitations = append(limitations, schema.Limitation{Stage: "budget", Message: "time budget reached; coverage may be incomplete"})
	}
	if reason, limited := tracker.Exhausted(); limited {
		limitations = append(limitations, schema.Limitation{Stage: "budget", Message: reason + "; coverage may be incomplete"})
	}
	progress("complete", "done")
	return e.assembleReview(ctx, runID, rs, riskMap, findings, &suppressed, evid, tracker, usage, limitations, profile), nil
}

// assembleReview builds the final ReviewResult, coverage, summary, and renders.
func (e *Engine) assembleReview(ctx context.Context, runID string, rs *repo.ReviewSnapshot, riskMap []schema.RiskArea,
	findings []schema.ReviewFinding, suppressed *schema.SuppressionStats, evid *evidence.Store, tracker *budget.Tracker,
	usage *usageAccumulator, limitations []schema.Limitation, profile schema.AnalysisProfile) schema.ReviewResult {

	manifest := rs.Manifest()
	coverage := buildCoverage(manifest, e.Cfg, riskMap, findings)
	status := schema.StatusComplete
	if _, limited := tracker.Exhausted(); tracker.TimedOut() || limited {
		status = schema.StatusPartial
	}

	blocking := 0
	highest := schema.Severity("")
	for _, f := range findings {
		if f.Blocking {
			blocking++
		}
		highest = higherSeverity(highest, f.Severity)
	}
	conclusion := "No blocking findings were found within the automated review scope."
	headline := conclusion
	if len(findings) > 0 {
		headline = pluralFindings(len(findings), blocking)
		conclusion = "Review surfaced " + headline + ". This is an automated, non-exhaustive review, not a merge approval, and makes no guarantee that the change is correct or complete."
	}

	if suppressed != nil {
		suppressed.Published = len(findings)
	}

	result := schema.ReviewResult{
		RunID:    runID,
		Status:   status,
		Snapshot: rs.SnapshotRef(),
		Summary: schema.ReviewSummary{
			Headline:        headline,
			TotalFindings:   len(findings),
			BlockingCount:   blocking,
			FilesReviewed:   len(coverage.ReviewedFiles),
			FilesChanged:    len(manifest.Files),
			HighestSeverity: highest,
			Conclusion:      conclusion,
		},
		RiskMap:     riskMap,
		Findings:    findings,
		Coverage:    coverage,
		Limitations: limitations,
		Usage:       finalizeUsage(usage, tracker),
	}
	if suppressed != nil {
		result.Suppressed = *suppressed
	}

	md := report.ReviewMarkdown(result)
	result.Markdown = md
	if uri := e.persistArtifact(runID, "report", []byte(md)); uri != "" {
		result.ReportURI = uri
	}
	if mj, _ := report.ReviewJSON(result); mj != "" {
		e.persistArtifact(runID, "result.json", []byte(mj))
	}
	return result
}

// reviewableFiles filters the manifest to files in scope for review.
func reviewableFiles(m *repo.ChangeManifest, cfg config.Config, policy schema.ReviewPolicy) []repo.ChangedFile {
	includeGen := cfg.Review.IncludeGenerated || policy.IncludeGenerated
	var out []repo.ChangedFile
	for _, f := range m.Files {
		if f.Status == "D" {
			continue // deletions: nothing in head to review
		}
		if f.Binary {
			continue
		}
		if f.Vendored {
			continue
		}
		if f.Generated && !includeGen {
			continue
		}
		out = append(out, f)
	}
	return out
}

// buildRiskMap derives review focus areas from deterministic change signals.
func buildRiskMap(m *repo.ChangeManifest) []schema.RiskArea {
	type acc struct {
		paths    map[string]bool
		priority int
	}
	areas := map[schema.FindingCategory]*acc{}
	bump := func(cat schema.FindingCategory, path string, pri int) {
		a := areas[cat]
		if a == nil {
			a = &acc{paths: map[string]bool{}}
			areas[cat] = a
		}
		if path != "" {
			a.paths[path] = true
		}
		a.priority += pri
	}
	for _, f := range m.Files {
		lower := strings.ToLower(f.Path)
		switch {
		case containsAny(lower, "auth", "login", "password", "token", "permission", "acl", "crypto", "secret", "deserial", "unmarshal", "parse"):
			bump(schema.CategorySecurity, f.Path, 3)
		}
		if containsAny(lower, "migrat", "schema", "persist", "cache", "store", "repository", "dao") {
			bump(schema.CategoryDataIntegrity, f.Path, 2)
		}
		if containsAny(lower, "lock", "mutex", "goroutine", "async", "queue", "worker", "concurren", "atomic", "channel") {
			bump(schema.CategoryConcurrency, f.Path, 2)
		}
		if containsAny(lower, "api", "schema", "proto", "interface", "handler", "endpoint", "route") {
			bump(schema.CategoryCompatibility, f.Path, 1)
		}
		if containsAny(lower, "loop", "query", "batch", "scan", "list", "render") {
			bump(schema.CategoryPerformance, f.Path, 1)
		}
		if containsAny(lower, "retry", "timeout", "error", "fail", "recover") {
			bump(schema.CategoryReliability, f.Path, 1)
		}
		if isDependencyOrBuildFile(lower) {
			// Dependency and build/config manifest changes can break consumers and
			// build reproducibility even when no source logic changes.
			bump(schema.CategoryCompatibility, f.Path, 2)
		}
		if isTestPathLite(lower) {
			bump(schema.CategoryTesting, f.Path, 1)
		}
		// Every changed file gets baseline correctness attention.
		bump(schema.CategoryCorrectness, f.Path, 1)
	}

	var out []schema.RiskArea
	for cat, a := range areas {
		paths := make([]string, 0, len(a.paths))
		for p := range a.paths {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		if len(paths) > 8 {
			paths = paths[:8]
		}
		out = append(out, schema.RiskArea{
			Category:  cat,
			Rationale: riskRationale(cat),
			Paths:     paths,
			Priority:  a.priority,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].Category < out[j].Category
	})
	return out
}

func riskRationale(cat schema.FindingCategory) string {
	switch cat {
	case schema.CategorySecurity:
		return "changed files touch authentication, trust boundaries, parsing, or secrets"
	case schema.CategoryDataIntegrity:
		return "changed files touch migrations, persistence, or cache keys"
	case schema.CategoryConcurrency:
		return "changed files touch locks, goroutines, queues, or async paths"
	case schema.CategoryCompatibility:
		return "changed files touch public interfaces, schemas, or protocols"
	case schema.CategoryPerformance:
		return "changed files contain loops, queries, or batch operations"
	case schema.CategoryReliability:
		return "changed files touch retries, timeouts, or error paths"
	case schema.CategoryTesting:
		return "test files changed; verify coverage of new behavior"
	default:
		return "baseline functional correctness review of the change"
	}
}

func buildCoverage(m *repo.ChangeManifest, cfg config.Config, riskMap []schema.RiskArea, findings []schema.ReviewFinding) schema.ReviewCoverage {
	cov := schema.ReviewCoverage{}
	includeGen := cfg.Review.IncludeGenerated
	for _, f := range m.Files {
		switch {
		case f.Status == "D":
			cov.SkippedFiles = append(cov.SkippedFiles, schema.SkippedFile{Path: f.Path, Reason: "deleted"})
		case f.Binary:
			cov.SkippedFiles = append(cov.SkippedFiles, schema.SkippedFile{Path: f.Path, Reason: "binary"})
		case f.Vendored:
			cov.SkippedFiles = append(cov.SkippedFiles, schema.SkippedFile{Path: f.Path, Reason: "vendored"})
		case f.Generated && !includeGen:
			cov.SkippedFiles = append(cov.SkippedFiles, schema.SkippedFile{Path: f.Path, Reason: "generated"})
		default:
			cov.ReviewedFiles = append(cov.ReviewedFiles, f.Path)
			cov.ChangedLineEstimate += f.Added + f.Deleted
		}
	}
	for _, ra := range riskMap {
		cov.SpecialistPasses = append(cov.SpecialistPasses, string(ra.Category))
	}
	return cov
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// isDependencyOrBuildFile reports whether a path is a dependency lockfile or a
// build/packaging manifest, by basename.
func isDependencyOrBuildFile(lowerPath string) bool {
	base := lowerPath
	if i := strings.LastIndexByte(lowerPath, '/'); i >= 0 {
		base = lowerPath[i+1:]
	}
	switch base {
	case "go.mod", "go.sum", "package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
		"cargo.toml", "cargo.lock", "requirements.txt", "pyproject.toml", "poetry.lock",
		"gemfile", "gemfile.lock", "pom.xml", "build.gradle", "dockerfile", "makefile":
		return true
	}
	return false
}

func isTestPathLite(lower string) bool {
	return strings.HasSuffix(lower, "_test.go") || strings.Contains(lower, ".test.") ||
		strings.Contains(lower, ".spec.") || strings.Contains(lower, "/test")
}

func higherSeverity(a, b schema.Severity) schema.Severity {
	if severityRank(b) > severityRank(a) {
		return b
	}
	return a
}

func severityRank(s schema.Severity) int {
	switch s {
	case schema.SeverityCritical:
		return 5
	case schema.SeverityHigh:
		return 4
	case schema.SeverityMedium:
		return 3
	case schema.SeverityLow:
		return 2
	case schema.SeverityInfo:
		return 1
	}
	return 0
}

func pluralFindings(total, blocking int) string {
	s := itoa(total) + " finding"
	if total != 1 {
		s += "s"
	}
	if blocking > 0 {
		s += " (" + itoa(blocking) + " blocking)"
	}
	return s
}
