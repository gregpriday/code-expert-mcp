package workflow

import (
	"reflect"
	"testing"

	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// These tests pin buildRiskMap's deterministic output so the declarative
// rule-table refactor stays behavior-preserving.

func manifestOf(paths ...string) *repo.ChangeManifest {
	files := make([]repo.ChangedFile, 0, len(paths))
	for _, p := range paths {
		files = append(files, repo.ChangedFile{Path: p, Status: "M"})
	}
	return &repo.ChangeManifest{Files: files}
}

func areaByCategory(areas []schema.RiskArea) map[schema.FindingCategory]schema.RiskArea {
	out := map[schema.FindingCategory]schema.RiskArea{}
	for _, a := range areas {
		out[a.Category] = a
	}
	return out
}

func TestBuildRiskMapCategoriesAndPriorities(t *testing.T) {
	m := manifestOf(
		"internal/auth/token.go",   // security (auth, token) +3
		"internal/store/cache.go",  // data_integrity (store, cache) +2
		"internal/worker/queue.go", // concurrency (worker, queue) +2
		"internal/api/handler.go",  // compatibility (api, handler) +1
		"go.mod",                   // compatibility (dep/build) +2
	)
	byCat := areaByCategory(buildRiskMap(m))

	cases := []struct {
		cat      schema.FindingCategory
		priority int
		paths    []string
	}{
		{schema.CategorySecurity, 3, []string{"internal/auth/token.go"}},
		{schema.CategoryDataIntegrity, 2, []string{"internal/store/cache.go"}},
		{schema.CategoryConcurrency, 2, []string{"internal/worker/queue.go"}},
		// Compatibility is raised twice: the keyword rule (+1, api/handler.go) and
		// the dependency/build rule (+2, go.mod). Both must accumulate.
		{schema.CategoryCompatibility, 3, []string{"go.mod", "internal/api/handler.go"}},
		// Every changed file gets baseline correctness attention.
		{schema.CategoryCorrectness, 5, []string{
			"go.mod", "internal/api/handler.go", "internal/auth/token.go",
			"internal/store/cache.go", "internal/worker/queue.go",
		}},
	}
	for _, c := range cases {
		a, ok := byCat[c.cat]
		if !ok {
			t.Errorf("%s: expected a risk area, got none", c.cat)
			continue
		}
		if a.Priority != c.priority {
			t.Errorf("%s: priority = %d, want %d", c.cat, a.Priority, c.priority)
		}
		if !reflect.DeepEqual(a.Paths, c.paths) {
			t.Errorf("%s: paths = %v, want %v", c.cat, a.Paths, c.paths)
		}
		if a.Rationale != riskRationale(c.cat) {
			t.Errorf("%s: rationale = %q, want %q", c.cat, a.Rationale, riskRationale(c.cat))
		}
	}
}

func TestBuildRiskMapTriggersEachKeywordCategory(t *testing.T) {
	cases := []struct {
		path string
		cat  schema.FindingCategory
	}{
		{"internal/parse/unmarshal.go", schema.CategorySecurity},
		{"internal/db/migrate.go", schema.CategoryDataIntegrity},
		{"internal/sync/mutex.go", schema.CategoryConcurrency},
		{"internal/route/endpoint.go", schema.CategoryCompatibility},
		{"internal/render/list.go", schema.CategoryPerformance},
		{"internal/retry/timeout.go", schema.CategoryReliability},
		{"internal/foo/foo_test.go", schema.CategoryTesting},
	}
	for _, c := range cases {
		byCat := areaByCategory(buildRiskMap(manifestOf(c.path)))
		if _, ok := byCat[c.cat]; !ok {
			t.Errorf("%q should raise %s, got %+v", c.path, c.cat, byCat)
		}
		if _, ok := byCat[schema.CategoryCorrectness]; !ok {
			t.Errorf("%q should always raise baseline correctness", c.path)
		}
	}
}

func TestBuildRiskMapPathTruncationAndSort(t *testing.T) {
	// 9 files, each only raising baseline correctness; paths must sort and cap at 8.
	m := manifestOf(
		"i.go", "h.go", "g.go", "f.go", "e.go", "d.go", "c.go", "b.go", "a.go",
	)
	byCat := areaByCategory(buildRiskMap(m))
	corr := byCat[schema.CategoryCorrectness]
	if len(corr.Paths) != 8 {
		t.Fatalf("paths capped at 8, got %d (%v)", len(corr.Paths), corr.Paths)
	}
	want := []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "g.go", "h.go"}
	if !reflect.DeepEqual(corr.Paths, want) {
		t.Errorf("paths = %v, want sorted first 8 %v", corr.Paths, want)
	}
	if corr.Priority != 9 {
		t.Errorf("priority accumulates across all files: got %d, want 9", corr.Priority)
	}
}

func TestBuildRiskMapDeterministic(t *testing.T) {
	m := manifestOf("internal/auth/token.go", "internal/api/handler.go", "go.mod")
	first := buildRiskMap(m)
	for i := 0; i < 3; i++ {
		if got := buildRiskMap(m); !reflect.DeepEqual(got, first) {
			t.Fatalf("buildRiskMap is not deterministic across calls\nfirst: %+v\n got: %+v", first, got)
		}
	}
}

func TestBuildRiskMapEmpty(t *testing.T) {
	if areas := buildRiskMap(manifestOf()); len(areas) != 0 {
		t.Errorf("empty manifest should yield no risk areas, got %+v", areas)
	}
}
