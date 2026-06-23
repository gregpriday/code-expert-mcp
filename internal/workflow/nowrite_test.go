package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// fingerprintTree hashes every file under dir (including .git) plus `git status`,
// so any repository or Git-state mutation changes the fingerprint.
func fingerprintTree(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		sum := sha256.Sum256(data)
		lines = append(lines, rel+":"+hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)

	status := exec.Command("git", "status", "--porcelain=v2", "--branch")
	status.Dir = dir
	out, _ := status.CombinedOutput()

	combined := strings.Join(lines, "\n") + "\n---status---\n" + string(out)
	sum := sha256.Sum256([]byte(combined))
	return hex.EncodeToString(sum[:])
}

// TestNoWriteInvariant is release-blocking: a full plan + review run must not
// modify the repository or any Git state.
func TestNoWriteInvariant(t *testing.T) {
	dir, rel := tempGitRepo(t)
	// Dirty the working tree so review has a diff and there is uncommitted state.
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("package main\n\nfunc main() {\n\tvar x *int\n\t_ = *x\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Add an untracked file too.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	before := fingerprintTree(t, dir)

	eng := newTestEngine(rel)
	if _, err := eng.Plan(context.Background(), schema.PlanRequest{
		Root: dir, Instructions: "Investigate the nil dereference in main.go", Mode: schema.PlanModePlan,
	}, RunOptions{}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := eng.Review(context.Background(), schema.ReviewRequest{
		Root: dir, Target: schema.ReviewTarget{Type: schema.TargetWorkingTree},
	}, RunOptions{}); err != nil {
		t.Fatalf("review: %v", err)
	}

	after := fingerprintTree(t, dir)
	if before != after {
		t.Fatal("REPOSITORY MUTATED during analysis: the read-only invariant was violated")
	}
}
