package security

import "testing"

func TestCleanRelativeRejectsEscapes(t *testing.T) {
	bad := []string{
		"", "..", "../etc/passwd", "/etc/passwd", "a/../../b",
		"C:\\Windows\\System32", "\\\\server\\share", "//host/path",
		"foo/../../bar",
	}
	for _, p := range bad {
		if _, ok := CleanRelative(p); ok {
			t.Errorf("CleanRelative(%q) should be rejected", p)
		}
	}
	good := map[string]string{
		"a/b/c.go":      "a/b/c.go",
		"./a/b.go":      "a/b.go",
		"a/./b.go":      "a/b.go",
		"internal/x.go": "internal/x.go",
	}
	for in, want := range good {
		got, ok := CleanRelative(in)
		if !ok || got != want {
			t.Errorf("CleanRelative(%q) = %q,%v; want %q,true", in, got, ok, want)
		}
	}
}

func TestRootContainment(t *testing.T) {
	r := Root{Canonical: "/work/project"}
	if !r.ContainsPath("/work/project/a/b.go") {
		t.Error("should contain nested path")
	}
	if r.ContainsPath("/work/project-evil/x") {
		t.Error("must not treat sibling prefix as contained")
	}
	if r.ContainsPath("/etc/passwd") {
		t.Error("must not contain outside path")
	}
	if abs, ok := r.JoinWithin("../escape"); ok {
		t.Errorf("JoinWithin should reject escape, got %q", abs)
	}
}

func TestRedactSecrets(t *testing.T) {
	in := "api_key=AKIAIOSFODNN7EXAMPLE and token: sk-abcdefghijklmnop1234"
	out := Redact(in)
	if out == in {
		t.Error("expected redaction")
	}
}
