// Trigram candidate pre-filter. This narrows the set of files an exact search
// must scan, without ever dropping a file that could contain a match. The exact
// verifier (searchFile) remains the source of truth for what actually matches.
//
// Soundness invariant (the one rule everything obeys):
//
//	candidates(query) ⊇ { f : the exact verifier finds a match in f }
//
// A file may only be excluded when we can prove it cannot match. Every design
// choice resolves ambiguity toward keeping files: the index is over-approximated
// (ASCII-lowercased so case folds in; degraded/tiny files become
// always-candidates) and the query is under-constrained (we require only
// provably-mandatory trigrams; anything uncertain falls back to a full scan).
// Both directions only ever enlarge the kept set, so neither can drop a true
// match. trigram_oracle_test.go enforces this property against a brute-force
// oracle over randomized inputs.
package index

import (
	"context"
	"sort"
	"unicode"

	"github.com/gregpriday/codeexpert/internal/repo"
)

// defaultTrigramsPerFile bounds the distinct trigrams indexed per file. A file
// that would exceed it (or is shorter than a trigram) is not indexed and instead
// becomes an unconditional candidate — never dropped. 1<<18 comfortably covers a
// 1 MiB text file while capping high-entropy (minified/generated) blobs.
const defaultTrigramsPerFile = 1 << 18

// trigramIndex maps a lowercased byte-trigram to the sorted, de-duplicated set of
// file IDs (indices into the snapshot's ListFiles slice) that contain it.
type trigramIndex struct {
	postings        map[uint32][]int32
	alwaysCandidate []int32 // file IDs that bypass filtering (degraded/tiny/binary)
	fileCount       int32
}

// triKey packs three bytes into a comparable uint32 key.
func triKey(a, b, c byte) uint32 {
	return uint32(a)<<16 | uint32(b)<<8 | uint32(c)
}

// asciiLowerByte folds A–Z to a–z and leaves every other byte (including all
// bytes >= 0x80) untouched. It is a pure per-byte map, so lowercasing commutes
// with trigram extraction and is identical on the index and query sides.
func asciiLowerByte(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

func asciiLowerBytes(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = asciiLowerByte(b[i])
	}
	return out
}

func containsNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

// asciiFoldEscapes[c] is true when ASCII byte c is a letter whose Unicode simple
// case-fold cycle includes a non-ASCII rune — e.g. 'k'/'K' fold with U+212A
// (KELVIN SIGN) and 's'/'S' fold with U+017F (LATIN SMALL LETTER LONG S). It is
// computed from unicode.SimpleFold, the exact table Go's regexp (?i) uses, so it
// stays correct as Unicode evolves. Under case-insensitive search the verifier
// can match such a fold variant, whose bytes differ from the ASCII letter, so any
// trigram containing one of these bytes is unsafe to require.
var asciiFoldEscapes [128]bool

func init() {
	for r := rune(0); r < 128; r++ {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f >= 0x80 {
				asciiFoldEscapes[r] = true
				break
			}
		}
	}
}

// foldUnsafeByte reports whether byte c cannot be safely required in a trigram
// under case-insensitive matching. Two cases: (1) any non-ASCII byte — it belongs
// to a multibyte rune whose Unicode fold orbit may be entirely different bytes
// (e.g. U+212B ANGSTROM folds with Å/å, and such runes reach the index via regex
// escapes like \x{212b} that keep the pattern string ASCII); (2) an ASCII letter
// whose simple fold escapes ASCII (e.g. 'k'<->U+212A, 's'<->U+017F). Dropping such
// trigrams only loosens the filter, so it stays sound.
func foldUnsafeByte(c byte) bool {
	return c >= 0x80 || asciiFoldEscapes[c]
}

// byteTrigrams returns the distinct trigrams of b, which the caller must already
// have ASCII-lowercased so the keys line up with the index.
func byteTrigrams(b []byte) []uint32 {
	return byteTrigramsFiltered(b, false)
}

