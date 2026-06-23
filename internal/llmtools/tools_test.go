package llmtools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/budget"
	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/evidence"
	"github.com/gregpriday/codeexpert/internal/provider"
	"github.com/gregpriday/codeexpert/internal/repo"
)

func testRegistry(t *testing.T) (*Registry, *evidence.Store) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc Foo() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	root, err := repo.ResolveRoot(repo.DefaultRoot(dir), cfg.Repository.FollowSymlinks, nil)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	snap, err := repo.BuildSnapshot(context.Background(), root, cfg.Repository)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	evid := evidence.NewStore(snap.ID())
	reg := New(Options{Snapshot: snap, Evidence: evid, Budget: budget.New(budget.Limits{}), Config: cfg})
	return reg, evid
}

// TestToolSurfaceIsReadOnly is a guard on the non-negotiable read-only invariant:
// no registered function tool may be a write/exec/mutation tool.
func TestToolSurfaceIsReadOnly(t *testing.T) {
	reg, _ := testRegistry(t)
	banned := []string{"write", "exec", "delete", "remove", "commit", "checkout", "reset", "rebase", "merge", "mkdir", "chmod", "apply", "patch", "move", "rename"}
	for _, name := range reg.Names() {
		lower := strings.ToLower(name)
		for _, b := range banned {
			if strings.Contains(lower, b) {
				t.Errorf("tool %q looks like a mutation tool (matched %q); the tool surface must be read-only", name, b)
			}
		}
	}
}

// TestToolResultsAreLabeledUntrusted confirms repository content returned to the
// model carries the untrusted-data label and that minted evidence is flagged
// untrusted — the code-enforced half of the instruction-hierarchy defense.
func TestToolResultsAreLabeledUntrusted(t *testing.T) {
	reg, evid := testRegistry(t)
	out := reg.Execute(context.Background(), provider.ToolCall{Name: "repo_search", Arguments: `{"query":"package"}`})
	if !strings.Contains(out, untrustedNote) {
		t.Errorf("repo_search result missing the untrusted-data note:\n%s", out)
	}
	recs := evid.All()
	if len(recs) == 0 {
		t.Fatal("expected repo_search to mint at least one evidence record")
	}
	for _, r := range recs {
		if !r.Provenance.Untrusted {
			t.Errorf("evidence %s is not flagged untrusted: %+v", r.ID, r.Provenance)
		}
	}
}
