package mcpserver

import "testing"

// TestSubtleCompare locks the constant-time bearer-token comparison: it must
// report equality/inequality correctly without short-circuiting on length.
func TestSubtleCompare(t *testing.T) {
	cases := []struct {
		a, b   string
		differ bool
	}{
		{"s3cret-token", "s3cret-token", false},
		{"", "", false},
		{"s3cret-token", "s3cret-toker", true},
		{"s3cret-token", "short", true}, // differing lengths still compare as "differ"
		{"a", "", true},
	}
	for _, c := range cases {
		if got := subtleCompare(c.a, c.b); got != c.differ {
			t.Errorf("subtleCompare(%q, %q) = %v, want differ=%v", c.a, c.b, got, c.differ)
		}
	}
}
