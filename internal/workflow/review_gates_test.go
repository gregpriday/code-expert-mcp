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
			survivors := applyGates([]verifiedCandidate{tc.vc}, snap, evid, changed, cfg, tc.policy, stats)
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

// TestApplyGatesBlockingCapDemotes confirms blocking findings beyond the cap are
// demoted (not dropped), and the total cap drops the overflow with a reason.
func TestApplyGatesBlockingCapDemotes(t *testing.T) {
	snap, rel := testSnapshot(t)
	cfg := config.Defaults()
	changed := map[string][]repo.LineRange{rel: {{Start: 1, End: 5}}}
	evid := evidence.NewStore(snap.ID())
	stats := &schema.SuppressionStats{ByReason: map[string]int{}}

	var in []verifiedCandidate
	for i := 0; i < 4; i++ {
		in = append(in, mkVerified(mkCandidate(rel, 1+i, 1+i, schema.CategoryCorrectness, "fix it"), true, schema.EvidenceCodePath, true))
	}
	policy := schema.ReviewPolicy{MaxBlockingFindings: 2, MaxTotalFindings: 3}
	survivors := applyGates(in, snap, evid, changed, cfg, policy, stats)
	if len(survivors) != 3 {
		t.Fatalf("expected 3 survivors after total cap, got %d", len(survivors))
	}
	blocking := 0
	for _, s := range survivors {
		if s.v.Blocking {
			blocking++
		}
	}
	if blocking != 2 {
		t.Errorf("expected 2 blocking after demotion, got %d", blocking)
	}
	if stats.ByReason["over_total_limit"] != 1 {
		t.Errorf("expected 1 over_total_limit suppression, got %+v", stats.ByReason)
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

func TestWithinChange(t *testing.T) {
	r := []repo.LineRange{{Start: 10, End: 12}}
	cases := []struct {
		name       string
		ranges     []repo.LineRange
		start, end int
		want       bool
	}{
		{"empty ranges pass", nil, 5, 6, true},
		{"zero start passes", r, 0, 0, true},
		{"inside hunk", r, 11, 11, true},
		{"within tolerance below", r, 8, 9, true},
		{"far below outside", r, 1, 1, false},
		{"far above outside", r, 16, 20, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := withinChange(tc.ranges, tc.start, tc.end); got != tc.want {
				t.Errorf("withinChange(%v, %d, %d) = %v, want %v", tc.ranges, tc.start, tc.end, got, tc.want)
			}
		})
	}
}

func TestDedupeCandidates(t *testing.T) {
	in := []candidateFinding{
		mkCandidate("a.go", 1, 1, schema.CategoryCorrectness, "fix", "E1"),
		mkCandidate("a.go", 2, 2, schema.CategoryCorrectness, "fix", "E2"), // same path/bucket/category -> merge
		mkCandidate("a.go", 5, 5, schema.CategoryCorrectness, "fix", "E3"), // line 5 -> different bucket
		{Title: "", Claim: ""}, // empty -> skipped
	}
	in[1].Severity = schema.SeverityCritical // higher severity should win on merge
	out := dedupeCandidates(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 deduped candidates, got %d", len(out))
	}
	merged := out[0]
	if merged.Severity != schema.SeverityCritical {
		t.Errorf("merge should take the higher severity, got %s", merged.Severity)
	}
	if len(merged.EvidenceIDs) != 2 {
		t.Errorf("merge should union evidence ids, got %v", merged.EvidenceIDs)
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

func TestRenderEvidenceCatalogFlagsMissing(t *testing.T) {
	store := evidence.NewStore("snap")
	store.Add(schema.EvidenceRecord{ID: "E-1", Kind: schema.EvidenceKindFile, Path: "main.go", StartLine: 1, EndLine: 2, Summary: "real record"})
	cands := []candidateFinding{
		mkCandidate("main.go", 1, 2, schema.CategoryCorrectness, "fix", "E-1", "E-missing"),
	}
	out := renderEvidenceCatalog(store, cands)
	for _, want := range []string{"E-1", "main.go:1-2", "real record", "NOT FOUND", "E-missing"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderEvidenceCatalog output missing %q:\n%s", want, out)
		}
	}
	noCites := []candidateFinding{mkCandidate("main.go", 1, 2, schema.CategoryCorrectness, "fix")}
	if !strings.Contains(renderEvidenceCatalog(store, noCites), "No evidence cited") {
		t.Error("a catalog with no cited evidence should say so")
	}
}