// byteTrigramsFiltered returns the distinct trigrams of b (already
// ASCII-lowercased). When foldActive is true it skips any trigram containing a
// byte whose Unicode case-fold escapes ASCII, because under case-insensitivity
// the verifier could match a non-ASCII fold variant that lacks that trigram —
// requiring it would be a false negative. Dropping such trigrams only loosens the
// filter (still sound); the remaining fold-stable trigrams keep it selective.
func byteTrigramsFiltered(b []byte, foldActive bool) []uint32 {
	if len(b) < 3 {
		return nil
	}
	seen := make(map[uint32]struct{}, len(b))
	out := make([]uint32, 0, len(b))
	for i := 0; i+3 <= len(b); i++ {
		if foldActive && (foldUnsafeByte(b[i]) || foldUnsafeByte(b[i+1]) || foldUnsafeByte(b[i+2])) {
			continue
		}
		k := triKey(b[i], b[i+1], b[i+2])
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	return out
}

// collectTrigrams records the file's distinct lowercased trigrams into set. It
// reports false (caller marks the file always-candidate) when the file is too
// short to form a trigram or exceeds the per-file budget — never emitting a
// partial set, since a missing tail trigram could otherwise false-negative a
// query.
func collectTrigrams(data []byte, set map[uint32]struct{}, budget int) bool {
	if len(data) < 3 {
		return false
	}
	for i := 0; i+3 <= len(data); i++ {
		k := triKey(asciiLowerByte(data[i]), asciiLowerByte(data[i+1]), asciiLowerByte(data[i+2]))
		if _, ok := set[k]; !ok {
			set[k] = struct{}{}
			if budget > 0 && len(set) > budget {
				return false
			}
		}
	}
	return true
}

// buildTrigramIndex reads every non-binary file through the snapshot (so the
// index sees exactly the bytes the verifier will, including any encoding
// normalization) and records its trigrams. Files are visited in ListFiles order,
// so each posting list and the always-candidate list come out sorted ascending
// and unique without an explicit sort.
func buildTrigramIndex(snap *repo.Snapshot, budget int) *trigramIndex {
	files := snap.ListFiles()
	ix := &trigramIndex{
		postings:  make(map[uint32][]int32),
		fileCount: int32(len(files)),
	}
	tmp := make(map[uint32]struct{}, 4096)
	for id, fm := range files {
		// Binary files are never matched by the verifier (it skips them), so they
		// need neither postings nor an always-candidate entry.
		if fm.Binary {
			continue
		}
		fc, err := snap.ReadFile(context.Background(), fm.Path)
		if err != nil {
			// A transient read failure here must not silently drop the file: keep
			// it as an unconditional candidate so the verifier still scans it (and
			// retries the read) rather than excluding a possible match.
			ix.alwaysCandidate = append(ix.alwaysCandidate, int32(id))
			continue
		}
		if fc.Meta.Binary {
			continue
		}
		for k := range tmp {
			delete(tmp, k)
		}
		if !collectTrigrams(fc.Bytes, tmp, budget) {
			ix.alwaysCandidate = append(ix.alwaysCandidate, int32(id))
			continue
		}
		for k := range tmp {
			ix.postings[k] = append(ix.postings[k], int32(id))
		}
	}
	// Postings were appended in ascending id order, but map iteration over tmp
	// emits a file's trigrams in arbitrary order — that does not affect per-list
	// ordering (each list gets at most one entry per file, appended as files are
	// visited in order), so lists remain sorted ascending and unique.
	return ix
}

// candidates returns the sound superset of file IDs that may contain a match for
// the query, or (nil, false) when no useful narrowing is possible and the caller
// must scan all files. Only content modes are handled; path mode does not use the
// content index.
func (ix *trigramIndex) candidates(mode SearchMode, pattern string, caseInsensitive bool) ([]int32, bool) {
	var sets [][]uint32
	switch mode {
	case ModeLiteral, ModeWord:
		tris, scan := requiredTrigramsLiteral(pattern, caseInsensitive)
		if scan {
			return nil, false
		}
		sets = [][]uint32{tris}
	case ModeRegex:
		s, scan := requiredTrigramsRegex(pattern, caseInsensitive)
		if scan {
			return nil, false
		}
		sets = s
	default:
		return nil, false
	}
	return ix.candidatesForSets(sets), true
}

// requiredTrigramsLiteral derives the mandatory trigrams of a literal/word
// token. Word mode's \b anchors constrain nothing about which trigrams appear, so
// it is identical to literal. A token under 3 bytes, or a case-insensitive token
// with non-ASCII bytes (ASCII folding can't cover Unicode case variants), forces
// a full scan.
func requiredTrigramsLiteral(token string, caseInsensitive bool) (tris []uint32, scan bool) {
	if caseInsensitive && containsNonASCII(token) {
		return nil, true
	}
	b := asciiLowerBytes([]byte(token))
	if len(b) < 3 {
		return nil, true
	}
	tris = byteTrigramsFiltered(b, caseInsensitive)
	if len(tris) == 0 {
		// Every trigram touched a fold-unsafe byte (e.g. "keys" under case
		// insensitivity, where 'k' and 's' both fold to non-ASCII) — scan.
		return nil, true
	}
	return tris, false
}

// candidatesForSets unions the per-set intersections (OR of ANDs) and always
// folds in the always-candidate files, which carry no postings and would
// otherwise be wrongly excluded.
func (ix *trigramIndex) candidatesForSets(sets [][]uint32) []int32 {
	var acc []int32
	for _, set := range sets {
		acc = sortedUnion(acc, ix.intersectAnd(set))
	}
	return sortedUnion(acc, ix.alwaysCandidate)
}

// intersectAnd returns the files containing all of the required trigrams. If any
// trigram is absent from the postings, no indexed file can match and the result
// is empty (always-candidate files are added by the caller).
func (ix *trigramIndex) intersectAnd(set []uint32) []int32 {
	if len(set) == 0 {
		return nil
	}
	lists := make([][]int32, 0, len(set))
	for _, t := range set {
		pl, ok := ix.postings[t]
		if !ok || len(pl) == 0 {
			return nil
		}
		lists = append(lists, pl)
	}
	// Intersect from the shortest list to minimize work.
	sort.Slice(lists, func(i, j int) bool { return len(lists[i]) < len(lists[j]) })
	result := append([]int32(nil), lists[0]...)
	for _, l := range lists[1:] {
		result = sortedIntersect(result, l)
		if len(result) == 0 {
			break
		}
	}
	return result
}

// sortedIntersect returns the intersection of two ascending, unique int32 slices.
func sortedIntersect(a, b []int32) []int32 {
	var out []int32
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

// sortedUnion returns the union of two ascending, unique int32 slices.
func sortedUnion(a, b []int32) []int32 {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	out := make([]int32, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		default:
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}
