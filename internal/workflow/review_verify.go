package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/gregpriday/codeexpert/internal/budget"
	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/evidence"
	"github.com/gregpriday/codeexpert/internal/prompts"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// dedupeCandidates merges candidates that target the same location and concern.
func dedupeCandidates(cands []candidateFinding) []candidateFinding {
	seen := map[string]int{} // key -> index in out
	var out []candidateFinding
	for _, c := range cands {
		if strings.TrimSpace(c.Title) == "" && strings.TrimSpace(c.Claim) == "" {
			continue
		}
		key := dedupeKey(c)
		if idx, ok := seen[key]; ok {
			// Merge evidence and prefer the higher severity.
			out[idx].EvidenceIDs = dedupeStrings(append(out[idx].EvidenceIDs, c.EvidenceIDs...))
			if severityRank(c.Severity) > severityRank(out[idx].Severity) {
				out[idx].Severity = c.Severity
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, c)
	}
	return out
}

func dedupeKey(c candidateFinding) string {
	return strings.ToLower(strings.TrimSpace(c.Location.Path)) + "|" +
		itoa(c.Location.StartLine/4) + "|" + // bucket nearby lines together
		string(c.Category)
}

// verdict is the verifier's decision for one candidate.
type verdict struct {
	Index         int                  `json:"index"`
	Keep          bool                 `json:"keep"`
	EvidenceLevel schema.EvidenceLevel `json:"evidence_level"`
	Severity      schema.Severity      `json:"severity"`
	Blocking      bool                 `json:"blocking"`
	Reason        string               `json:"reason"`
}

type verifiedCandidate struct {
	cand candidateFinding
	v    verdict
}

// verifyCandidates asks the verifier model to reject unsupported candidates. It
// is instructed to default to rejection when uncertain.
func (e *Engine) verifyCandidates(ctx context.Context, rs *repo.ReviewSnapshot, cands []candidateFinding,
	evid *evidence.Store, model, effort string, tracker *budget.Tracker, usage *usageAccumulator) []verifiedCandidate {

	if len(cands) == 0 {
		return nil
	}
	_ = rs
	system := prompts.MustGet(prompts.CommonSystem) + "\n\n" + prompts.MustGet(prompts.ReviewVerify)
	sess := e.NewSession(model, effort, system, nil, tracker, usage, nil)

	var b strings.Builder
	b.WriteString("# Candidate findings to verify\nFor each, decide keep (true/false), evidence_level (A/B/C/D), severity, and blocking. Reject unsupported candidates; default to keep=false when uncertain.\nJudge each claim against the resolved evidence records shown below; if a cited record is marked NOT FOUND, treat the claim as unsupported.\n\n")
	for i, c := range cands {
		fmt.Fprintf(&b, "## Candidate %d\nTitle: %s\nLocation: %s:%d-%d\nCategory: %s\nClaim: %s\nTrigger: %s\nImpact: %s\nRecommendation: %s\nAssumptions: %s\n%s\n",
			i, c.Title, c.Location.Path, c.Location.StartLine, c.Location.EndLine, c.Category, c.Claim, c.Trigger, c.Impact,
			c.Recommendation, strings.Join(c.Assumptions, "; "), renderEvidence(evid, c.EvidenceIDs))
	}
	b.WriteString("Return JSON {\"verdicts\": [{index, keep, evidence_level, severity, blocking, reason}, ...]} with one entry per candidate index.")

	type verdictList struct {
		Verdicts []verdict `json:"verdicts"`
	}
	out := inferSchema[verdictList]("review_verdicts")
	raw, err := sess.Synthesize(ctx, b.String(), out)
	if err != nil {
		e.Log.Warn("verification failed; suppressing all candidates", "err", err.Error())
		return nil
	}
	var vl verdictList
	if jsonUnmarshal(raw, &vl) != nil {
		return nil
	}
	byIdx := map[int]verdict{}
	for _, v := range vl.Verdicts {
		byIdx[v.Index] = v
	}
	var verified []verifiedCandidate
	for i, c := range cands {
		v, ok := byIdx[i]
		if !ok {
			continue // no verdict => suppress (default reject)
		}
		verified = append(verified, verifiedCandidate{cand: c, v: v})
	}
	return verified
}

// applyGates enforces the deterministic publication gates and policy limits,
// recording suppression reasons.
func applyGates(verified []verifiedCandidate, snap *repo.Snapshot, evid *evidence.Store, changed map[string][]repo.LineRange,
	cfg config.Config, policy schema.ReviewPolicy, stats *schema.SuppressionStats) []verifiedCandidate {

	val := evidence.NewValidator(snap)
	minLevel := minEvidenceLevel(cfg, policy)
	includeStyle := cfg.Review.IncludeStyle || policy.IncludeStyle

	var survivors []verifiedCandidate
	suppress := func(reason string) {
		stats.Suppressed++
		stats.ByReason[reason]++
	}
	for _, vc := range verified {
		if !vc.v.Keep {
			suppress("verifier_rejected")
			continue
		}
		if vc.cand.Location.Path == "" || !val.FileExists(vc.cand.Location.Path) {
			suppress("invalid_location")
			continue
		}
		if err := val.ValidateLocation(nil, vc.cand.Location.Path, vc.cand.Location.StartLine, vc.cand.Location.EndLine); err != nil {
			suppress("invalid_location")
			continue
		}
		// Deterministic changed-line attribution: a finding must point inside (or
		// adjacent to) a changed hunk. Files with no recorded ranges (e.g. newly
		// added or untracked) cannot be attributed deterministically, so they
		// fall through to the verifier's judgment rather than being dropped.
		if !withinChange(changed[vc.cand.Location.Path], vc.cand.Location.StartLine, vc.cand.Location.EndLine, cfg.Review.LineOverlapTolerance) {
			suppress("outside_change")
			continue
		}
		if vc.cand.Category == schema.CategoryStyle && !includeStyle {
			suppress("style_disabled")
			continue
		}
		// The model's self-reported evidence level is never trusted above what the
		// cited, store-resident records can support (A needs an executed check; B
		// needs tool-derived evidence; a validated in-change location is itself C).
		vc.v.EvidenceLevel = capEvidenceLevel(vc.v.EvidenceLevel, evid, vc.cand.EvidenceIDs)
		if evidenceRank(vc.v.EvidenceLevel) < evidenceRank(minLevel) {
			suppress("below_evidence_threshold")
			continue
		}
		if strings.TrimSpace(vc.cand.Recommendation) == "" {
			suppress("no_recommendation")
			continue
		}
		survivors = append(survivors, vc)
	}

	// Rank: blocking first, then severity, then evidence strength.
	sort.SliceStable(survivors, func(i, j int) bool {
		if survivors[i].v.Blocking != survivors[j].v.Blocking {
			return survivors[i].v.Blocking
		}
		if severityRank(survivors[i].v.Severity) != severityRank(survivors[j].v.Severity) {
			return severityRank(survivors[i].v.Severity) > severityRank(survivors[j].v.Severity)
		}
		return evidenceRank(survivors[i].v.EvidenceLevel) > evidenceRank(survivors[j].v.EvidenceLevel)
	})

	// Apply count limits.
	maxBlocking := cfg.Review.MaxBlockingFindings
	if policy.MaxBlockingFindings > 0 {
		maxBlocking = policy.MaxBlockingFindings
	}
	maxTotal := cfg.Review.MaxTotalFindings
	if policy.MaxTotalFindings > 0 {
		maxTotal = policy.MaxTotalFindings
	}
	var limited []verifiedCandidate
	blocking := 0
	for _, vc := range survivors {
		if maxTotal > 0 && len(limited) >= maxTotal {
			suppress("over_total_limit")
			continue
		}
		if vc.v.Blocking {
			if maxBlocking > 0 && blocking >= maxBlocking {
				vc.v.Blocking = false // demote rather than drop
			} else {
				blocking++
			}
		}
		limited = append(limited, vc)
	}
	return limited
}

// finalizeFindings assembles the surviving candidates into published findings.
// It is fully deterministic and cannot introduce new findings. Evidence refs are
// resolved against the store so only real, provenance-backed records are emitted
// (model-supplied IDs that do not resolve are silently dropped).
func (e *Engine) finalizeFindings(survivors []verifiedCandidate, evid *evidence.Store) []schema.ReviewFinding {
	findings := make([]schema.ReviewFinding, 0, len(survivors))
	for i, vc := range survivors {
		c := vc.cand
		sev := vc.v.Severity
		if sev == "" {
			sev = c.Severity
		}
		level := vc.v.EvidenceLevel
		if level == "" {
			level = schema.EvidenceCodePath
		}
		findings = append(findings, schema.ReviewFinding{
			ID:             fmt.Sprintf("F%d", i+1),
			Title:          c.Title,
			Category:       c.Category,
			Severity:       sev,
			Blocking:       vc.v.Blocking,
			EvidenceLevel:  level,
			Location:       c.Location,
			Claim:          c.Claim,
			Trigger:        c.Trigger,
			Impact:         c.Impact,
			Evidence:       resolveRefs(evid, c.EvidenceIDs),
			Recommendation: c.Recommendation,
			Verification:   schema.VerificationInfo{Method: "verifier+location", Confirmed: true, Detail: vc.v.Reason},
			Assumptions:    c.Assumptions,
		})
	}
	return findings
}

// resolveRefs returns the store-resident references for the cited IDs, dropping
// any that do not exist in the evidence store.
func resolveRefs(evid *evidence.Store, ids []string) []schema.EvidenceRef {
	if evid == nil {
		return nil
	}
	return evid.RefsFor(ids)
}

func minEvidenceLevel(cfg config.Config, policy schema.ReviewPolicy) schema.EvidenceLevel {
	v := cfg.Review.MinimumEvidence
	if policy.MinimumEvidence != "" {
		v = policy.MinimumEvidence
	}
	switch strings.ToLower(v) {
	case "executable", "a":
		return schema.EvidenceExecutable
	case "tool-supported", "tool", "b":
		return schema.EvidenceTool
	case "speculative", "d":
		return schema.EvidenceSpeculative
	default: // code-path
		return schema.EvidenceCodePath
	}
}

func evidenceRank(l schema.EvidenceLevel) int {
	switch l {
	case schema.EvidenceExecutable:
		return 4
	case schema.EvidenceTool:
		return 3
	case schema.EvidenceCodePath:
		return 2
	case schema.EvidenceSpeculative:
		return 1
	}
	return 0
}

// renderEvidence resolves cited evidence IDs against the store and renders the
// underlying records so the verifier judges against real evidence rather than
// opaque ID strings. IDs that do not resolve are flagged as untrustworthy.
func renderEvidence(evid *evidence.Store, ids []string) string {
	if len(ids) == 0 {
		return "Evidence: none cited (judge from the diff and location only)."
	}
	var b strings.Builder
	b.WriteString("Evidence records:\n")
	var missing []string
	for _, id := range ids {
		rec, ok := lookupEvidence(evid, id)
		if !ok {
			missing = append(missing, id)
			continue
		}
		loc := rec.Path
		if rec.StartLine > 0 {
			loc = fmt.Sprintf("%s:%d-%d", rec.Path, rec.StartLine, rec.EndLine)
		}
		summary := rec.Summary
		if len(summary) > 200 {
			summary = summary[:200] + "…"
		}
		fmt.Fprintf(&b, "  - [%s] %s %s — %s\n", rec.Kind, id, loc, summary)
	}
	if len(missing) > 0 {
		fmt.Fprintf(&b, "  - NOT FOUND in evidence store (do not trust): %s\n", strings.Join(missing, ", "))
	}
	return b.String()
}

func lookupEvidence(evid *evidence.Store, id string) (schema.EvidenceRecord, bool) {
	if evid == nil {
		return schema.EvidenceRecord{}, false
	}
	return evid.Get(id)
}

// withinChange reports whether [start,end] overlaps any changed range, allowing a
// small adjacent-context tolerance (tol lines on each side). Empty ranges or a
// zero start mean the finding cannot be attributed to a hunk deterministically,
// so it passes through. A tol of 0 requires exact overlap.
func withinChange(ranges []repo.LineRange, start, end, tol int) bool {
	if len(ranges) == 0 || start <= 0 {
		return true
	}
	if end < start {
		end = start
	}
	for _, r := range ranges {
		if start <= r.End+tol && end >= r.Start-tol {
			return true
		}
	}
	return false
}

// capEvidenceLevel limits a self-reported level to what the cited, store-resident
// records can support. It only ever lowers a level, never upgrades a claim.
func capEvidenceLevel(claimed schema.EvidenceLevel, evid *evidence.Store, ids []string) schema.EvidenceLevel {
	achievable := achievableEvidenceLevel(evid, ids)
	if evidenceRank(claimed) <= evidenceRank(achievable) {
		return claimed
	}
	return achievable
}

// achievableEvidenceLevel is the strongest level the cited records justify. A
// validated, in-change location is itself code-path (C) evidence, so C is the
// floor; tool-derived records raise it to B and an executed check to A.
func achievableEvidenceLevel(evid *evidence.Store, ids []string) schema.EvidenceLevel {
	best := schema.EvidenceCodePath
	for _, id := range ids {
		rec, ok := lookupEvidence(evid, id)
		if !ok {
			continue
		}
		var lvl schema.EvidenceLevel
		switch rec.Kind {
		case schema.EvidenceKindCheck:
			lvl = schema.EvidenceExecutable
		case schema.EvidenceKindSymbol, schema.EvidenceKindSearch, schema.EvidenceKindDiff, schema.EvidenceKindHistory:
			lvl = schema.EvidenceTool
		default:
			lvl = schema.EvidenceCodePath
		}
		if evidenceRank(lvl) > evidenceRank(best) {
			best = lvl
		}
	}
	return best
}
