package git

import (
	"context"
	"strconv"
	"strings"
)

// DiffSpec holds the trailing arguments that define a diff (e.g. {"HEAD"} or
// {"--cached"} or {"base", "head"}). It never includes paths; pass those to the
// per-file methods.
type DiffSpec struct {
	Args []string
}

// ChangedFile describes one file's change between two views.
type ChangedFile struct {
	Path    string
	OldPath string
	Status  string // A, M, D, R, C, T
	Added   int
	Deleted int
	Binary  bool
}

// ChangedFiles returns the name-status + numstat for a diff spec.
func (c *Client) ChangedFiles(ctx context.Context, spec DiffSpec) ([]ChangedFile, error) {
	nameArgs := append([]string{"diff", "--name-status", "-M", "-z"}, spec.Args...)
	nameOut, err := c.run(ctx, nameArgs...)
	if err != nil {
		return nil, err
	}
	files := parseNameStatus(string(nameOut))

	numArgs := append([]string{"diff", "--numstat", "-M", "-z"}, spec.Args...)
	numOut, err := c.run(ctx, numArgs...)
	if err == nil {
		applyNumstat(files, string(numOut))
	}
	return files, nil
}

func parseNameStatus(s string) []ChangedFile {
	recs := strings.Split(s, "\x00")
	var out []ChangedFile
	for i := 0; i < len(recs); i++ {
		code := recs[i]
		if code == "" {
			continue
		}
		status := string(code[0])
		switch status {
		case "R", "C": // rename/copy: status, then oldpath, then newpath
			if i+2 < len(recs) {
				out = append(out, ChangedFile{Status: status, OldPath: recs[i+1], Path: recs[i+2]})
				i += 2
			}
		default:
			if i+1 < len(recs) {
				out = append(out, ChangedFile{Status: status, Path: recs[i+1]})
				i++
			}
		}
	}
	return out
}

func applyNumstat(files []ChangedFile, s string) {
	byPath := map[string]*ChangedFile{}
	for i := range files {
		byPath[files[i].Path] = &files[i]
	}
	recs := strings.Split(s, "\x00")
	for i := 0; i < len(recs); i++ {
		line := recs[i]
		if line == "" {
			continue
		}
		// "added<TAB>deleted<TAB>path" — for renames path may be empty and the
		// old/new paths follow as separate NUL records.
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		added, deleted, binary := parseCount(parts[0]), parseCount(parts[1]), parts[0] == "-"
		path := parts[2]
		if path == "" && i+2 < len(recs) {
			path = recs[i+2] // new path of a rename
			i += 2
		}
		if cf, ok := byPath[path]; ok {
			cf.Added, cf.Deleted, cf.Binary = added, deleted, binary
		}
	}
}

func parseCount(s string) int {
	if s == "-" {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}

// UnifiedDiff returns the unified diff text for a single path under a spec.
func (c *Client) UnifiedDiff(ctx context.Context, spec DiffSpec, path string, contextLines int) (string, error) {
	args := append([]string{"diff", "-M", "--unified=" + strconv.Itoa(contextLines)}, spec.Args...)
	args = append(args, "--", path)
	out, err := c.run(ctx, args...)
	return string(out), err
}
