package repo

import (
	"context"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gregpriday/codeexpert/internal/config"
	"github.com/gregpriday/codeexpert/internal/hashutil"
	"github.com/gregpriday/codeexpert/internal/repo/git"
	"github.com/gregpriday/codeexpert/internal/schema"
	"github.com/gregpriday/codeexpert/internal/security"
)

// Snapshot is the immutable, read-only view of a repository at run start.
type Snapshot struct {
	id     string
	root   security.Root
	cfg    config.RepositoryConfig
	git    *git.Client
	isGit  bool
	head   string
	branch string
	dirty  bool

	files     []FileMeta
	byPath    map[string]int
	class     classifier
	ignore    *ignoreMatcher
	untracked map[string]bool
	totalBy   int64
	trunc     bool

	mu            sync.RWMutex
	contents      map[string]FileContent
	contentsBytes int64
}

// ID returns the content-addressed snapshot identifier.
func (s *Snapshot) ID() string { return s.id }

// Root returns the canonical root path.
func (s *Snapshot) Root() string { return s.root.Canonical }

// SecurityRoot exposes the validated root for path checks.
func (s *Snapshot) SecurityRoot() security.Root { return s.root }

// IsGit reports whether the snapshot is backed by a Git repository.
func (s *Snapshot) IsGit() bool { return s.isGit }

// Git returns the read-only git client, or nil for non-Git folders.
func (s *Snapshot) Git() *git.Client { return s.git }

// HeadSHA, Branch, Dirty expose frozen Git state.
func (s *Snapshot) HeadSHA() string { return s.head }
func (s *Snapshot) Branch() string  { return s.branch }
func (s *Snapshot) Dirty() bool     { return s.dirty }

// ListFiles returns the frozen inventory.
// ListFiles returns a copy of the frozen file inventory. It is a copy so callers
// cannot mutate the snapshot's internal ordering — the trigram index relies on it
// (candidate file IDs are indices into this slice, stable across calls).
func (s *Snapshot) ListFiles() []FileMeta {
	out := make([]FileMeta, len(s.files))
	copy(out, s.files)
	return out
}

// Stat returns the metadata for a repo-relative path.
func (s *Snapshot) Stat(path string) (FileMeta, bool) {
	if i, ok := s.byPath[path]; ok {
		return s.files[i], true
	}
	return FileMeta{}, false
}

// Truncated reports whether the snapshot hit the total-bytes limit.
func (s *Snapshot) Truncated() bool { return s.trunc }

// Ref builds the schema-level reference for outputs.
func (s *Snapshot) Ref() schema.SnapshotRef {
	return schema.SnapshotRef{
		SnapshotID: s.id,
		Root:       s.root.Canonical,
		IsGit:      s.isGit,
		HeadSHA:    s.head,
		Branch:     s.branch,
		Dirty:      s.dirty,
		FileCount:  len(s.files),
	}
}

// ReadFile reads a file's content from the frozen working tree, enforcing the
// per-file byte limit and root containment.
func (s *Snapshot) ReadFile(_ context.Context, path string) (FileContent, error) {
	meta, ok := s.Stat(path)
	if !ok {
		return FileContent{}, schema.NewError(schema.CodeInvalidArgument, "file %q is not in the snapshot", path)
	}
	s.mu.RLock()
	fc, cached := s.contents[path]
	s.mu.RUnlock()
	if cached {
		return fc, nil
	}

	abs, ok := s.root.JoinWithin(path)
	if !ok {
		return FileContent{}, schema.NewError(schema.CodeRootSymlinkEscape, "path %q escapes the root", path)
	}
	// Reject symlinks at read time (time-of-use check).
	if li, err := os.Lstat(abs); err == nil && li.Mode()&os.ModeSymlink != 0 && !s.cfg.FollowSymlinks {
		return FileContent{}, schema.NewError(schema.CodeRootSymlinkEscape, "path %q is a symlink", path)
	}
	limit := s.cfg.MaxFileBytes
	data, err := readLimited(abs, limit)
	if err != nil {
		return FileContent{}, schema.NewError(schema.CodeInternal, "read %q: %v", path, err)
	}
	fc = newFileContent(meta, data)

	s.mu.Lock()
	// Re-check for a racing insert by another reader before admitting.
	if existing, ok := s.contents[path]; ok {
		s.mu.Unlock()
		return existing, nil
	}
	if s.contents == nil {
		s.contents = map[string]FileContent{}
	}
	// Admit until the cache reaches the snapshot byte budget; oversized late
	// files are returned uncached rather than growing memory without bound. The
	// charge counts content bytes only (the small per-line offset table is not
	// accounted), so the cap is an approximate ceiling, not an exact one.
	// Charge by the normalized content actually retained (fc.Bytes), not the raw
	// read length, so the budget reflects what is really held in memory.
	cap := s.cfg.MaxTotalSnapshotBytes
	if cap <= 0 || s.contentsBytes+int64(len(fc.Bytes)) <= cap {
		s.contents[path] = fc
		s.contentsBytes += int64(len(fc.Bytes))
	}
	s.mu.Unlock()
	return fc, nil
}

