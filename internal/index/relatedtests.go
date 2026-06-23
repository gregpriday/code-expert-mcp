package index

import (
	"context"
	"path"
	"strings"

	"github.com/gregpriday/codeexpert/internal/repo"
)

// RelatedTests returns likely test files for a source path using naming
// conventions and lightweight content references. Results are heuristic.
func RelatedTests(ctx context.Context, snap *repo.Snapshot, srcPath string) []string {
	dir := path.Dir(srcPath)
	base := path.Base(srcPath)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	candidates := map[string]bool{}
	add := func(p string) {
		if _, ok := snap.Stat(p); ok {
			candidates[p] = true
		}
	}

	// Convention-based siblings.
	add(path.Join(dir, stem+"_test"+ext))
	add(path.Join(dir, stem+".test"+ext))
	add(path.Join(dir, stem+".spec"+ext))
	add(path.Join(dir, "test_"+stem+ext))
	add(path.Join(dir, "__tests__", stem+".test"+ext))
	add(path.Join(dir, "__tests__", stem+".spec"+ext))

	// Content reference scan across known test files mentioning the stem.
	for _, fm := range snap.ListFiles() {
		if !isTestFile(fm.Path) || candidates[fm.Path] {
			continue
		}
		fc, err := snap.ReadFile(ctx, fm.Path)
		if err != nil || fc.Meta.Binary {
			continue
		}
		if strings.Contains(string(fc.Bytes), stem) {
			candidates[fm.Path] = true
		}
	}

	out := make([]string, 0, len(candidates))
	for p := range candidates {
		out = append(out, p)
	}
	return out
}

func isTestFile(p string) bool {
	lower := strings.ToLower(p)
	base := strings.ToLower(path.Base(p))
	return strings.HasSuffix(base, "_test.go") ||
		strings.HasPrefix(base, "test_") ||
		strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "/__tests__/") || strings.Contains(lower, "/spec/")
}
