package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/repo"
)

func TestEnrichEnclosingSymbols(t *testing.T) {
	snap, rel := testSnapshot(t) // main.go: func main() spans lines 3-5
	m := &repo.ChangeManifest{Files: []repo.ChangedFile{
		{Path: rel, Status: "M", NewRanges: []repo.LineRange{{Start: 4, End: 4}}},
	}}
	enrichEnclosingSymbols(context.Background(), snap, m)
	if len(m.Files[0].Symbols) == 0 {
		t.Fatal("expected enclosing symbols for a change inside func main")
	}
	found := false
	for _, s := range m.Files[0].Symbols {
		if strings.Contains(s, "main") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'main' among enclosing symbols, got %v", m.Files[0].Symbols)
	}
}

func TestEnrichEnclosingSymbolsSkipsWhenNoRanges(t *testing.T) {
	snap, rel := testSnapshot(t)
	m := &repo.ChangeManifest{Files: []repo.ChangedFile{{Path: rel, Status: "M"}}}
	enrichEnclosingSymbols(context.Background(), snap, m)
	if len(m.Files[0].Symbols) != 0 {
		t.Errorf("no NewRanges should yield no enclosing symbols, got %v", m.Files[0].Symbols)
	}
}

func TestSymbolOverlapsRanges(t *testing.T) {
	r := []repo.LineRange{{Start: 3, End: 5}}
	if !symbolOverlapsRanges(3, 5, r) {
		t.Error("exact overlap should be true")
	}
	if !symbolOverlapsRanges(1, 4, r) {
		t.Error("partial overlap should be true")
	}
	if symbolOverlapsRanges(10, 12, r) {
		t.Error("disjoint ranges should be false")
	}
}

func TestIsDependencyOrBuildFile(t *testing.T) {
	yes := []string{"go.mod", "go.sum", "internal/foo/go.mod", "dockerfile", "makefile", "package-lock.json", "cargo.toml"}
	no := []string{"main.go", "internal/foo.go", "readme.md", "config.yaml"}
	for _, p := range yes {
		if !isDependencyOrBuildFile(strings.ToLower(p)) {
			t.Errorf("%q should be classified as a dependency/build file", p)
		}
	}
	for _, p := range no {
		if isDependencyOrBuildFile(strings.ToLower(p)) {
			t.Errorf("%q should not be classified as a dependency/build file", p)
		}
	}
}

func TestBuildRiskMapFlagsDependencyChange(t *testing.T) {
	m := &repo.ChangeManifest{Files: []repo.ChangedFile{{Path: "go.mod", Status: "M"}}}
	areas := buildRiskMap(m)
	var compat *bool
	for _, a := range areas {
		if a.Category == "compatibility" {
			v := true
			compat = &v
		}
	}
	if compat == nil {
		t.Errorf("a go.mod change should raise a compatibility risk area, got %+v", areas)
	}
}
