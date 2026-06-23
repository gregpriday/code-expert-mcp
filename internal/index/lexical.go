// Package index implements the retrieval ladder's search tiers: lexical/path
// search (pure Go, the default) and a heuristic structural symbol index. Rich
// semantic indexers are optional and live in adapters.
package index

import (
	"bufio"
	"bytes"
	"context"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/gregpriday/codeexpert/internal/repo"
	"github.com/gregpriday/codeexpert/internal/schema"
)

// SearchMode selects how a query is interpreted.
type SearchMode string

const (
	ModeLiteral SearchMode = "literal"
	ModeWord    SearchMode = "word"
	ModeRegex   SearchMode = "regex"
	ModePath    SearchMode = "path"
)

// SearchQuery is a single search request.
type SearchQuery struct {
	Pattern      string
	Mode         SearchMode
	Include      []string
	Exclude      []string
	MaxResults   int
	ContextLines int
	// IncludeGenerated/IncludeVendored allow searching normally-excluded files.
	IncludeGenerated bool
	IncludeVendored  bool
	CaseInsensitive  bool
}

// SearchHit is one match with surrounding context.
type SearchHit struct {
	Path          string   `json:"path"`
	Line          int      `json:"line"`
	Column        int      `json:"column"`
	Match         string   `json:"match"`
	ContextBefore []string `json:"context_before,omitempty"`
	ContextAfter  []string `json:"context_after,omitempty"`
	FileHash      string   `json:"file_hash"`
}

// LexicalEngine searches a snapshot's text files.
type LexicalEngine struct {
	snap        *repo.Snapshot
	maxPerFile  int
	globalLimit int
}

// NewLexicalEngine builds an engine. globalLimit caps total hits across files.
func NewLexicalEngine(snap *repo.Snapshot, globalLimit int) *LexicalEngine {
	if globalLimit <= 0 {
		globalLimit = 100
	}
	return &LexicalEngine{snap: snap, maxPerFile: 20, globalLimit: globalLimit}
}

// Search executes a query over the snapshot, honoring caps and cancellation.
func (e *LexicalEngine) Search(ctx context.Context, q SearchQuery) ([]SearchHit, error) {
	if strings.TrimSpace(q.Pattern) == "" {
		return nil, schema.NewError(schema.CodeInvalidArgument, "empty search pattern")
	}
	limit := q.MaxResults
	if limit <= 0 || limit > e.globalLimit {
		limit = e.globalLimit
	}
	ctxLines := q.ContextLines
	if ctxLines < 0 {
		ctxLines = 0
	}
	if ctxLines > 8 {
		ctxLines = 8
	}

	if q.Mode == ModePath {
		return e.searchPaths(q, limit), nil
	}

	re, err := e.compile(q)
	if err != nil {
		return nil, err
	}

	var hits []SearchHit
	for _, fm := range e.snap.ListFiles() {
		if err := ctx.Err(); err != nil {
			return hits, schema.NewError(schema.CodeCancelled, "search cancelled")
		}
		if !e.eligible(fm, q) {
			continue
		}
		if !matchGlobs(fm.Path, q.Include, q.Exclude) {
			continue
		}
		fc, rerr := e.snap.ReadFile(ctx, fm.Path)
		if rerr != nil || fc.Meta.Binary {
			continue
		}
		fileHits := searchFile(fc, re, ctxLines, e.maxPerFile)
		for _, h := range fileHits {
			hits = append(hits, h)
			if len(hits) >= limit {
				return hits, nil
			}
		}
	}
	return hits, nil
}

func (e *LexicalEngine) eligible(fm repo.FileMeta, q SearchQuery) bool {
	if fm.Binary {
		return false
	}
	if fm.Vendored && !q.IncludeVendored {
		return false
	}
	if fm.Generated && !q.IncludeGenerated {
		return false
	}
	return true
}

