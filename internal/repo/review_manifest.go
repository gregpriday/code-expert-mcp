package repo

import (
	"context"
	"strconv"
	"strings"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/repo/git"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// ReviewSnapshot is the frozen base/head view pair for a review target.
type ReviewSnapshot struct {
	snap     *Snapshot
	manifest *ChangeManifest

	baseRev        string // git revision for base, or "" for the empty tree
	headRev        string // git revision for head, or "" meaning the working tree
	headIsWorktree bool
	headIsIndex    bool
	baseIsIndex    bool
}

// Manifest returns the frozen change manifest.
func (r *ReviewSnapshot) Manifest() *ChangeManifest { return r.manifest }

// Base returns the underlying repository snapshot.
func (r *ReviewSnapshot) Base() *Snapshot { return r.snap }

// SnapshotRef builds the schema reference for outputs.
func (r *ReviewSnapshot) SnapshotRef() schema.ReviewSnapshot {
	return schema.ReviewSnapshot{
		SnapshotID: r.snap.ID(),
		Root:       r.snap.Root(),
		Target:     r.manifest.Target,
		BaseSHA:    r.manifest.BaseSHA,
		HeadSHA:    r.manifest.HeadSHA,
		BaseLabel:  r.manifest.BaseLabel,
		HeadLabel:  r.manifest.HeadLabel,
	}
}

// ReadHead returns the head-view content of a changed file.
func (r *ReviewSnapshot) ReadHead(ctx context.Context, path string) (FileContent, error) {
	if r.headIsWorktree {
		return r.snap.ReadFile(ctx, path)
	}
	rev := r.headRev
	if r.headIsIndex {
		rev = ""
	}
	data, err := r.snap.git.ShowBlob(ctx, blobRev(rev), path)
	if err != nil {
		return FileContent{}, err
	}
	meta, _ := r.snap.Stat(path)
	meta.Path = path
	// Git blob bytes are not bounded by MaxFileBytes here; the int32 offset table
	// built by newFileContent is safe because review targets hold ordinary source
	// objects, far below the ~2 GiB int32 limit.
	return newFileContent(meta, data), nil
}

// ReadBase returns the base-view bytes of a path, or an error if absent.
func (r *ReviewSnapshot) ReadBase(ctx context.Context, path string) ([]byte, error) {
	if r.baseRev == "" && !r.baseIsIndex {
		return nil, schema.NewError(schema.CodeGitRefInvalid, "no base content (added file or empty base)")
	}
	return r.snap.git.ShowBlob(ctx, blobRev(r.baseRev), path)
}

func blobRev(rev string) string {
	if rev == "" {
		return "" // index reference (":path") handled by caller convention
	}
	return rev
}

// BuildReviewSnapshot freezes the change set described by target. It requires a
// Git repository.
func BuildReviewSnapshot(ctx context.Context, snap *Snapshot, target schema.ReviewTarget, cfg config.RepositoryConfig) (*ReviewSnapshot, error) {
	if !snap.isGit {
		return nil, schema.NewError(schema.CodeNotGitRepository, "review requires a Git repository")
	}
	gc := snap.git
	rs := &ReviewSnapshot{snap: snap}
	manifest := &ChangeManifest{Target: string(target.Type)}

	var spec git.DiffSpec
	switch target.Type {
	case schema.TargetWorkingTree, "":
		manifest.Target = string(schema.TargetWorkingTree)
		rs.baseRev = snap.head
		rs.headIsWorktree = true
		manifest.BaseSHA, manifest.HeadSHA = snap.head, ""
		manifest.BaseLabel, manifest.HeadLabel = "HEAD", "working tree"
		manifest.IncludesWT = true
		spec = git.DiffSpec{Args: []string{"HEAD"}}
	case schema.TargetStaged:
		rs.baseRev = snap.head
		rs.headIsIndex = true
		manifest.BaseSHA, manifest.HeadSHA = snap.head, "index"
		manifest.BaseLabel, manifest.HeadLabel = "HEAD", "index"
		spec = git.DiffSpec{Args: []string{"--cached"}}
	case schema.TargetUnstaged:
		rs.baseIsIndex = true
		rs.headIsWorktree = true
		manifest.BaseSHA, manifest.HeadSHA = "index", ""
		manifest.BaseLabel, manifest.HeadLabel = "index", "working tree"
		manifest.IncludesWT = true
		spec = git.DiffSpec{Args: nil}
	case schema.TargetRange:
		base, err := gc.ResolveRef(ctx, orDefault(target.BaseRef, "HEAD~1"))
		if err != nil {
			return nil, err
		}
		head, err := gc.ResolveRef(ctx, orDefault(target.HeadRef, "HEAD"))
		if err != nil {
			return nil, err
		}
		rs.baseRev, rs.headRev = base, head
		manifest.BaseSHA, manifest.HeadSHA = base, head
		manifest.BaseLabel, manifest.HeadLabel = orDefault(target.BaseRef, base), orDefault(target.HeadRef, head)
		spec = git.DiffSpec{Args: []string{base, head}}
	case schema.TargetCommit:
		if target.Commit == "" {
			return nil, schema.NewError(schema.CodeInvalidArgument, "commit target requires a commit")
		}
		head, err := gc.ResolveRef(ctx, target.Commit)
		if err != nil {
			return nil, err
		}
		base, berr := gc.ResolveRef(ctx, target.Commit+"^")
		rs.headRev = head
		manifest.HeadSHA, manifest.HeadLabel = head, target.Commit
		if berr != nil {
			// Root commit: diff against the empty tree.
			rs.baseRev = ""
			manifest.BaseSHA, manifest.BaseLabel = "", "(root)"
			spec = git.DiffSpec{Args: []string{emptyTreeHash, head}}
		} else {
			rs.baseRev = base
			manifest.BaseSHA, manifest.BaseLabel = base, target.Commit+"^"
			spec = git.DiffSpec{Args: []string{base, head}}
		}
	case schema.TargetMergeBase:
		upstream := orDefault(target.UpstreamRef, "origin/HEAD")
		head := orDefault(target.HeadRef, "HEAD")
		headSHA, err := gc.ResolveRef(ctx, head)
		if err != nil {
			return nil, err
		}
		mb, err := gc.MergeBase(ctx, upstream, head)
		if err != nil {
			return nil, err
		}
		rs.baseRev, rs.headRev = mb, headSHA
		manifest.BaseSHA, manifest.HeadSHA = mb, headSHA
		manifest.BaseLabel, manifest.HeadLabel = "merge-base("+upstream+")", head
		spec = git.DiffSpec{Args: []string{mb, headSHA}}
	default:
		return nil, schema.NewError(schema.CodeInvalidArgument, "unknown review target type %q", target.Type)
	}

	changed, err := gc.ChangedFiles(ctx, spec)
	if err != nil {
		return nil, err
	}

	includeUntracked := manifest.IncludesWT && (target.IncludeUntracked == nil || *target.IncludeUntracked) && cfg.IncludeUntracked
	if includeUntracked {
		untracked, uerr := gc.LsFilesUntracked(ctx)
		if uerr == nil {
			for _, p := range untracked {
				changed = append(changed, git.ChangedFile{Path: p, Status: "A"})
			}
		}
	}

	cls := newClassifier(cfg.VendorGlobs, cfg.GeneratedGlobs)
	for _, cf := range changed {
		out := ChangedFile{
			Path:      cf.Path,
			OldPath:   cf.OldPath,
			Status:    cf.Status,
			Added:     cf.Added,
			Deleted:   cf.Deleted,
			Binary:    cf.Binary,
			Generated: cls.isGenerated(cf.Path),
			Vendored:  cls.isVendored(cf.Path),
			Language:  DetectLanguage(cf.Path, nil),
		}
		// Freeze the unified diff text for tracked changes.
		if cf.Status != "?" {
			if diff, derr := gc.UnifiedDiff(ctx, spec, cf.Path, 3); derr == nil {
				out.Diff = diff
				out.NewRanges = parseNewRanges(diff)
			}
		}
		manifest.TotalAdded += out.Added
		manifest.TotalDeleted += out.Deleted
		manifest.Files = append(manifest.Files, out)
	}

	rs.manifest = manifest
	return rs, nil
}

// emptyTreeHash is Git's well-known empty tree object, used for root-commit diffs.
const emptyTreeHash = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// parseNewRanges extracts contiguous added/changed line ranges in the head view
// from a unified diff, for changed-line coverage and evidence anchoring.
func parseNewRanges(diff string) []LineRange {
	var ranges []LineRange
	var newLine int
	inHunk := false
	var cur *LineRange
	flush := func() {
		if cur != nil {
			ranges = append(ranges, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "@@") {
			flush()
			inHunk = true
			newLine = parseHunkNewStart(line)
			continue
		}
		if !inHunk {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+++"):
			// file header inside diff; ignore
		case strings.HasPrefix(line, "+"):
			if cur == nil {
				cur = &LineRange{Start: newLine, End: newLine}
			} else {
				cur.End = newLine
			}
			newLine++
		case strings.HasPrefix(line, "-"):
			// deletion: does not advance the new-file line counter
		default:
			// context line
			flush()
			newLine++
		}
	}
	flush()
	return ranges
}

// parseHunkNewStart parses the new-file start line from "@@ -a,b +c,d @@".
func parseHunkNewStart(hunk string) int {
	plus := strings.Index(hunk, "+")
	if plus < 0 {
		return 1
	}
	rest := hunk[plus+1:]
	end := strings.IndexAny(rest, " ,")
	if end >= 0 {
		rest = rest[:end]
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 1
	}
	return n
}