// BuildSnapshot freezes the repository at root into an immutable Snapshot. It
// performs no writes.
func BuildSnapshot(ctx context.Context, root security.Root, cfg config.RepositoryConfig) (*Snapshot, error) {
	s := &Snapshot{
		root:     root,
		cfg:      cfg,
		byPath:   map[string]int{},
		class:    newClassifier(cfg.VendorGlobs, cfg.GeneratedGlobs),
		contents: map[string]FileContent{},
	}
	if cfg.RespectIgnoreFile {
		s.ignore = loadIgnoreFile(root.Canonical, cfg.IgnoreFile)
	} else {
		s.ignore = &ignoreMatcher{}
	}

	var paths []string
	if git.Available() {
		gc, err := git.New(root.Canonical)
		if err == nil && gc.IsRepository(ctx) {
			s.isGit = true
			s.git = gc
			s.head = gc.HeadSHA(ctx)
			s.branch = gc.CurrentBranch(ctx)
			s.dirty = gc.IsDirty(ctx)
			paths, s.untracked, err = gitInventory(ctx, gc, cfg)
			if err != nil {
				return nil, err
			}
		}
	}
	if !s.isGit {
		var err error
		paths, err = enumerateFilesystem(root.Canonical, cfg)
		if err != nil {
			return nil, schema.NewError(schema.CodeInternal, "filesystem walk: %v", err)
		}
	}

	if err := s.ingest(ctx, paths); err != nil {
		return nil, err
	}
	s.computeID()
	return s, nil
}

