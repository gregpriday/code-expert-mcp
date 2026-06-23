package evidence

import (
	"context"
	"sync"

	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// Validator checks that a citation points at real, in-snapshot content. It is
// the deterministic gate that runs before any path/line is published.
type Validator struct {
	snap *repo.Snapshot
	mu   sync.Mutex
	// lineCounts caches per-path line counts to avoid re-reading files.
	lineCounts map[string]int
}

// NewValidator builds a Validator over a snapshot.
func NewValidator(snap *repo.Snapshot) *Validator {
	return &Validator{snap: snap, lineCounts: map[string]int{}}
}

// FileExists reports whether a path is in the snapshot.
func (v *Validator) FileExists(path string) bool {
	_, ok := v.snap.Stat(path)
	return ok
}

// ValidateLocation verifies a path exists and the line range is within bounds.
// A zero start/end means "whole file" and only checks existence.
func (v *Validator) ValidateLocation(ctx context.Context, path string, start, end int) error {
	if path == "" {
		return schema.NewError(schema.CodeOutputInvalid, "citation has no path")
	}
	if _, ok := v.snap.Stat(path); !ok {
		return schema.NewError(schema.CodeOutputInvalid, "cited path %q is not in the snapshot", path)
	}
	if start == 0 && end == 0 {
		return nil
	}
	if start < 0 || end < 0 || (end > 0 && end < start) {
		return schema.NewError(schema.CodeOutputInvalid, "cited range %d-%d for %q is malformed", start, end, path)
	}
	lines, err := v.lineCount(ctx, path)
	if err != nil {
		return nil // unreadable for counting; treat existence as sufficient
	}
	// Allow a 1-line tolerance for trailing-newline edge cases.
	if start > lines+1 || end > lines+1 {
		return schema.NewError(schema.CodeOutputInvalid,
			"cited range %d-%d for %q exceeds file length %d", start, end, path, lines)
	}
	return nil
}

func (v *Validator) lineCount(ctx context.Context, path string) (int, error) {
	v.mu.Lock()
	if n, ok := v.lineCounts[path]; ok {
		v.mu.Unlock()
		return n, nil
	}
	v.mu.Unlock()
	fc, err := v.snap.ReadFile(ctx, path)
	if err != nil {
		return 0, err
	}
	v.mu.Lock()
	v.lineCounts[path] = fc.Lines
	v.mu.Unlock()
	return fc.Lines, nil
}