func (e *LexicalEngine) compile(q SearchQuery) (*regexp.Regexp, error) {
	var pattern string
	switch q.Mode {
	case ModeRegex:
		if len(q.Pattern) > 2000 {
			return nil, schema.NewError(schema.CodeInvalidArgument, "regex pattern too long")
		}
		pattern = q.Pattern
	case ModeWord:
		pattern = `\b` + regexp.QuoteMeta(q.Pattern) + `\b`
	default: // literal
		pattern = regexp.QuoteMeta(q.Pattern)
	}
	if q.CaseInsensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, schema.NewError(schema.CodeInvalidArgument, "invalid regex: %v", err)
	}
	return re, nil
}

func (e *LexicalEngine) searchPaths(q SearchQuery, limit int) []SearchHit {
	needle := q.Pattern
	if q.CaseInsensitive || q.Mode == ModePath {
		needle = strings.ToLower(needle)
	}
	var hits []SearchHit
	for _, fm := range e.snap.ListFiles() {
		p := fm.Path
		cmp := p
		if q.CaseInsensitive || q.Mode == ModePath {
			cmp = strings.ToLower(p)
		}
		matched := strings.Contains(cmp, needle)
		if !matched {
			if ok, _ := doublestar.Match(q.Pattern, p); ok {
				matched = true
			}
		}
		if matched {
			hits = append(hits, SearchHit{Path: p, Line: 0, Match: p, FileHash: fm.Hash})
			if len(hits) >= limit {
				break
			}
		}
	}
	return hits
}

// searchFile scans a file's bytes line by line for matches with context. It
// slices each line directly out of the underlying buffer using the precomputed
// line-offset table, allocating strings only for the lines it actually reports
// (the match and its context) instead of copying every line into a []string.
func searchFile(fc repo.FileContent, re *regexp.Regexp, ctxLines, maxPerFile int) []SearchHit {
	data := fc.Bytes
	offsets := fc.LineOffsets
	if offsets == nil {
		offsets = repo.LineOffsets(data)
	}
	path := fc.Meta.Path
	hash := fc.Meta.Hash
	var hits []SearchHit
	for i := 0; i < len(offsets); i++ {
		line := lineAt(data, offsets, i)
		loc := re.FindIndex(line)
		if loc == nil {
			continue
		}
		h := SearchHit{
			Path:     path,
			Line:     i + 1,
			Column:   loc[0] + 1,
			Match:    trimLineBytes(line),
			FileHash: hash,
		}
		for j := i - ctxLines; j < i; j++ {
			if j >= 0 {
				h.ContextBefore = append(h.ContextBefore, trimLineBytes(lineAt(data, offsets, j)))
			}
		}
		for j := i + 1; j <= i+ctxLines && j < len(offsets); j++ {
			h.ContextAfter = append(h.ContextAfter, trimLineBytes(lineAt(data, offsets, j)))
		}
		hits = append(hits, h)
		if len(hits) >= maxPerFile {
			break
		}
	}
	return hits
}

// lineAt returns line i (0-based) from data as a sub-slice, with a trailing
// carriage return stripped to match bufio.Scanner's ScanLines. The returned
// slice aliases data and must not be mutated.
func lineAt(data []byte, offsets []int32, i int) []byte {
	start := int(offsets[i])
	var end int
	if i+1 < len(offsets) {
		// The next line begins one byte past its terminating '\n', so that '\n'
		// sits at offsets[i+1]-1 and is excluded from this line.
		end = int(offsets[i+1]) - 1
	} else {
		// Last recorded line: ends at a trailing '\n' if present, else at EOF.
		end = len(data)
		if nl := bytes.IndexByte(data[start:], '\n'); nl >= 0 {
			end = start + nl
		}
	}
	line := data[start:end]
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line
}

// trimLineBytes converts a line slice to a display string, capping it at 400
// bytes like trimLine but without first allocating the full line for long lines.
func trimLineBytes(b []byte) string {
	if len(b) > 400 {
		return string(b[:400]) + "…"
	}
	return string(b)
}

func splitLines(content []byte) []string {
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

func trimLine(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}

func matchGlobs(path string, include, exclude []string) bool {
	for _, g := range exclude {
		if ok, _ := doublestar.Match(g, path); ok {
			return false
		}
	}
	if len(include) == 0 {
		return true
	}
	for _, g := range include {
		if ok, _ := doublestar.Match(g, path); ok {
			return true
		}
	}
	return false
}
