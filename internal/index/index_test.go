package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/repo"
)

// buildSnapshot writes the given files into a fresh temp dir and freezes a
// read-only snapshot over it. The dir is not a Git repo, so BuildSnapshot uses
// the filesystem walk; every text file is ingested.
func buildSnapshot(t *testing.T, files map[string]string) *repo.Snapshot {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
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
	return snap
}

const alphaGo = "package main\n\n// preceding line\nfunc Alpha() int {\n\treturn 1 // marker token\n}\n\nfunc helper() {}\n"
const betaGo = "package main\n\nfunc Beta() int {\n\treturn 2\n}\n"
const notesTxt = "alpha line one\nBeta line two\nmarker token also here\n"

func searchFiles() map[string]string {
	return map[string]string{
		"alpha.go":  alphaGo,
		"beta.go":   betaGo,
		"notes.txt": notesTxt,
	}
}

func TestLexicalSearchLiteral(t *testing.T) {
	snap := buildSnapshot(t, searchFiles())
	e := NewLexicalEngine(snap, 100)
	hits, err := e.Search(context.Background(), SearchQuery{Pattern: "marker token", Mode: ModeLiteral})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("expected the literal to match in both alpha.go and notes.txt, got %d hits: %+v", len(hits), hits)
	}
	for _, h := range hits {
		if h.Path == "" || h.Line <= 0 || h.Match == "" || h.FileHash == "" {
			t.Errorf("hit missing fields: %+v", h)
		}
	}
}

func TestLexicalSearchWordBoundary(t *testing.T) {
	snap := buildSnapshot(t, searchFiles())
	e := NewLexicalEngine(snap, 100)

	// Whole-word "Alpha" matches the identifier.
	whole, err := e.Search(context.Background(), SearchQuery{Pattern: "Alpha", Mode: ModeWord})
	if err != nil {
		t.Fatalf("Search word: %v", err)
	}
	if len(whole) == 0 {
		t.Error("word search for 'Alpha' should match")
	}

	// A sub-token must NOT match in word mode (it lacks word boundaries inside
	// "Alpha"), but DOES match as a literal substring.
	subWord, err := e.Search(context.Background(), SearchQuery{Pattern: "lpha", Mode: ModeWord})
	if err != nil {
		t.Fatalf("Search sub word: %v", err)
	}
	if len(subWord) != 0 {
		t.Errorf("word search for 'lpha' should not match inside 'Alpha', got %+v", subWord)
	}
	subLit, err := e.Search(context.Background(), SearchQuery{Pattern: "lpha", Mode: ModeLiteral})
	if err != nil {
		t.Fatalf("Search sub literal: %v", err)
	}
	if len(subLit) == 0 {
		t.Error("literal search for 'lpha' should match the substring inside 'Alpha'")
	}
}

func TestLexicalSearchRegex(t *testing.T) {
	snap := buildSnapshot(t, searchFiles())
	e := NewLexicalEngine(snap, 100)
	hits, err := e.Search(context.Background(), SearchQuery{Pattern: `func \w+\(\) int`, Mode: ModeRegex})
	if err != nil {
		t.Fatalf("Search regex: %v", err)
	}
	if len(hits) < 2 {
		t.Errorf("regex should match Alpha and Beta declarations, got %d: %+v", len(hits), hits)
	}
}

