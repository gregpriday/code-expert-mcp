package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/evidence"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// testSnapshot builds a real read-only snapshot of a tiny git repo whose only
// file is main.go (5 lines). It lets the deterministic gates run without a
// provider.
func testSnapshot(t *testing.T) (*repo.Snapshot, string) {
	t.Helper()
	dir, rel := tempGitRepo(t)
	eng := newTestEngine(rel)
	snap, err := eng.resolveAndSnapshot(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return snap, rel
}

func mkCandidate(path string, start, end int, cat schema.FindingCategory, rec string, ids ...string) candidateFinding {
	return candidateFinding{
		Title:          "candidate",
		Category:       cat,
		Severity:       schema.SeverityHigh,
		Location:       schema.SourceLocation{Path: path, StartLine: start, EndLine: end},
		Claim:          "claim",
		Trigger:        "trigger",
		Impact:         "impact",
		Recommendation: rec,
		EvidenceIDs:    ids,
	}
}

func mkVerified(c candidateFinding, keep bool, level schema.EvidenceLevel, blocking bool) verifiedCandidate {
	return verifiedCandidate{cand: c, v: verdict{Keep: keep, EvidenceLevel: level, Severity: c.Severity, Blocking: blocking}}
}

// TestApplyGatesSuppressionReasons exercises every suppression reason and the
// surviving path. These gates are the project's precision-first guarantee.
func TestApplyGatesSuppressionReasons(t *testing.T) {
	snap, rel := testSnapshot(t)
	cfg := config.Defaults()
	// main.go is "changed" on line 1 only, so a finding on line 5 is out of the hunk.
	changed := map[string][]repo.LineRange{rel: {{Start: 1, End: 1}}}

	cases := []struct {
		name   string
		vc     verifiedCandidate
		policy schema.ReviewPolicy
		reason string // "" means it should survive
	}{
		{
			name:   "verifier_rejected",
			vc:     mkVerified(mkCandidate(rel, 1, 1, schema.CategoryCorrectness, "fix it"), false, schema.EvidenceCodePath, true),
			reason: "verifier_rejected",
		},
		{
			name:   "invalid_location missing file",
			vc:     mkVerified(mkCandidate("nope.go", 1, 1, schema.CategoryCorrectness, "fix it"), true, schema.EvidenceCodePath, true),
			reason: "invalid_location",
		},
		{
			name:   "invalid_location out of bounds",
			vc:     mkVerified(mkCandidate(rel, 500, 501, schema.CategoryCorrectness, "fix it"), true, schema.EvidenceCodePath, true),
			reason: "invalid_location",
		},
		{
			name:   "outside_change",
			vc:     mkVerified(mkCandidate(rel, 5, 5, schema.CategoryCorrectness, "fix it"), true, schema.EvidenceCodePath, true),
			reason: "outside_change",
		},
		{
			name:   "style_disabled",
			vc:     mkVerified(mkCandidate(rel, 1, 1, schema.CategoryStyle, "fix it"), true, schema.EvidenceCodePath, true),
			reason: "style_disabled",
		},
		{
			name:   "below_evidence_threshold",
			vc:     mkVerified(mkCandidate(rel, 1, 1, schema.CategoryCorrectness, "fix it"), true, schema.EvidenceExecutable, true),
			policy: schema.ReviewPolicy{MinimumEvidence: "A"}, // no check record => capped to C < A
			reason: "below_evidence_threshold",
		},
		{
			name:   "no_recommendation",
			vc:     mkVerified(mkCandidate(rel, 1, 1, schema.CategoryCorrectness, "  "), true, schema.EvidenceCodePath, true),
			reason: "no_recommendation",
		},
		{
			name:   "survives",
			vc:     mkVerified(mkCandidate(rel, 1, 1, schema.CategoryCorrectness, "fix it"), true, schema.EvidenceCodePath, true),
			reason: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evid := evidence.NewStore(snap.ID())
			stats := &schema.SuppressionStats{ByReason: map[string]int{}}
			survivors := applyGates([]verifiedCandidate{tc.vc}, snap, evid, changed, nil, cfg, tc.policy, stats)
			if tc.reason == "" {
				if len(survivors) != 1 {
					t.Fatalf("expected 1 survivor, got %d (suppressed %+v)", len(survivors), stats.ByReason)
				}
				return
			}
			if len(survivors) != 0 {
				t.Fatalf("expected suppression %q, but candidate survived", tc.reason)
			}
			if stats.ByReason[tc.reason] != 1 {
				t.Fatalf("expected reason %q=1, got %+v", tc.reason, stats.ByReason)
			}
		})
	}
}

