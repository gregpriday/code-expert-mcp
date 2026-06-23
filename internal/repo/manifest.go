package repo

import (
	"path"
	"sort"
	"strings"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// manifestFiles maps well-known manifest filenames to a build/test system.
var manifestSignatures = map[string]string{
	"go.mod":           "Go modules",
	"package.json":     "npm/Node",
	"requirements.txt": "pip",
	"pyproject.toml":   "Python (PEP 518)",
	"Pipfile":          "pipenv",
	"Cargo.toml":       "Cargo",
	"Gemfile":          "Bundler",
	"pom.xml":          "Maven",
	"build.gradle":     "Gradle",
	"composer.json":    "Composer",
	"Makefile":         "Make",
	"CMakeLists.txt":   "CMake",
	"Dockerfile":       "Docker",
}

// Brief builds the deterministic RepositoryBrief from a snapshot.
func (s *Snapshot) Brief(cfg config.RepositoryConfig) schema.RepositoryBrief {
	b := schema.RepositoryBrief{
		Root:    s.root.Canonical,
		IsGit:   s.isGit,
		Branch:  s.branch,
		HeadSHA: s.head,
		Dirty:   s.dirty,
	}

	langBytes := map[string]int64{}
	langFiles := map[string]int{}
	var totalBytes int64
	manifests := map[string]bool{}
	buildSystems := map[string]bool{}
	srcDirs := map[string]bool{}
	testDirs := map[string]bool{}

	for _, f := range s.files {
		if f.Untracked {
			b.UntrackedFiles++
		} else if f.Tracked {
			b.TrackedFiles++
		}
		base := path.Base(f.Path)
		if sys, ok := manifestSignatures[base]; ok {
			manifests[f.Path] = true
			buildSystems[sys] = true
		}
		if f.Binary || f.Vendored || f.Generated {
			continue
		}
		if f.Language != "" {
			langBytes[f.Language] += f.Size
			langFiles[f.Language]++
			totalBytes += f.Size
		}
		dir := path.Dir(f.Path)
		if isTestPath(f.Path) {
			testDirs[topDir(dir)] = true
		} else if f.Language != "" && !isDocPath(f.Path) {
			srcDirs[topDir(dir)] = true
		}
	}

	for lang, bytes := range langBytes {
		share := 0.0
		if totalBytes > 0 {
			share = float64(bytes) / float64(totalBytes)
		}
		b.Languages = append(b.Languages, schema.LanguageStat{
			Language: lang,
			Files:    langFiles[lang],
			Bytes:    bytes,
			Indexer:  indexerFor(lang),
			Share:    round2(share),
		})
	}
	sort.Slice(b.Languages, func(i, j int) bool {
		if b.Languages[i].Bytes != b.Languages[j].Bytes {
			return b.Languages[i].Bytes > b.Languages[j].Bytes
		}
		return b.Languages[i].Language < b.Languages[j].Language
	})

	b.Manifests = sortedKeys(manifests)
	b.BuildSystems = sortedKeys(buildSystems)
	b.TestSystems = detectTestSystems(buildSystems, langFiles)
	b.SourceDirs = topN(sortedKeys(srcDirs), 12)
	b.TestDirs = topN(sortedKeys(testDirs), 12)
	b.GuidanceFiles = s.guidanceFiles(cfg)
	return b
}

func indexerFor(lang string) string {
	if lang == "Go" {
		return "go/ast (semantic)"
	}
	return "generic (heuristic)"
}

func detectTestSystems(build map[string]bool, langs map[string]int) []string {
	var out []string
	if langs["Go"] > 0 {
		out = append(out, "go test")
	}
	if build["npm/Node"] {
		out = append(out, "node test runner")
	}
	if langs["Python"] > 0 {
		out = append(out, "pytest/unittest")
	}
	if build["Cargo"] {
		out = append(out, "cargo test")
	}
	sort.Strings(out)
	return out
}

func isTestPath(p string) bool {
	lower := strings.ToLower(p)
	base := strings.ToLower(path.Base(p))
	return strings.HasSuffix(base, "_test.go") ||
		strings.HasPrefix(base, "test_") ||
		strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".test.ts") ||
		strings.HasSuffix(base, ".spec.js") || strings.HasSuffix(base, ".spec.ts") ||
		strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "/__tests__/") || strings.Contains(lower, "/spec/")
}

func isDocPath(p string) bool {
	lower := strings.ToLower(p)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".rst") || strings.HasSuffix(lower, ".txt")
}

func topDir(dir string) string {
	if dir == "." || dir == "" {
		return "."
	}
	parts := strings.SplitN(dir, "/", 3)
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func topN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
