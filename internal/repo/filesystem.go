package repo

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gregpriday/codeexpert/internal/config"
)

// enumerateFilesystem walks root and returns candidate repo-relative paths,
// honoring default ignore directories, the optional ignore file, and symlink
// policy. It never follows symlinks unless explicitly enabled.
func enumerateFilesystem(rootDir string, cfg config.RepositoryConfig) ([]string, error) {
	var ignore *ignoreMatcher
	if cfg.RespectIgnoreFile {
		ignore = loadIgnoreFile(rootDir, cfg.IgnoreFile)
	} else {
		ignore = &ignoreMatcher{}
	}
	var paths []string
	walkFn := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries rather than aborting the whole walk.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == rootDir {
			return nil
		}
		rel, rerr := filepath.Rel(rootDir, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			name := d.Name()
			if defaultIgnoreDirs[name] {
				return fs.SkipDir
			}
			if ignore.Match(rel, true) {
				return fs.SkipDir
			}
			return nil
		}

		// Regular files only: skip symlinks, devices, sockets, pipes.
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 && !cfg.FollowSymlinks {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if ignore.Match(rel, false) {
			return nil
		}
		paths = append(paths, rel)
		return nil
	}
	if err := filepath.WalkDir(rootDir, walkFn); err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

// isHidden reports whether any path segment begins with a dot (excluding the
// configured guidance/ignore files that callers explicitly include).
func isHidden(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") && seg != "." {
			return true
		}
	}
	return false
}