// TestApplyGatesNeverDemotesBlockers proves the total cap drops only the lowest
// priority NON-blocking overflow and never demotes or drops a genuine blocker,
// even past the configured total (hiding a blocker to fit a count is the most
// dangerous thing a review can do).
func TestApplyGatesNeverDemotesBlockers(t *testing.T) {
	snap, rel := testSnapshot(t)
	cfg := config.Defaults()
	changed := map[string][]repo.LineRange{rel: {{Start: 1, End: 5}}}
	evid := evidence.NewStore(snap.ID())

	// 3 blocking + 2 non-blocking, total cap 3 -> all 3 blockers survive blocking,
	// both non-blocking overflow dropped.
	var in []verifiedCandidate
	for i := 0; i < 3; i++ {
		in = append(in, mkVerified(mkCandidate(rel, 1+i, 1+i, schema.CategoryCorrectness, "fix it"), true, schema.EvidenceCodePath, true))
	}
	for i := 0; i < 2; i++ {
		in = append(in, mkVerified(mkCandidate(rel, 1+i, 1+i, schema.CategorySecurity, "fix it too"), true, schema.EvidenceCodePath, false))
	}
	stats := &schema.SuppressionStats{ByReason: map[string]int{}}
	survivors := applyGates(in, snap, evid, changed, nil, cfg, schema.ReviewPolicy{MaxTotalFindings: 3}, stats)
	blocking := 0
	for _, s := range survivors {
		if s.v.Blocking {
			blocking++
		}
	}
	if blocking != 3 {
		t.Errorf("all 3 blockers must survive AS blocking, got %d blocking of %d survivors", blocking, len(survivors))
	}
	if stats.ByReason["over_total_limit"] != 2 {
		t.Errorf("expected 2 non-blocking dropped by the total cap, got %+v", stats.ByReason)
	}

	// 4 blockers, cap 3 -> all 4 still survive; a blocker is never dropped to fit.
	var allBlock []verifiedCandidate
	for i := 0; i < 4; i++ {
		allBlock = append(allBlock, mkVerified(mkCandidate(rel, 1+i, 1+i, schema.CategoryCorrectness, "fix it"), true, schema.EvidenceCodePath, true))
	}
	stats = &schema.SuppressionStats{ByReason: map[string]int{}}
	survivors = applyGates(allBlock, snap, evid, changed, nil, cfg, schema.ReviewPolicy{MaxTotalFindings: 3}, stats)
	if len(survivors) != 4 {
		t.Errorf("4 blockers must all publish past a total cap of 3, got %d", len(survivors))
	}
}

func TestCapEvidenceLevel(t *testing.T) {
	store := evidence.NewStore("snap")
	store.Add(schema.EvidenceRecord{ID: "E-check", Kind: schema.EvidenceKindCheck, Summary: "ran test"})
	store.Add(schema.EvidenceRecord{ID: "E-sym", Kind: schema.EvidenceKindSymbol, Summary: "symbol"})
	store.Add(schema.EvidenceRecord{ID: "E-file", Kind: schema.EvidenceKindFile, Summary: "file"})

	cases := []struct {
		name    string
		claimed schema.EvidenceLevel
		ids     []string
		want    schema.EvidenceLevel
	}{
		{"check backs A", schema.EvidenceExecutable, []string{"E-check"}, schema.EvidenceExecutable},
		{"symbol caps A to B", schema.EvidenceExecutable, []string{"E-sym"}, schema.EvidenceTool},
		{"file caps A to C", schema.EvidenceExecutable, []string{"E-file"}, schema.EvidenceCodePath},
		{"no records caps A to C", schema.EvidenceExecutable, nil, schema.EvidenceCodePath},
		{"fabricated id caps A to C", schema.EvidenceExecutable, []string{"E-missing"}, schema.EvidenceCodePath},
		{"never upgrades D", schema.EvidenceSpeculative, []string{"E-check"}, schema.EvidenceSpeculative},
		{"C stays C under check", schema.EvidenceCodePath, []string{"E-check"}, schema.EvidenceCodePath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := capEvidenceLevel(tc.claimed, store, tc.ids); got != tc.want {
				t.Errorf("capEvidenceLevel(%s, %v) = %s, want %s", tc.claimed, tc.ids, got, tc.want)
			}
		})
	}
}

