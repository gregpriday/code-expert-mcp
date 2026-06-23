package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitRepoWithHistory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	// Commit 1: a.go + b.go together.
	write("a.go", "package a\n")
	write("b.go", "package b\n")
	run("add", ".")
	run("commit", "-q", "-m", "c1")
	// Commit 2: a.go + c.go together.
	write("a.go", "package a // v2\n")
	write("c.go", "package c\n")
	run("add", ".")
	run("commit", "-q", "-m", "c2")
	return dir
}

func TestCoChangedFiles(t *testing.T) {
	if !Available() {
		t.Skip("git not available")
	}
	c, err := New(gitRepoWithHistory(t))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	cos, err := c.CoChangedFiles(context.Background(), "a.go", 10)
	if err != nil {
		t.Fatalf("co-changed: %v", err)
	}
	got := map[string]int{}
	for _, co := range cos {
		got[co.Path] = co.Count
		if co.Path == "a.go" {
			t.Error("the target path must not appear in its own co-changed list")
		}
	}
	if got["b.go"] != 1 || got["c.go"] != 1 {
		t.Errorf("expected b.go and c.go to each co-occur once with a.go, got %v", got)
	}
}
