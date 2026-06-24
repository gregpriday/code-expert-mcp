package checks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
)

func freezeDir(t *testing.T, files map[string]string) (string, *repo.Snapshot) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := repo.ResolveRoot(repo.DefaultRoot(dir), false, nil)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	snap, err := repo.BuildSnapshot(context.Background(), root, config.Defaults().Repository)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return dir, snap
}

func fingerprint(root string) string {
	h := sha256.New()
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		fmt.Fprintf(h, "%s\n", rel)
		if b, rerr := os.ReadFile(p); rerr == nil {
			h.Write(b)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// TestRunnerExecutesInIsolationNoWrite proves a check runs against a throwaway
// copy and produces a result + evidence, while the protected repository tree is
// byte-identical before and after — the no-write invariant holds.
func TestRunnerExecutesInIsolationNoWrite(t *testing.T) {
	dir, snap := freezeDir(t, map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	before := fingerprint(dir)

	cfg := config.ChecksConfig{Command: []config.CheckCommand{
		{Name: "git-version", Argv: []string{"git", "--version"}, Enabled: true, Safe: true},
	}}
	r := NewRunner(snap, cfg, schema.VerifySafe, nil)
	if !r.Enabled() {
		t.Fatal("runner should be enabled for a safe check")
	}
	results, evid, err := r.Run(context.Background(), 30*time.Second)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(results) != 1 || !results[0].Passed || results[0].ExitCode != 0 {
		t.Fatalf("expected one passing check, got %+v", results)
	}
	if len(evid) != 1 || evid[0].Kind != schema.EvidenceKindCheck {
		t.Fatalf("expected one check-evidence record, got %+v", evid)
	}
	if after := fingerprint(dir); after != before {
		t.Fatal("the check runner mutated the protected repository — no-write invariant violated")
	}
}

// TestApplicableByMode pins the mode->commands mapping.
func TestApplicableByMode(t *testing.T) {
	cfg := config.ChecksConfig{Command: []config.CheckCommand{
		{Name: "fmt", Argv: []string{"gofmt", "-l", "."}, Enabled: true, Safe: true},
		{Name: "test", Argv: []string{"go", "test", "./..."}, Enabled: true, Safe: false},
		{Name: "off", Argv: []string{"x"}, Enabled: false, Safe: true},
	}}
	count := func(mode schema.VerificationMode) int {
		return len(NewRunner(nil, cfg, mode, nil).applicable())
	}
	if got := count(schema.VerifyOff); got != 0 {
		t.Errorf("off mode applicable = %d, want 0", got)
	}
	if got := count(schema.VerifySafe); got != 1 {
		t.Errorf("safe mode applicable = %d, want 1 (read-only analyzer only)", got)
	}
	if got := count(schema.VerifyConfigured); got != 2 {
		t.Errorf("configured mode applicable = %d, want 2", got)
	}
	if got := count(schema.VerifyDeep); got != 2 {
		t.Errorf("deep mode applicable = %d, want 2", got)
	}
}

// TestRunOneRejectsPathExecutable proves an executable with a path separator is
// refused, so a binary inside the repo can never be invoked.
func TestRunOneRejectsPathExecutable(t *testing.T) {
	_, err := runOne(context.Background(), t.TempDir(), nil, config.CheckCommand{Name: "evil", Argv: []string{"./payload"}}, 30*time.Second)
	if err == nil {
		t.Error("an executable with a path separator must be rejected")
	}
}

func TestModeFallsBackToConfig(t *testing.T) {
	cfg := config.ChecksConfig{Mode: "safe", Command: []config.CheckCommand{
		{Name: "fmt", Argv: []string{"gofmt"}, Enabled: true, Safe: true},
	}}
	// An unset request mode falls back to the configured mode (safe).
	if got := len(NewRunner(nil, cfg, "", nil).applicable()); got != 1 {
		t.Errorf("empty mode should fall back to configured safe, applicable = %d, want 1", got)
	}
}
