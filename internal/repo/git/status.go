package git

import (
	"context"
	"strings"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// IsRepository reports whether dir is inside a Git working tree.
func (c *Client) IsRepository(ctx context.Context) bool {
	out, err := c.runText(ctx, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// TopLevel returns the absolute path of the repository root.
func (c *Client) TopLevel(ctx context.Context) (string, error) {
	return c.runText(ctx, "rev-parse", "--show-toplevel")
}

// HeadSHA returns the resolved HEAD commit, or "" if there are no commits yet.
func (c *Client) HeadSHA(ctx context.Context) string {
	out, err := c.runText(ctx, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// CurrentBranch returns the short branch name, or "" if detached.
func (c *Client) CurrentBranch(ctx context.Context) string {
	out, err := c.runText(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// IsDirty reports whether the working tree has staged or unstaged changes.
func (c *Client) IsDirty(ctx context.Context) bool {
	entries, err := c.Status(ctx)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// StatusEntry is one line of porcelain v2 status.
type StatusEntry struct {
	Path      string
	OrigPath  string // for renames/copies
	Staged    byte   // X code
	Unstaged  byte   // Y code
	Untracked bool
	Ignored   bool
}

// Status parses `git status --porcelain=v2 --untracked-files=all`.
func (c *Client) Status(ctx context.Context) ([]StatusEntry, error) {
	out, err := c.run(ctx, "status", "--porcelain=v2", "--untracked-files=all", "-z")
	if err != nil {
		return nil, err
	}
	var entries []StatusEntry
	records := strings.Split(string(out), "\x00")
	for i := 0; i < len(records); i++ {
		rec := records[i]
		if rec == "" {
			continue
		}
		switch rec[0] {
		case '1': // ordinary changed entry: "1 XY sub ... <path>"
			fields := strings.SplitN(rec, " ", 9)
			if len(fields) >= 9 {
				xy := fields[1]
				entries = append(entries, StatusEntry{
					Path:     fields[8],
					Staged:   xy[0],
					Unstaged: xy[1],
				})
			}
		case '2': // renamed/copied: path then origPath in next NUL field
			fields := strings.SplitN(rec, " ", 10)
			if len(fields) >= 10 {
				xy := fields[1]
				path := fields[9]
				orig := ""
				if i+1 < len(records) {
					orig = records[i+1]
					i++
				}
				entries = append(entries, StatusEntry{
					Path:     path,
					OrigPath: orig,
					Staged:   xy[0],
					Unstaged: xy[1],
				})
			}
		case 'u': // unmerged
			fields := strings.SplitN(rec, " ", 11)
			if len(fields) >= 11 {
				entries = append(entries, StatusEntry{Path: fields[10], Staged: 'U', Unstaged: 'U'})
			}
		case '?': // untracked
			entries = append(entries, StatusEntry{Path: strings.TrimSpace(rec[1:]), Untracked: true})
		case '!': // ignored
			entries = append(entries, StatusEntry{Path: strings.TrimSpace(rec[1:]), Ignored: true})
		}
	}
	return entries, nil
}

// LsFiles returns tracked files (repo-relative, forward slash).
func (c *Client) LsFiles(ctx context.Context) ([]string, error) {
	out, err := c.run(ctx, "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// LsFilesUntracked returns untracked, non-ignored files honoring .gitignore.
func (c *Client) LsFilesUntracked(ctx context.Context) ([]string, error) {
	out, err := c.run(ctx, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// CheckIgnore reports whether a path is ignored by Git.
func (c *Client) CheckIgnore(ctx context.Context, path string) (bool, error) {
	_, err := c.runText(ctx, "check-ignore", "-q", "--", path)
	if err == nil {
		return true, nil
	}
	// exit status 1 (not ignored) surfaces as a commandError; treat as not ignored.
	if _, ok := err.(*commandError); ok {
		return false, nil
	}
	return false, err
}

// ResolveRef resolves a ref to a commit SHA, returning a typed error if invalid.
func (c *Client) ResolveRef(ctx context.Context, ref string) (string, error) {
	out, err := c.runText(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil || out == "" {
		return "", schema.NewError(schema.CodeGitRefInvalid, "ref %q could not be resolved", ref)
	}
	return out, nil
}

// MergeBase returns the merge base of two refs.
func (c *Client) MergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := c.runText(ctx, "merge-base", a, b)
	if err != nil {
		return "", schema.NewError(schema.CodeGitRefInvalid, "no merge base for %q and %q", a, b)
	}
	return out, nil
}

func splitNUL(b []byte) []string {
	parts := strings.Split(string(b), "\x00")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
