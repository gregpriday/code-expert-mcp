package repo

import "testing"

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