func TestLexicalSearchCaseInsensitive(t *testing.T) {
	snap := buildSnapshot(t, searchFiles())
	e := NewLexicalEngine(snap, 100)
	hits, err := e.Search(context.Background(), SearchQuery{Pattern: "ALPHA", Mode: ModeLiteral, CaseInsensitive: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Error("case-insensitive search for 'ALPHA' should match 'alpha'/'Alpha'")
	}
}

func TestLexicalSearchPathMode(t *testing.T) {
	snap := buildSnapshot(t, searchFiles())
	e := NewLexicalEngine(snap, 100)
	hits, err := e.Search(context.Background(), SearchQuery{Pattern: "alpha", Mode: ModePath})
	if err != nil {
		t.Fatalf("Search path: %v", err)
	}
	found := false
	for _, h := range hits {
		if h.Path == "alpha.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("path search for 'alpha' should match alpha.go, got %+v", hits)
	}
}

func TestLexicalSearchIncludeExclude(t *testing.T) {
	snap := buildSnapshot(t, searchFiles())
	e := NewLexicalEngine(snap, 100)

	// Restrict to .go files: notes.txt must be excluded.
	inc, err := e.Search(context.Background(), SearchQuery{Pattern: "marker token", Mode: ModeLiteral, Include: []string{"*.go"}})
	if err != nil {
		t.Fatalf("Search include: %v", err)
	}
	for _, h := range inc {
		if filepath.Ext(h.Path) != ".go" {
			t.Errorf("include *.go leaked a non-go file: %s", h.Path)
		}
	}

	// Exclude .txt files explicitly.
	exc, err := e.Search(context.Background(), SearchQuery{Pattern: "marker token", Mode: ModeLiteral, Exclude: []string{"*.txt"}})
	if err != nil {
		t.Fatalf("Search exclude: %v", err)
	}
	for _, h := range exc {
		if h.Path == "notes.txt" {
			t.Errorf("exclude *.txt should have dropped notes.txt")
		}
	}
}

func TestLexicalSearchMaxResults(t *testing.T) {
	snap := buildSnapshot(t, searchFiles())
	e := NewLexicalEngine(snap, 100)
	hits, err := e.Search(context.Background(), SearchQuery{Pattern: "marker token", Mode: ModeLiteral, MaxResults: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("MaxResults=1 should cap hits to 1, got %d", len(hits))
	}
}

func TestLexicalSearchContextLines(t *testing.T) {
	snap := buildSnapshot(t, searchFiles())
	e := NewLexicalEngine(snap, 100)
	hits, err := e.Search(context.Background(), SearchQuery{
		Pattern: "marker token", Mode: ModeLiteral, Include: []string{"alpha.go"}, ContextLines: 1,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected a hit in alpha.go")
	}
	h := hits[0]
	if len(h.ContextBefore) == 0 || len(h.ContextAfter) == 0 {
		t.Errorf("ContextLines=1 should populate before/after context, got before=%v after=%v", h.ContextBefore, h.ContextAfter)
	}
}

func TestLexicalSearchEmptyPattern(t *testing.T) {
	snap := buildSnapshot(t, searchFiles())
	e := NewLexicalEngine(snap, 100)
	if _, err := e.Search(context.Background(), SearchQuery{Pattern: "   ", Mode: ModeLiteral}); err == nil {
		t.Error("empty/whitespace pattern should error")
	}
}

func TestLexicalSearchBadRegex(t *testing.T) {
	snap := buildSnapshot(t, searchFiles())
	e := NewLexicalEngine(snap, 100)
	if _, err := e.Search(context.Background(), SearchQuery{Pattern: "(unclosed", Mode: ModeRegex}); err == nil {
		t.Error("invalid regex should error")
	}
}

func TestLexicalSearchCancelledContext(t *testing.T) {
	snap := buildSnapshot(t, searchFiles())
	e := NewLexicalEngine(snap, 100)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Search(ctx, SearchQuery{Pattern: "marker token", Mode: ModeLiteral}); err == nil {
		t.Error("a cancelled context should abort the search")
	}
}

func TestLexicalSearchRanksNameAndDefinition(t *testing.T) {
	files := map[string]string{
		// Incidental usage only: no name match and no definition line.
		"server/handler.go": "package server\n\n// reads from cache\nfunc h() { cache(); cache() }\n",
		// Name match plus a definition line, same number of matching lines.
		"cache.go": "package cache\n\nfunc cache() {}\n",
	}
	snap := buildSnapshot(t, files)
	e := NewLexicalEngine(snap, 100)
	res, err := e.SearchDetailed(context.Background(), SearchQuery{Pattern: "cache", Mode: ModeLiteral})
	if err != nil {
		t.Fatalf("SearchDetailed: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatal("expected hits")
	}
	if res.Hits[0].Path != "cache.go" {
		t.Errorf("expected cache.go to rank first (name + definition), got %q", res.Hits[0].Path)
	}
	if res.FilesMatched != 2 {
		t.Errorf("FilesMatched = %d, want 2", res.FilesMatched)
	}
	if res.Truncated {
		t.Error("Truncated should be false when all matches fit the limit")
	}
}

func TestLexicalSearchDetailedTruncation(t *testing.T) {
	files := map[string]string{
		"a.go": "package p\nvar x = token\n",
		"b.go": "package p\nvar y = token\n",
		"c.go": "package p\nvar z = token\n",
	}
	snap := buildSnapshot(t, files)
	e := NewLexicalEngine(snap, 100)
	res, err := e.SearchDetailed(context.Background(), SearchQuery{Pattern: "token", Mode: ModeLiteral, MaxResults: 2})
	if err != nil {
		t.Fatalf("SearchDetailed: %v", err)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("expected 2 hits (capped), got %d", len(res.Hits))
	}
	if res.FilesMatched != 3 {
		t.Errorf("FilesMatched = %d, want 3", res.FilesMatched)
	}
	if !res.Truncated {
		t.Error("expected Truncated=true when matched files exceed the limit")
	}
}

func TestSearchPerFileCapTruncated(t *testing.T) {
	// A single file with more matches than the per-file cap (20) must report
	// Truncated, not imply completeness.
	content := "package p\n"
	for i := 0; i < 30; i++ {
		content += "var needle = 1\n"
	}
	snap := buildSnapshot(t, map[string]string{"f.go": content})
	e := NewLexicalEngine(snap, 100)
	res, err := e.SearchDetailed(context.Background(), SearchQuery{Pattern: "needle", Mode: ModeLiteral, MaxResults: 100})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Hits) != 20 {
		t.Errorf("expected 20 hits (per-file cap), got %d", len(res.Hits))
	}
	if !res.Truncated {
		t.Error("expected Truncated=true when a file hits the per-file cap")
	}
}

func TestSearchPathsRanking(t *testing.T) {
	files := map[string]string{
		"cache.go":         "package p\n", // basename prefix match (strong)
		"x/oldcache.go":    "package p\n", // basename substring match
		"cachelib/util.go": "package p\n", // full-path substring match only
		"unrelated.go":     "package p\n", // no match
	}
	snap := buildSnapshot(t, files)
	e := NewLexicalEngine(snap, 100)
	res, err := e.SearchDetailed(context.Background(), SearchQuery{Pattern: "cache", Mode: ModePath})
	if err != nil {
		t.Fatalf("SearchDetailed: %v", err)
	}
	want := []string{"cache.go", "x/oldcache.go", "cachelib/util.go"}
	if len(res.Hits) != len(want) {
		t.Fatalf("got %d path hits, want %d: %+v", len(res.Hits), len(want), res.Hits)
	}
	for i, w := range want {
		if res.Hits[i].Path != w {
			t.Errorf("path rank %d = %q, want %q", i, res.Hits[i].Path, w)
		}
	}
	if res.FilesMatched != 3 {
		t.Errorf("FilesMatched = %d, want 3", res.FilesMatched)
	}
}

func TestFindSymbolGo(t *testing.T) {
	snap := buildSnapshot(t, searchFiles())
	si := NewSymbolIndex(snap)
	syms, err := si.FindSymbol(context.Background(), "Alpha", "", 100)
	if err != nil {
		t.Fatalf("FindSymbol: %v", err)
	}
	if len(syms) == 0 {
		t.Fatal("expected to find the Alpha symbol")
	}
	got := syms[0]
	if got.Name != "Alpha" || got.Kind != "func" || got.Path != "alpha.go" {
		t.Errorf("unexpected symbol: %+v", got)
	}
	if got.Heuristic {
		t.Error("Go symbols come from the AST parser and must not be flagged heuristic")
	}

	// Kind filter that does not match yields nothing.
	none, err := si.FindSymbol(context.Background(), "Alpha", "type", 100)
	if err != nil {
		t.Fatalf("FindSymbol with kind: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("kind=type should not match a func, got %+v", none)
	}
}

func TestSymbolsInFileGeneric(t *testing.T) {
	snap := buildSnapshot(t, map[string]string{
		"script.py": "def my_func():\n    pass\n\nclass MyClass:\n    pass\n",
	})
	si := NewSymbolIndex(snap)
	syms, err := si.SymbolsInFile(context.Background(), "script.py")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}
	names := map[string]string{}
	for _, s := range syms {
		names[s.Name] = s.Kind
		if !s.Heuristic {
			t.Errorf("non-Go symbols must be flagged heuristic: %+v", s)
		}
	}
	if names["my_func"] != "def" {
		t.Errorf("expected a 'def my_func', got %+v", syms)
	}
	if names["MyClass"] != "class" {
		t.Errorf("expected a 'class MyClass', got %+v", syms)
	}
}

func TestRelatedTests(t *testing.T) {
	snap := buildSnapshot(t, map[string]string{
		"foo.go":       "package foo\n\nfunc Foo() {}\n",
		"foo_test.go":  "package foo\n\nimport \"testing\"\n\nfunc TestFoo(t *testing.T) {}\n",
		"bar_test.go":  "package foo\n\nimport \"testing\"\n\n// references foo by name\nfunc TestBar(t *testing.T) { _ = \"foo\" }\n",
		"unrelated.go": "package foo\n",
	})
	tests := RelatedTests(context.Background(), snap, "foo.go")
	if len(tests) == 0 {
		t.Fatal("expected related test files for foo.go")
	}
	set := map[string]bool{}
	for _, p := range tests {
		set[p] = true
	}
	if !set["foo_test.go"] {
		t.Errorf("convention sibling foo_test.go should be detected, got %v", tests)
	}
	// The content-reference scan should pick up bar_test.go mentioning the stem.
	if !set["bar_test.go"] {
		t.Errorf("content-referencing bar_test.go should be detected, got %v", tests)
	}
}
