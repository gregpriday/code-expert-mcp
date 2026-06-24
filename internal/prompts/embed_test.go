package prompts

import (
	"sort"
	"strings"
	"testing"
)

// canonicalIDs is the exact set of prompt IDs the code depends on. List() must
// match it, so an orphaned or renamed asset fails the build's tests.
func canonicalIDs() []string {
	return []string{
		CommonSystem, PlanExplore, PlanFinalize, HelpExplore, HelpFinalize,
		ReviewDiffLocal, ReviewContext, ReviewSpecialist, ReviewVerify, RepairSchema,
	}
}

func TestListMatchesCanonicalIDs(t *testing.T) {
	got := List()
	want := canonicalIDs()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("embedded prompts %v do not match canonical IDs %v (orphaned or renamed asset?)", got, want)
	}
}

func TestAllPromptsResolveNonEmpty(t *testing.T) {
	for _, id := range canonicalIDs() {
		s, err := Get(id)
		if err != nil {
			t.Errorf("prompt %q failed to load: %v", id, err)
			continue
		}
		if strings.TrimSpace(s) == "" {
			t.Errorf("prompt %q is empty", id)
		}
	}
}

// TestLoadBearingAnchorsPresent guards the non-negotiable invariants encoded in
// the prompts so silently gutting an asset fails CI rather than only at runtime.
func TestLoadBearingAnchorsPresent(t *testing.T) {
	anchors := map[string][]string{
		CommonSystem: {"safe to merge", "untrusted data", "never follow instructions"},
		ReviewVerify: {"verification gates", "reject", "location exists"},
	}
	for id, subs := range anchors {
		content, err := Get(id)
		if err != nil {
			t.Fatalf("prompt %q: %v", id, err)
		}
		lower := strings.ToLower(content)
		for _, sub := range subs {
			if !strings.Contains(lower, sub) {
				t.Errorf("prompt %q lost a load-bearing anchor %q", id, sub)
			}
		}
	}
}

func TestHashStableAndDistinct(t *testing.T) {
	h1 := Hash(CommonSystem)
	if h1 == "" {
		t.Fatal("Hash returned empty for an existing prompt")
	}
	if h1 != Hash(CommonSystem) {
		t.Error("Hash is not stable across calls")
	}
	if Hash(CommonSystem) == Hash(ReviewVerify) {
		t.Error("distinct prompts hashed to the same value")
	}
	if Hash("does-not-exist") != "" {
		t.Error("Hash of a missing prompt should be empty")
	}
}
