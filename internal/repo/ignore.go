package repo

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ignoreMatcher applies a simplified gitignore-style pattern set. It is used for
// the optional .codeexpertignore file and as a fallback in non-Git folders. It
// supports glob patterns, directory-anchored patterns, and "!" negation. It is
// intentionally simpler than full gitignore semantics.
type ignoreMatcher struct {
	rules []ignoreRule
}

type ignoreRule struct {
	pattern string
	negate  bool
	dirOnly bool
}

// defaultIgnoreDirs are always skipped during filesystem walks.
var defaultIgnoreDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true, "vendor": true,
	".idea": true, ".vscode": true, "dist": true, "build": true,
	"__pycache__": true, ".mypy_cache": true, ".pytest_cache": true, "target": true,
}

func loadIgnoreFile(root, name string) *ignoreMatcher {
	if name == "" {
		return &ignoreMatcher{}
	}
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return &ignoreMatcher{}
	}
	return parseIgnore(string(data))
}

func parseIgnore(content string) *ignoreMatcher {
	m := &ignoreMatcher{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		r := ignoreRule{pattern: trimmed}
		if strings.HasPrefix(r.pattern, "!") {
			r.negate = true
			r.pattern = r.pattern[1:]
		}
		if strings.HasSuffix(r.pattern, "/") {
			r.dirOnly = true
			r.pattern = strings.TrimSuffix(r.pattern, "/")
		}
		r.pattern = strings.TrimPrefix(r.pattern, "/")
		m.rules = append(m.rules, r)
	}
	return m
}

// Match reports whether a repo-relative forward-slash path is ignored.
func (m *ignoreMatcher) Match(rel string, isDir bool) bool {
	if m == nil || len(m.rules) == 0 {
		return false
	}
	ignored := false
	base := path.Base(rel)
	for _, r := range m.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if matchRule(r.pattern, rel, base) {
			ignored = !r.negate
		}
	}
	return ignored
}

func matchRule(pattern, rel, base string) bool {
	if !strings.Contains(pattern, "/") {
		// Basename match for unanchored patterns.
		if ok, _ := doublestar.Match(pattern, base); ok {
			return true
		}
	}
	if ok, _ := doublestar.Match(pattern, rel); ok {
		return true
	}
	// Allow a leading-directory pattern to match nested paths.
	if ok, _ := doublestar.Match(pattern+"/**", rel); ok {
		return true
	}
	return false
}
