package repo

import (
	"strings"
	"testing"
)

// TestTopDir pins topDir's two-level prefix behavior, including the edge cases
// (empty, root-relative, single-component) so the stdlib-based cleanup stays
// behavior-preserving.
func TestTopDir(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "."},
		{".", "."},
		{"internal", "internal"},
		{"internal/repo", "internal/repo"},
		{"internal/repo/git", "internal/repo"},
		{"a/b/c/d", "a/b"},
	}
	for _, c := range cases {
		if got := topDir(c.in); got != c.want {
			t.Errorf("topDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSyntheticAddDiff(t *testing.T) {
	diff, ranges, added := syntheticAddDiff("new/file.go", []byte("package new\n\nfunc F() {}\n"))
	if added != 3 {
		t.Errorf("added = %d, want 3", added)
	}
	if len(ranges) != 1 || ranges[0].Start != 1 || ranges[0].End != 3 {
		t.Errorf("ranges = %+v, want [{1 3}]", ranges)
	}
	for _, want := range []string{"+++ b/new/file.go", "@@ -0,0 +1,3 @@", "+package new", "+func F() {}"} {
		if !strings.Contains(diff, want) {
			t.Errorf("synthetic diff missing %q:\n%s", want, diff)
		}
	}
	// Empty content yields nothing to review.
	if d, r, n := syntheticAddDiff("empty", nil); d != "" || r != nil || n != 0 {
		t.Errorf("empty content should produce no diff, got (%q,%v,%d)", d, r, n)
	}
	// Interior and trailing blank lines are preserved, not collapsed.
	if _, _, n := syntheticAddDiff("f", []byte("a\n\nb\n\n")); n != 4 {
		t.Errorf("trailing/interior blanks: line count = %d, want 4", n)
	}
	// A file with no trailing newline keeps its last line and gets the git marker.
	d, _, n := syntheticAddDiff("f", []byte("a\nb"))
	if n != 2 || !strings.Contains(d, "No newline at end of file") {
		t.Errorf("no-final-newline: n=%d diff=%q", n, d)
	}
}
