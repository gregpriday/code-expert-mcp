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

// dedupeKey fingerprints a candidate by what it actually claims — path, enclosing
// symbol, category, and the normalized trigger and claim — rather than by a line
// bucket. Two passes that report the SAME defect (identical trigger/claim) merge
// even if their reported lines differ slightly; two DISTINCT defects on the same
// line stay separate because their trigger/claim differ. The old line-bucket key
// did the opposite, merging distinct nearby defects and splitting identical ones.
func dedupeKey(c candidateFinding) string {
	return strings.Join([]string{
		strings.ToLower(strings.TrimSpace(c.Location.Path)),
		normalizeFingerprintText(c.Location.Symbol),
		string(c.Category),
		normalizeFingerprintText(c.Trigger),
		normalizeFingerprintText(c.Claim),
	}, "|")
}

// normalizeFingerprintText lowercases, drops non-alphanumeric characters, and
// collapses runs so trivial wording/whitespace differences do not defeat dedup.
func normalizeFingerprintText(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevSpace = false
		default:
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// numberedSnippet renders content lines [start-ctx, end+ctx] with 1-based line
// numbers so the verifier can reason about exact locations. A non-positive start
// falls back to the head of the file.
func numberedSnippet(content []byte, start, end, ctxLines int) string {
	lines := strings.Split(string(content), "\n")
	if len(lines) == 0 {
		return ""
	}
	lo, hi := start-ctxLines, end+ctxLines
	if start <= 0 {
		lo, hi = 1, 24
	}
	if lo < 1 {
		lo = 1
	}
	if hi > len(lines) {
		hi = len(lines)
	}
	if end < start {
		hi = lo + ctxLines
		if hi > len(lines) {
			hi = len(lines)
		}
	}
	// A location past EOF (a model hallucination) would leave lo > hi and render
	// nothing; clamp lo down so the verifier still sees the file tail with context
	// rather than an empty code block.
	if lo > hi {
		lo = hi - 2*ctxLines
		if lo < 1 {
			lo = 1
		}
	}
	var b strings.Builder
	for i := lo; i <= hi; i++ {
		fmt.Fprintf(&b, "%5d| %s\n", i, lines[i-1])
	}
	return b.String()
}

// buildLocationPacket assembles the location-specific evidence the verifier needs
// to judge a candidate against real code rather than its own description: the
// frozen changed hunk for the file and the actual head-view (or, for a deletion,
// removed base) source around the cited location. Cited corroborating evidence is
// rendered once in the shared catalog and referenced by ID, so it is not
// duplicated here.
func (e *Engine) buildLocationPacket(ctx context.Context, rs *repo.ReviewSnapshot, cand candidateFinding) string {
	if rs == nil {
		return ""
	}
	path := cand.Location.Path
	var cf *repo.ChangedFile
	for i := range rs.Manifest().Files {
		if rs.Manifest().Files[i].Path == path {
			cf = &rs.Manifest().Files[i]
			break
		}
	}
	var b strings.Builder
	if cf != nil && cf.Diff != "" {
		fmt.Fprintf(&b, "Frozen changed hunk (%s):\n```diff\n%s\n```\n", cf.Status, truncateStr(cf.Diff, 2400))
	} else {
		b.WriteString("Frozen changed hunk: none recorded for this path (the location may be outside the change).\n")
	}
	switch {
	case cf != nil && cf.Status == "D":
		if data, err := rs.ReadBase(ctx, path); err == nil {
			fmt.Fprintf(&b, "Removed (base) source near the finding:\n```\n%s```\n", numberedSnippet(data, cand.Location.StartLine, cand.Location.EndLine, 4))
		}
	case path != "":
		if fc, err := rs.ReadHead(ctx, path); err == nil && !fc.Meta.Binary {
			fmt.Fprintf(&b, "Head source %s around lines %d-%d:\n```\n%s```\n", path, cand.Location.StartLine, cand.Location.EndLine,
				numberedSnippet(fc.Bytes, cand.Location.StartLine, cand.Location.EndLine, 4))
		}
	}
	return b.String()
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
	system := prompts.MustGet(prompts.CommonSystem) + "\n\n" + prompts.MustGet(prompts.ReviewVerify)
	sess := e.NewSession(model, effort, system, nil, tracker, usage, nil)

	var b strings.Builder
	b.WriteString("# Candidate findings to verify\nFor each, decide keep (true/false), evidence_level (A/B/C/D), severity, and blocking. Reject unsupported candidates; default to keep=false when uncertain. Judge each candidate against the REAL CODE in its evidence packet below — the frozen hunk and the head/base source — not against the candidate's own wording. You may only LOWER an evidence level; a level you cannot justify from the shown code must come down.\nEach candidate also cites corroborating evidence by ID; resolve those IDs against the Evidence catalog. If a cited ID is listed as NOT FOUND, treat that support as absent.\n\n")
	b.WriteString(renderEvidenceForVerifier(evid, cands))
	b.WriteString("\n")
	for i, c := range cands {
		fmt.Fprintf(&b, "## Candidate %d\nTitle: %s\nLocation: %s:%d-%d\nCategory: %s\nClaim: %s\nTrigger: %s\nImpact: %s\nRecommendation: %s\nAssumptions: %s\n%s\n\n### Evidence packet (real code)\n%s\n",
			i, c.Title, c.Location.Path, c.Location.StartLine, c.Location.EndLine, c.Category, c.Claim, c.Trigger, c.Impact,
			c.Recommendation, strings.Join(c.Assumptions, "; "), renderCandidateEvidenceRefs(c.EvidenceIDs),
			e.buildLocationPacket(ctx, rs, c))
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
	deleted map[string]bool, cfg config.Config, policy schema.ReviewPolicy, stats *schema.SuppressionStats) []verifiedCandidate {

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
		if vc.cand.Location.Path == "" {
			suppress("invalid_location")
			continue
		}
		// A finding attributed to a deleted file cannot be validated against the
		// head (the file is gone) or against changed head ranges (there are none).
		// Its lines live only in the base, so accept it on path identity alone —
		// it is a finding about removed content, which is exactly what we want to
		// surface — and skip the head-oriented location and attribution gates.
		isDeletion := deleted[vc.cand.Location.Path]
		if !isDeletion {
			if !val.FileExists(vc.cand.Location.Path) {
				suppress("invalid_location")
				continue
			}
			if err := val.ValidateLocation(nil, vc.cand.Location.Path, vc.cand.Location.StartLine, vc.cand.Location.EndLine); err != nil {
				suppress("invalid_location")
				continue
			}
			// Deterministic changed-line attribution: a finding must point inside
			// (or adjacent to) a changed hunk. Files with no recorded ranges (e.g.
			// newly added or untracked) cannot be attributed deterministically, so
			// they fall through to the verifier's judgment rather than being dropped.
			if !withinChange(changed[vc.cand.Location.Path], vc.cand.Location.StartLine, vc.cand.Location.EndLine, cfg.Review.LineOverlapTolerance) {
				suppress("outside_change")
				continue
			}
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

	// Apply the total-findings cap. A blocker is NEVER demoted or dropped for a
	// display limit: hiding or downgrading a genuine blocker to fit a count is the
	// most dangerous thing a review can do. The cap drops only the lowest-priority
	// NON-blocking overflow (survivors are already sorted blocking-first); every
	// blocker is always published.
	maxTotal := cfg.Review.MaxTotalFindings
	if policy.MaxTotalFindings > 0 {
		maxTotal = policy.MaxTotalFindings
	}
	var limited []verifiedCandidate
	for _, vc := range survivors {
		if maxTotal > 0 && len(limited) >= maxTotal && !vc.v.Blocking {
			suppress("over_total_limit")
			continue
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
		// Confirmation strength is derived from the (already capped) evidence level,
		// not asserted by the model. Only an executed check (level A) is "confirmed";
		// everything else carries an honest status.
		status := schema.ConfirmationFromEvidence(level)
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
			Verification: schema.VerificationInfo{
				Method:    "verifier+evidence-packet",
				Status:    status,
				Confirmed: level == schema.EvidenceExecutable,
				Detail:    vc.v.Reason,
			},
			Assumptions: c.Assumptions,
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

// renderEvidenceForVerifier resolves every evidence ID cited across all
// candidates and renders each underlying record exactly once, so the verifier
// judges against real evidence rather than opaque ID strings. Deduplicating here
// means a record cited by N candidates is rendered once instead of N times —
// candidates reference it by ID alone (see renderCandidateEvidenceRefs). IDs that
// do not resolve are flagged once as untrustworthy. The name marks this as
// verification-context rendering: missing evidence is surfaced as
// not-to-be-trusted rather than silently omitted.
func renderEvidenceForVerifier(evid *evidence.Store, cands []candidateFinding) string {
	seen := map[string]bool{}
	var ids []string
	for _, c := range cands {
		for _, id := range c.EvidenceIDs {
			// Normalize before dedup/lookup so a padded "E-1 " resolves to the same
			// record as "E-1" instead of being falsely flagged NOT FOUND.
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "# Evidence catalog\nNo evidence cited by any candidate; judge from the candidate descriptions and locations only.\n"
	}
	var b strings.Builder
	b.WriteString("# Evidence catalog (resolved records; candidates cite these by ID)\n")
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

// renderCandidateEvidenceRefs emits the compact per-candidate evidence line: the
// cited IDs only, resolved against the catalog rendered once at the top of the
// prompt. This avoids re-expanding the same record under every candidate.
func renderCandidateEvidenceRefs(ids []string) string {
	var cited []string
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			cited = append(cited, id)
		}
	}
	if len(cited) == 0 {
		return "Evidence: none cited (judge from the candidate description and location only)."
	}
	return "Evidence IDs (see catalog above): " + strings.Join(cited, ", ")
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
