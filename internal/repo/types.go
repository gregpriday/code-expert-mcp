// Package repo freezes repository inputs into an immutable snapshot and produces
// the deterministic repository brief and change manifest. It is strictly
// read-only: nothing in this package writes under the repository root or Git
// directory.
package repo

import "io/fs"

// FileMeta is the metadata for one file in a snapshot. Paths are repo-relative
// and forward-slash normalized.
type FileMeta struct {
	Path      string      `json:"path"`
	Size      int64       `json:"size"`
	Mode      fs.FileMode `json:"-"`
	Binary    bool        `json:"binary"`
	Hash      string      `json:"hash"`
	Tracked   bool        `json:"tracked"`
	Untracked bool        `json:"untracked"`
	Generated bool        `json:"generated"`
	Vendored  bool        `json:"vendored"`
	Language  string      `json:"language"`
	// ContentFrozen is true when Hash is a content hash of the file's frozen bytes
	// (the in-budget case), so a later read can be verified byte-for-byte against
	// it. When false (oversized or over the snapshot byte budget), Hash is a
	// synthetic path+size digest and a read is verified by size only.
	ContentFrozen bool `json:"-"`
}

// FileContent is a file's bytes plus metadata, returned by snapshot reads.
type FileContent struct {
	Meta  FileMeta
	Bytes []byte
	Lines int
	// LineOffsets holds the 0-based byte start offset of each text line in Bytes,
	// using the same line definition as bufio.Scanner's ScanLines. It is built
	// once by newFileContent so search paths can slice lines without copying the
	// whole file into a []string on every query. A nil slice means the offsets
	// were not precomputed (e.g. empty content); callers may rebuild via
	// LineOffsets(Bytes).
	LineOffsets []int32
}

// ChangeManifest is the frozen description of a review target's changes.
type ChangeManifest struct {
	Target       string        `json:"target"`
	BaseSHA      string        `json:"base_sha"`
	HeadSHA      string        `json:"head_sha"`
	BaseLabel    string        `json:"base_label"`
	HeadLabel    string        `json:"head_label"`
	Files        []ChangedFile `json:"files"`
	IncludesWT   bool          `json:"includes_working_tree"`
	TotalAdded   int           `json:"total_added"`
	TotalDeleted int           `json:"total_deleted"`
}

// ChangedFile is one changed file with its diff metadata and frozen hunks.
type ChangedFile struct {
	Path      string      `json:"path"`
	OldPath   string      `json:"old_path,omitempty"`
	Status    string      `json:"status"`
	Added     int         `json:"added"`
	Deleted   int         `json:"deleted"`
	Binary    bool        `json:"binary"`
	Generated bool        `json:"generated"`
	Vendored  bool        `json:"vendored"`
	Language  string      `json:"language"`
	Diff      string      `json:"-"` // unified diff text, frozen at snapshot time
	NewRanges []LineRange `json:"new_ranges,omitempty"`
	// Symbols are the enclosing changed symbols ("name (kind)"), populated by the
	// analysis layer (the repo package itself has no symbol index).
	Symbols []string `json:"symbols,omitempty"`
}

// LineRange is an inclusive 1-based line span in the head view.
type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}
