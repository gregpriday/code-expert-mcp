package repo

import (
	"os"
	"path/filepath"

	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/security"
)

// ResolveRoot canonicalizes and validates a repository root. When allowedRoots
// is non-empty (e.g. MCP client roots), the resolved root must lie within one of
// them. Symlinks in the root path are evaluated so containment checks are
// time-of-use safe.
func ResolveRoot(input string, followSymlinks bool, allowedRoots []string) (security.Root, error) {
	if input == "" {
		return security.Root{}, schema.NewError(schema.CodeRootNotFound, "no root provided")
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return security.Root{}, schema.NewError(schema.CodeRootNotFound, "cannot resolve %q: %v", input, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return security.Root{}, schema.NewError(schema.CodeRootNotFound, "root %q does not exist", abs)
	}
	if !info.IsDir() {
		return security.Root{}, schema.NewError(schema.CodeRootNotFound, "root %q is not a directory", abs)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return security.Root{}, schema.NewError(schema.CodeRootSymlinkEscape, "cannot evaluate symlinks for %q: %v", abs, err)
	}
	canonical = filepath.Clean(canonical)

	if len(allowedRoots) > 0 {
		ok := false
		for _, ar := range allowedRoots {
			arCanon, err := filepath.EvalSymlinks(ar)
			if err != nil {
				continue
			}
			r := security.Root{Canonical: filepath.Clean(arCanon)}
			if r.ContainsPath(canonical) {
				ok = true
				break
			}
		}
		if !ok {
			return security.Root{}, schema.NewError(schema.CodeRootOutsideAllowed,
				"root %q is outside the client-permitted roots", canonical)
		}
	}

	return security.Root{Canonical: canonical, FollowSymlinks: followSymlinks}, nil
}

// DefaultRoot picks a root from the first non-empty of: explicit, the
// CLAUDE_PROJECT_DIR environment variable, or the process working directory.
func DefaultRoot(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv("CLAUDE_PROJECT_DIR"); v != "" {
		return v
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}