// TestApplyGatesUsesLineOverlapTolerance proves the changed-line attribution gate
// reads cfg.Review.LineOverlapTolerance instead of a hardcoded tolerance.
func TestApplyGatesUsesLineOverlapTolerance(t *testing.T) {
	snap, rel := testSnapshot(t)
	// Hunk on line 1 only; the candidate sits two lines below it (line 3).
	changed := map[string][]repo.LineRange{rel: {{Start: 1, End: 1}}}
	vc := mkVerified(mkCandidate(rel, 3, 3, schema.CategoryCorrectness, "fix it"), true, schema.EvidenceCodePath, true)

	// tol = 0: exact overlap only -> suppressed as outside_change.
	cfg := config.Defaults()
	cfg.Review.LineOverlapTolerance = 0
	stats := &schema.SuppressionStats{ByReason: map[string]int{}}
	if got := applyGates([]verifiedCandidate{vc}, snap, evidence.NewStore(snap.ID()), changed, nil, cfg, schema.ReviewPolicy{}, stats); len(got) != 0 {
		t.Fatalf("tol=0 should suppress a finding two lines outside the hunk, %d survived", len(got))
	}
	if stats.ByReason["outside_change"] != 1 {
		t.Errorf("tol=0 expected outside_change=1, got %+v", stats.ByReason)
	}

	// tol = 2: the same finding is within tolerance and survives.
	cfg.Review.LineOverlapTolerance = 2
	stats = &schema.SuppressionStats{ByReason: map[string]int{}}
	if got := applyGates([]verifiedCandidate{vc}, snap, evidence.NewStore(snap.ID()), changed, nil, cfg, schema.ReviewPolicy{}, stats); len(got) != 1 {
		t.Fatalf("tol=2 should keep a finding within tolerance, got %d (suppressed %+v)", len(got), stats.ByReason)
	}
}

// TestApplyGatesAcceptsDeletionFindings proves a finding attributed to a deleted
// file survives on path identity (its lines live only in the base) instead of
// being dropped for failing the head-existence and changed-range gates.
func TestApplyGatesAcceptsDeletionFindings(t *testing.T) {
	snap, _ := testSnapshot(t)
	cfg := config.Defaults()
	evid := evidence.NewStore(snap.ID())
	// A finding on a file that no longer exists in the head snapshot.
	vc := mkVerified(mkCandidate("removed/api.go", 10, 12, schema.CategorySecurity, "restore the auth check"),
		true, schema.EvidenceCodePath, true)

	// Without deletion context the head-existence gate drops it.
	stats := &schema.SuppressionStats{ByReason: map[string]int{}}
	if got := applyGates([]verifiedCandidate{vc}, snap, evid, nil, nil, cfg, schema.ReviewPolicy{}, stats); len(got) != 0 {
		t.Fatalf("a finding on a non-existent path should be suppressed without deletion context, %d survived", len(got))
	}
	if stats.ByReason["invalid_location"] != 1 {
		t.Errorf("expected invalid_location suppression, got %+v", stats.ByReason)
	}

	// Marked as a deletion (50-line base), an in-range finding is accepted.
	stats = &schema.SuppressionStats{ByReason: map[string]int{}}
	deleted := map[string]int{"removed/api.go": 50}
	if got := applyGates([]verifiedCandidate{vc}, snap, evid, nil, deleted, cfg, schema.ReviewPolicy{}, stats); len(got) != 1 {
		t.Fatalf("deletion finding should survive, got %d (suppressed %+v)", len(got), stats.ByReason)
	}

	// A deletion finding pointing past the end of the removed file is suppressed.
	stats = &schema.SuppressionStats{ByReason: map[string]int{}}
	past := mkVerified(mkCandidate("removed/api.go", 999999, 999999, schema.CategorySecurity, "restore the check"),
		true, schema.EvidenceCodePath, true)
	if got := applyGates([]verifiedCandidate{past}, snap, evid, nil, deleted, cfg, schema.ReviewPolicy{}, stats); len(got) != 0 {
		t.Fatalf("a deletion finding past the base EOF must be suppressed, %d survived", len(got))
	}
}

