package git

import (
	"context"
	"sort"
	"strings"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// ShowBlob returns the bytes of path at the given revision (e.g. a commit SHA or
// "HEAD"). It returns CE_GIT_REF_INVALID if the object does not exist.
func (c *Client) ShowBlob(ctx context.Context, rev, path string) ([]byte, error) {
	out, err := c.run(ctx, "show", rev+":"+path)
	if err != nil {
		if _, ok := err.(*commandError); ok {
			return nil, schema.NewError(schema.CodeGitRefInvalid, "object %s:%s not found", rev, path)
		}
		return nil, err
	}
	return out, nil
}

// BlobExists reports whether path exists at rev.
func (c *Client) BlobExists(ctx context.Context, rev, path string) bool {
	_, err := c.runText(ctx, "cat-file", "-e", rev+":"+path)
	return err == nil
}

// LsTree lists the files present at a revision (repo-relative, forward slash).
func (c *Client) LsTree(ctx context.Context, rev string) ([]string, error) {
	out, err := c.run(ctx, "ls-tree", "-r", "--name-only", "-z", rev)
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// LogPaths returns up to limit recent commit subjects touching a path. Commit
// messages are untrusted and labeled as such by callers.
func (c *Client) LogPaths(ctx context.Context, path string, limit int) ([]CommitInfo, error) {
	args := []string{"log", "--no-merges", "--format=%H%x1f%an%x1f%s", "-n"}
	args = append(args, itoa(limit))
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var commits []CommitInfo
	for _, line := range splitLines(string(out)) {
		parts := splitUnit(line)
		if len(parts) == 3 {
			commits = append(commits, CommitInfo{SHA: parts[0], Author: parts[1], Subject: parts[2]})
		}
	}
	return commits, nil
}

// CommitInfo is a minimal commit record.
type CommitInfo struct {
	SHA     string
	Author  string
	Subject string
}

// CoChange is a file that historically changed in the same commits as a target
// path, with its co-occurrence count.
type CoChange struct {
	Path  string
	Count int
}

// CoChangedFiles returns files that changed together with path across up to limit
// recent commits that touched path, ranked by co-occurrence count. It is a
// heuristic coupling signal for the "updated one side but not the other" defect
// class. All operations are read-only (log + show).
func (c *Client) CoChangedFiles(ctx context.Context, path string, limit int) ([]CoChange, error) {
	commits, err := c.LogPaths(ctx, path, limit)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, ci := range commits {
		files, ferr := c.commitFiles(ctx, ci.SHA)
		if ferr != nil {
			continue
		}
		for _, f := range files {
			if f == "" || f == path {
				continue
			}
			counts[f]++
		}
	}
	out := make([]CoChange, 0, len(counts))
	for p, n := range counts {
		out = append(out, CoChange{Path: p, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// commitFiles lists the files changed in a single (non-merge) commit.
func (c *Client) commitFiles(ctx context.Context, sha string) ([]string, error) {
	out, err := c.run(ctx, "show", "--no-merges", "--name-only", "--format=", sha)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range splitLines(string(out)) {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}
