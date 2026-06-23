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
		fileHits := searchFile(fc.Bytes, re, fm.Path, fm.Hash, ctxLines, e.maxPerFile)
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

// searchFile scans content line by line for matches with context.
func searchFile(content []byte, re *regexp.Regexp, path, hash string, ctxLines, maxPerFile int) []SearchHit {
	lines := splitLines(content)
	var hits []SearchHit
	for i, line := range lines {
		loc := re.FindIndex([]byte(line))
		if loc == nil {
			continue
		}
		h := SearchHit{
			Path:     path,
			Line:     i + 1,
			Column:   loc[0] + 1,
			Match:    trimLine(line),
			FileHash: hash,
		}
		for j := i - ctxLines; j < i; j++ {
			if j >= 0 {
				h.ContextBefore = append(h.ContextBefore, trimLine(lines[j]))
			}
		}
		for j := i + 1; j <= i+ctxLines && j < len(lines); j++ {
			h.ContextAfter = append(h.ContextAfter, trimLine(lines[j]))
		}
		hits = append(hits, h)
		if len(hits) >= maxPerFile {
			break
		}
	}
	return hits
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
