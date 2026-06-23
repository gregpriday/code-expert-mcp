package llmtools

import (
	"context"
	"encoding/json"

	"github.com/gregpriday/codeexpert/internal/schema"
)

func (r *Registry) registerReview() {
	r.register(tool{
		name:        "repo_git_diff",
		description: "Return the frozen unified diff for the review. With a path, returns that file's hunks and head line ranges; without a path, lists all changed files with stats.",
		parameters: objSchema(map[string]any{
			"path": strProp("Optional changed-file path to fetch hunks for."),
		}),
		handler: r.handleGitDiff,
	})
	r.register(tool{
		name:        "repo_read_head",
		description: "Read the head-view (post-change) content of a changed file in the frozen review, with line numbers and an evidence_id.",
		parameters: objSchema(map[string]any{
			"path":       strProp("Changed-file path."),
			"start_line": intProp("1-based start line (default 1)."),
			"end_line":   intProp("1-based inclusive end line (default: start+120)."),
		}, "path"),
		handler: r.handleReadHead,
	})
}

func (r *Registry) handleGitDiff(ctx context.Context, raw json.RawMessage) (any, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	manifest := r.review.Manifest()
	if p.Path == "" {
		files := make([]map[string]any, 0, len(manifest.Files))
		for _, f := range manifest.Files {
			files = append(files, map[string]any{
				"path": f.Path, "status": f.Status, "added": f.Added, "deleted": f.Deleted,
				"binary": f.Binary, "generated": f.Generated, "vendored": f.Vendored, "language": f.Language,
			})
		}
		return map[string]any{
			"snapshot_id": r.snap.ID(), "target": manifest.Target,
			"base": manifest.BaseLabel, "head": manifest.HeadLabel,
			"files": files, "total_added": manifest.TotalAdded, "total_deleted": manifest.TotalDeleted,
		}, nil
	}
	for _, f := range manifest.Files {
		if f.Path == p.Path {
			ev := r.evid.Add(schema.EvidenceRecord{
				Kind: schema.EvidenceKindDiff, Path: f.Path, Summary: "diff " + f.Path,
				Provenance: schema.Provenance{Tool: "repo_git_diff", Untrusted: true},
			})
			return map[string]any{
				"snapshot_id": r.snap.ID(), "path": f.Path, "status": f.Status,
				"diff": f.Diff, "new_ranges": f.NewRanges, "evidence_id": ev.ID, "note": untrustedNote,
			}, nil
		}
	}
	return nil, schema.NewError(schema.CodeInvalidArgument, "%q is not a changed file in this review", p.Path)
}

func (r *Registry) handleReadHead(ctx context.Context, raw json.RawMessage) (any, error) {
	var p readParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	fc, err := r.review.ReadHead(ctx, p.Path)
	if err != nil {
		return nil, err
	}
	start := p.StartLine
	if start <= 0 {
		start = 1
	}
	end := p.EndLine
	if end <= 0 {
		end = start + 120
	}
	content, gs, ge := sliceLines(fc.Bytes, start, end)
	ev := r.evid.Add(schema.EvidenceRecord{
		Kind: schema.EvidenceKindFile, Path: p.Path, StartLine: gs, EndLine: ge,
		BlobHash: fc.Meta.Hash, Summary: "head content " + p.Path,
		Provenance: schema.Provenance{Tool: "repo_read_head", Untrusted: true},
	})
	return map[string]any{
		"snapshot_id": r.snap.ID(), "path": p.Path, "start_line": gs, "end_line": ge,
		"content": content, "evidence_id": ev.ID, "note": untrustedNote,
	}, nil
}
