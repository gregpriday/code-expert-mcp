// Package security holds the deterministic safety controls that must not depend
// on prompt text: filesystem containment, symlink policy, secret redaction, and
// child-process environment allowlisting.
package security

import (
	"path/filepath"
	"strings"
)

// Root represents a validated, canonical repository root that all reads are
// confined to.
type Root struct {
	// Canonical is the fully resolved (symlinks evaluated) absolute path.
	Canonical string
	// FollowSymlinks controls whether symlinks inside the root may be traversed.
	FollowSymlinks bool
}

// ContainsPath reports whether an absolute, already-cleaned path lies inside the
// root. The comparison is lexical; callers that need symlink-safety must resolve
// the path first with ResolveWithin.
func (r Root) ContainsPath(abs string) bool {
	abs = filepath.Clean(abs)
	if abs == r.Canonical {
		return true
	}
	prefix := r.Canonical + string(filepath.Separator)
	return strings.HasPrefix(abs, prefix)
}

// RelTo returns the forward-slash repository-relative form of abs, or an empty
// string and false if abs is outside the root.
func (r Root) RelTo(abs string) (string, bool) {
	abs = filepath.Clean(abs)
	if !r.ContainsPath(abs) {
		return "", false
	}
	rel, err := filepath.Rel(r.Canonical, abs)
	if err != nil {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// CleanRelative validates a repository-relative path coming from a model or
// tool argument. It rejects absolute paths, parent-directory escapes, and empty
// segments, returning the normalized forward-slash relative path.
func CleanRelative(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	// Reject absolute paths (including Windows drive-letter and UNC forms).
	if filepath.IsAbs(p) {
		return "", false
	}
	if len(p) >= 2 && p[1] == ':' { // C:\... drive letter
		return "", false
	}
	if strings.HasPrefix(p, "\\\\") || strings.HasPrefix(p, "//") {
		return "", false
	}
	slashed := filepath.ToSlash(p)
	// Clean using slash semantics so behavior is identical across platforms.
	cleaned := cleanSlash(slashed)
	if cleaned == "." || cleaned == "" {
		return "", false
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

// cleanSlash is path.Clean over forward slashes without importing path to keep
// platform behavior identical (filepath.Clean would flip separators on Windows).
func cleanSlash(p string) string {
	segs := strings.Split(p, "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		switch s {
		case "", ".":
			continue
		case "..":
			if len(out) > 0 && out[len(out)-1] != ".." {
				out = out[:len(out)-1]
			} else {
				out = append(out, "..")
			}
		default:
			out = append(out, s)
		}
	}
	return strings.Join(out, "/")
}

// JoinWithin resolves a repository-relative path against the root and confirms
// the result stays inside the root. It returns the absolute path or false.
func (r Root) JoinWithin(rel string) (string, bool) {
	clean, ok := CleanRelative(rel)
	if !ok {
		return "", false
	}
	abs := filepath.Join(r.Canonical, filepath.FromSlash(clean))
	if !r.ContainsPath(abs) {
		return "", false
	}
	return abs, true
}