func gitInventory(ctx context.Context, gc *git.Client, cfg config.RepositoryConfig) ([]string, map[string]bool, error) {
	tracked, err := gc.LsFiles(ctx)
	if err != nil {
		return nil, nil, err
	}
	set := map[string]bool{}
	for _, p := range tracked {
		set[p] = true
	}
	untrackedSet := map[string]bool{}
	if cfg.IncludeUntracked {
		untracked, err := gc.LsFilesUntracked(ctx)
		if err == nil {
			for _, p := range untracked {
				if !set[p] {
					untrackedSet[p] = true
				}
				set[p] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, untrackedSet, nil
}

// ingest reads each candidate path and builds its FileMeta, enforcing limits.
func (s *Snapshot) ingest(_ context.Context, paths []string) error {
	for _, rel := range paths {
		if rel == "" {
			continue
		}
		abs, ok := s.root.JoinWithin(rel)
		if !ok {
			continue
		}
		li, err := os.Lstat(abs)
		if err != nil {
			continue
		}
		if li.Mode()&os.ModeSymlink != 0 && !s.cfg.FollowSymlinks {
			continue
		}
		if !li.Mode().IsRegular() {
			continue
		}
		size := li.Size()
		isUntracked := s.untracked[rel]
		meta := FileMeta{
			Path:      rel,
			Size:      size,
			Mode:      li.Mode(),
			Tracked:   s.isGit && !isUntracked,
			Untracked: isUntracked,
			Vendored:  s.class.isVendored(rel),
			Generated: s.class.isGenerated(rel),
		}

		// Read a bounded prefix for binary/language detection; full content only
		// up to the per-file limit for hashing.
		if size <= s.cfg.MaxFileBytes && s.totalBy+size <= s.cfg.MaxTotalSnapshotBytes {
			data, rerr := readLimited(abs, s.cfg.MaxFileBytes)
			if rerr == nil {
				meta.Binary = IsBinary(data)
				meta.Language = DetectLanguage(rel, data)
				meta.Hash = hashutil.Hash(data)
				s.totalBy += int64(len(data))
			}
		} else {
			// Oversized or over-budget: keep metadata, sniff a small prefix.
			prefix, _ := readLimited(abs, 8192)
			meta.Binary = IsBinary(prefix)
			meta.Language = DetectLanguage(rel, prefix)
			meta.Hash = hashutil.HashString(rel + ":" + strconv.FormatInt(size, 10))
			if s.totalBy+size > s.cfg.MaxTotalSnapshotBytes {
				s.trunc = true
			}
		}

		s.byPath[rel] = len(s.files)
		s.files = append(s.files, meta)
	}
	return nil
}

func (s *Snapshot) computeID() {
	parts := make([]string, 0, len(s.files)+3)
	parts = append(parts, "head="+s.head)
	for _, f := range s.files {
		parts = append(parts, f.Path+"@"+f.Hash)
	}
	ignoreHash := hashutil.HashString(strings.Join(append([]string{s.cfg.IgnoreFile}, s.cfg.VendorGlobs...), "|"))
	parts = append(parts, "ignore="+ignoreHash)
	s.id = "snap-" + hashutil.Short(hashutil.HashList(parts), 16)
}

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if limit <= 0 {
		limit = 1 << 20
	}
	buf := make([]byte, 0, min64(limit, 64*1024))
	tmp := make([]byte, 32*1024)
	var total int64
	for total < limit {
		n, rerr := f.Read(tmp)
		if n > 0 {
			remaining := limit - total
			if int64(n) > remaining {
				n = int(remaining)
			}
			buf = append(buf, tmp[:n]...)
			total += int64(n)
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			// Propagate genuine read failures instead of silently returning a
			// truncated buffer: a partial read must surface as an error so callers
			// (e.g. the trigram index build) treat the file conservatively rather
			// than indexing incomplete content.
			return nil, rerr
		}
	}
	return buf, nil
}

func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := 1
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}

// newFileContent wraps file bytes with metadata and a precomputed line-offset
// table, the single constructor used by all snapshot read paths. It normalizes
// content to UTF-8 here so every consumer (search, reads, the trigram index) sees
// one consistent byte space; valid UTF-8 is returned unchanged.
func newFileContent(meta FileMeta, data []byte) FileContent {
	data = normalizeToUTF8(data)
	return FileContent{
		Meta:        meta,
		Bytes:       data,
		Lines:       countLines(data),
		LineOffsets: LineOffsets(data),
	}
}

// LineOffsets returns the 0-based byte start offset of each text line in data.
// It uses the same line definition as bufio.Scanner's ScanLines: lines are split
// on '\n', and a trailing '\n' does not produce an empty final line. The result
// has one entry per scanned line, so search paths can slice a line as
// data[offsets[i] : <next '\n' or len>] without copying the file into a []string.
// It returns nil for empty data.
//
// Offsets are int32, which holds any input shorter than math.MaxInt32 (~2 GiB).
// The live search path feeds only readLimited output (bounded by MaxFileBytes,
// default 1 MiB); review git-blob reads are not capped but realistic objects are
// far below the limit, so a single file cannot overflow the table in practice.
func LineOffsets(data []byte) []int32 {
	n := len(data)
	if n == 0 {
		return nil
	}
	offsets := make([]int32, 1, n/40+1)
	offsets[0] = 0
	for i := 0; i < n; i++ {
		if data[i] == '\n' {
			start := i + 1
			// A '\n' at the very end terminates the last line without starting
			// a new one (matches ScanLines: no trailing empty line).
			if start < n {
				offsets = append(offsets, int32(start))
			}
		}
	}
	return offsets
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