func TestWithinChange(t *testing.T) {
	r := []repo.LineRange{{Start: 10, End: 12}}
	cases := []struct {
		name       string
		ranges     []repo.LineRange
		start, end int
		tol        int
		want       bool
	}{
		{"empty ranges pass", nil, 5, 6, 3, true},
		{"zero start passes", r, 0, 0, 3, true},
		{"inside hunk", r, 11, 11, 3, true},
		{"within tolerance below", r, 8, 9, 3, true},
		{"far below outside", r, 1, 1, 3, false},
		{"far above outside", r, 16, 20, 3, false},
		// tol=0 requires exact overlap: the adjacent line 9 no longer passes.
		{"zero tol drops adjacent", r, 9, 9, 0, false},
		{"zero tol keeps exact overlap", r, 10, 10, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := withinChange(tc.ranges, tc.start, tc.end, tc.tol); got != tc.want {
				t.Errorf("withinChange(%v, %d, %d, %d) = %v, want %v", tc.ranges, tc.start, tc.end, tc.tol, got, tc.want)
			}
		})
	}
}

// TestDedupeCandidates proves the content fingerprint merges the SAME defect even
// when reported far apart, and keeps DISTINCT defects on the same line separate —
// the opposite of the old line-bucket key.
func TestDedupeCandidates(t *testing.T) {
	// Identical trigger/claim reported 39 lines apart -> one defect, merged.
	a1 := mkCandidate("a.go", 1, 1, schema.CategoryCorrectness, "fix", "E1")
	a2 := mkCandidate("a.go", 40, 40, schema.CategoryCorrectness, "fix", "E2")
	a2.Severity = schema.SeverityCritical // higher severity should win on merge
	// A genuinely different defect on the same line as a1 -> kept separate.
	b := mkCandidate("a.go", 1, 1, schema.CategoryCorrectness, "fix", "E3")
	b.Trigger = "an entirely different trigger condition"
	b.Claim = "a different claim about unrelated behavior"

	out := dedupeCandidates([]candidateFinding{a1, a2, b, {Title: "", Claim: ""}})
	if len(out) != 2 {
		t.Fatalf("expected 2 deduped (identical merged across lines, distinct kept), got %d", len(out))
	}
	merged := out[0] // a1 with a2 merged in
	if merged.Severity != schema.SeverityCritical {
		t.Errorf("merge should take the higher severity, got %s", merged.Severity)
	}
	if len(merged.EvidenceIDs) != 2 {
		t.Errorf("merge should union evidence ids, got %v", merged.EvidenceIDs)
	}
	if len(out[1].EvidenceIDs) != 1 || out[1].EvidenceIDs[0] != "E3" {
		t.Errorf("distinct defect should stay separate with its own evidence, got %+v", out[1].EvidenceIDs)
	}
}

func TestResolveRefsDropsUnknown(t *testing.T) {
	store := evidence.NewStore("snap")
	store.Add(schema.EvidenceRecord{ID: "E-1", Kind: schema.EvidenceKindFile, Path: "main.go", Summary: "x"})
	refs := resolveRefs(store, []string{"E-1", "E-missing"})
	if len(refs) != 1 || refs[0].ID != "E-1" || refs[0].Path != "main.go" {
		t.Fatalf("expected only the resolved ref, got %+v", refs)
	}
	if resolveRefs(nil, []string{"E-1"}) != nil {
		t.Errorf("nil store should resolve to nil refs")
	}
}

func TestRenderEvidenceForVerifierFlagsMissing(t *testing.T) {
	store := evidence.NewStore("snap")
	store.Add(schema.EvidenceRecord{ID: "E-1", Kind: schema.EvidenceKindFile, Path: "main.go", StartLine: 1, EndLine: 2, Summary: "real record"})
	cands := []candidateFinding{
		mkCandidate("main.go", 1, 2, schema.CategoryCorrectness, "fix", "E-1", "E-missing"),
	}
	out := renderEvidenceForVerifier(store, cands)
	for _, want := range []string{"E-1", "main.go:1-2", "real record", "NOT FOUND", "E-missing"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderEvidenceForVerifier output missing %q:\n%s", want, out)
		}
	}
	noCites := []candidateFinding{mkCandidate("main.go", 1, 2, schema.CategoryCorrectness, "fix")}
	if !strings.Contains(renderEvidenceForVerifier(store, noCites), "No evidence cited") {
		t.Error("a catalog with no cited evidence should say so")
	}
}
