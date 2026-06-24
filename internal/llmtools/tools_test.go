package llmtools

import (
	"context"
	"encoding/json"
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

// execJSON runs a tool call and decodes the JSON result into a generic map.
func execJSON(t *testing.T, reg *Registry, name, args string) map[string]any {
	t.Helper()
	out := reg.Execute(context.Background(), provider.ToolCall{Name: name, Arguments: args})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("%s result is not valid JSON %q: %v", name, out, err)
	}
	return m
}

func TestHandleManifest(t *testing.T) {
	reg, _ := testRegistry(t)
	m := execJSON(t, reg, "repo_get_manifest", "{}")
	if _, ok := m["snapshot_id"].(string); !ok || m["snapshot_id"] == "" {
		t.Errorf("manifest missing snapshot_id: %v", m)
	}
	if _, ok := m["repository"]; !ok {
		t.Errorf("manifest missing repository brief: %v", m)
	}
}

func TestHandleReadValidPath(t *testing.T) {
	reg, evid := testRegistry(t)
	m := execJSON(t, reg, "repo_read", `{"path":"main.go"}`)
	if m["error"] != nil {
		t.Fatalf("repo_read on a valid path returned an error: %v", m["error"])
	}
	content, _ := m["content"].(string)
	if !strings.Contains(content, "func Foo") {
		t.Errorf("repo_read content missing the file body, got %q", content)
	}
	evID, _ := m["evidence_id"].(string)
	if evID == "" {
		t.Error("repo_read must return an evidence_id")
	}
	if !strings.Contains(toString(m["note"]), "UNTRUSTED") {
		t.Errorf("repo_read must carry the untrusted-data note, got %v", m["note"])
	}
	// The cited evidence must actually exist in the store.
	if !evid.Has(evID) {
		t.Errorf("evidence_id %q from repo_read is not in the store", evID)
	}
}

func TestHandleReadMissingPath(t *testing.T) {
	reg, _ := testRegistry(t)
	m := execJSON(t, reg, "repo_read", `{"path":"does-not-exist.go"}`)
	if m["error"] == nil {
		t.Errorf("repo_read on a missing path must return an error payload, got %v", m)
	}
}

func TestHandleFindSymbol(t *testing.T) {
	reg, _ := testRegistry(t)
	m := execJSON(t, reg, "repo_find_symbol", `{"name":"Foo"}`)
	if m["error"] != nil {
		t.Fatalf("repo_find_symbol returned an error: %v", m["error"])
	}
	syms, ok := m["symbols"].([]any)
	if !ok || len(syms) == 0 {
		t.Fatalf("repo_find_symbol should find Foo, got %v", m["symbols"])
	}
	first, _ := syms[0].(map[string]any)
	if first["name"] != "Foo" {
		t.Errorf("expected symbol Foo, got %v", first["name"])
	}
	if first["kind"] != "func" {
		t.Errorf("expected kind func for Foo, got %v", first["kind"])
	}
}

func TestExecuteUnknownTool(t *testing.T) {
	reg, _ := testRegistry(t)
	m := execJSON(t, reg, "repo_nonexistent", "{}")
	if m["error"] == nil {
		t.Errorf("unknown tool must return an error payload, got %v", m)
	}
}

// TestRepoSearchEmitsMarkdownSection confirms the Markdown section breadcrumb
// reaches the model through the repo_search tool output, not just the engine.
func TestRepoSearchEmitsMarkdownSection(t *testing.T) {
	dir := t.TempDir()
	md := "# Guide\n\n## Caching\n\nthe cache is fast here\n"
	if err := os.WriteFile(filepath.Join(dir, "guide.md"), []byte(md), 0o644); err != nil {
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
	reg := New(Options{Snapshot: snap, Evidence: evidence.NewStore(snap.ID()), Budget: budget.New(budget.Limits{}), Config: cfg})
	m := execJSON(t, reg, "repo_search", `{"query":"fast"}`)
	hits, ok := m["hits"].([]any)
	if !ok || len(hits) == 0 {
		t.Fatalf("expected hits, got %v", m["hits"])
	}
	h0, _ := hits[0].(map[string]any)
	if h0["section"] != "Caching" {
		t.Errorf("repo_search should emit section %q, got %v", "Caching", h0["section"])
	}
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}
