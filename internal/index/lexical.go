// Package index implements the retrieval ladder's search tiers: lexical/path
// search (pure Go, the default) and a heuristic structural symbol index. Rich
// semantic indexers are optional and live in adapters.
package index

import (
	"bufio"
	"bytes"
	"context"
	"math"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

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
	// Section is the nearest enclosing Markdown heading (without its leading
	// '#'), set only for hits in Markdown files so a result locates itself.
	Section string `json:"section,omitempty"`
}

// SearchResult is a ranked set of hits plus coverage metadata, so callers can
// tell when matches were ranked and capped rather than exhaustive.
type SearchResult struct {
	Hits         []SearchHit
	FilesMatched int  // distinct files with at least one match (before the limit)
	Truncated    bool // some matches may be omitted (result limit or per-file cap)
}

// LexicalEngine searches a snapshot's text files.
type LexicalEngine struct {
	snap        *repo.Snapshot
	maxPerFile  int
	globalLimit int

	// Optional trigram pre-filter. When enabled, the index is built lazily on the
	// first content search and reused for the engine's lifetime; it only narrows
	// the files the exact verifier scans and never changes results.
	trigramEnabled bool
	trigramBudget  int
	triOnce        sync.Once
	tri            *trigramIndex
}

// LexicalOption configures a LexicalEngine at construction.
type LexicalOption func(*LexicalEngine)

// WithTrigramFilter enables (or disables) the trigram candidate pre-filter.
// maxPerFile bounds the distinct trigrams indexed per file before that file is
// treated as an unconditional candidate; <= 0 uses the default.
func WithTrigramFilter(enabled bool, maxPerFile int) LexicalOption {
	return func(e *LexicalEngine) {
		e.trigramEnabled = enabled
		if maxPerFile > 0 {
			e.trigramBudget = maxPerFile
		}
	}
}

// NewLexicalEngine builds an engine. globalLimit caps total hits across files.
// The trigram pre-filter is off unless enabled via WithTrigramFilter.
func NewLexicalEngine(snap *repo.Snapshot, globalLimit int, opts ...LexicalOption) *LexicalEngine {
	if globalLimit <= 0 {
		globalLimit = 100
	}
	e := &LexicalEngine{
		snap:          snap,
		maxPerFile:    20,
		globalLimit:   globalLimit,
		trigramBudget: defaultTrigramsPerFile,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// trigram returns the lazily-built trigram index, or nil if filtering is off. The
// index is built once (under sync.Once) with a background context so it is always
// complete and reusable regardless of any single request's cancellation.
func (e *LexicalEngine) trigram() *trigramIndex {
	if !e.trigramEnabled {
		return nil
	}
	e.triOnce.Do(func() {
		e.tri = buildTrigramIndex(e.snap, e.trigramBudget)
	})
	return e.tri
}

// Search executes a query over the snapshot and returns ranked hits, honoring
// caps and cancellation. It is a thin wrapper over SearchDetailed for callers
// that only need the hits.
func (e *LexicalEngine) Search(ctx context.Context, q SearchQuery) ([]SearchHit, error) {
	res, err := e.SearchDetailed(ctx, q)
	return res.Hits, err
}

// SearchDetailed executes a query and returns ranked hits with coverage
// metadata. Files are scored by cheap, deterministic signals — name/path match,
// a definition-shaped match line, match count (with diminishing returns), minus
// a penalty for test/generated/vendored files — then hits are emitted in score
// order (tie-broken by path) until the limit is reached. To rank correctly it
// scans all eligible files instead of stopping at the first N matches; the
// in-memory snapshot's byte budget bounds that work.
func (e *LexicalEngine) SearchDetailed(ctx context.Context, q SearchQuery) (SearchResult, error) {
	if strings.TrimSpace(q.Pattern) == "" {
		return SearchResult{}, schema.NewError(schema.CodeInvalidArgument, "empty search pattern")
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
		return SearchResult{}, err
	}

	files := e.snap.ListFiles()

	// Narrow the file set with the trigram pre-filter when it can prove a
	// narrowing is sound; otherwise scan everything. The filter only ever yields a
	// superset of the files that can match, so the verified results are identical
	// to a full scan — it just skips files that provably cannot match.
	var candidates []int32
	filtered := false
	if ti := e.trigram(); ti != nil {
		if cand, ok := ti.candidates(q.Mode, q.Pattern, q.CaseInsensitive); ok {
			candidates, filtered = cand, true
		}
	}

	type fileMatch struct {
		path  string
		hits  []SearchHit
		score float64
	}
	var matches []fileMatch
	totalHits := 0
	perFileCapped := false
	visit := func(fm repo.FileMeta) error {
		if err := ctx.Err(); err != nil {
			return schema.NewError(schema.CodeCancelled, "search cancelled")
		}
		if !e.eligible(fm, q) {
			return nil
		}
		if !matchGlobs(fm.Path, q.Include, q.Exclude) {
			return nil
		}
		fc, rerr := e.snap.ReadFile(ctx, fm.Path)
		if rerr != nil || fc.Meta.Binary {
			return nil
		}
		fileHits := searchFile(fc, re, ctxLines, e.maxPerFile)
		if len(fileHits) == 0 {
			return nil
		}
		if len(fileHits) == e.maxPerFile {
			// The per-file cap was reached; this file may have more matches than
			// reported, so surface truncation rather than implying completeness.
			perFileCapped = true
		}
		annotateSections(fm, fc, fileHits)
		totalHits += len(fileHits)
		matches = append(matches, fileMatch{
			path:  fm.Path,
			hits:  fileHits,
			score: scoreFile(fm, fileHits, q),
		})
		return nil
	}

	if filtered {
		for _, id := range candidates {
			if int(id) < 0 || int(id) >= len(files) {
				continue
			}
			if err := visit(files[id]); err != nil {
				return SearchResult{}, err
			}
		}
	} else {
		for _, fm := range files {
			if err := visit(fm); err != nil {
				return SearchResult{}, err
			}
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].path < matches[j].path
	})

	hits := make([]SearchHit, 0, limit)
	emittedFiles := 0
	for _, m := range matches {
		if len(hits) >= limit {
			break
		}
		emittedFiles++
		for _, h := range m.hits {
			if len(hits) >= limit {
				break
			}
			hits = append(hits, h)
		}
	}
	return SearchResult{
		Hits:         hits,
		FilesMatched: len(matches),
		Truncated:    perFileCapped || len(hits) < totalHits || emittedFiles < len(matches),
	}, nil
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

// searchPaths matches against file paths and ranks them so that the closest
// name matches surface first: exact basename, then basename prefix, then
// basename substring, then a full-path substring, then a glob match.
func (e *LexicalEngine) searchPaths(q SearchQuery, limit int) SearchResult {
	needle := strings.ToLower(q.Pattern)
	type pathMatch struct {
		hit   SearchHit
		score float64
	}
	var matches []pathMatch
	for _, fm := range e.snap.ListFiles() {
		p := fm.Path
		base := strings.ToLower(path.Base(p))
		lp := strings.ToLower(p)
		var score float64
		matched := true
		switch {
		case base == needle:
			score = 100
		case strings.HasPrefix(base, needle):
			score = 50
		case strings.Contains(base, needle):
			score = 25
		case strings.Contains(lp, needle):
			score = 10
		default:
			if ok, _ := doublestar.Match(q.Pattern, p); ok {
				score = 5
			} else {
				matched = false
			}
		}
		if matched {
			matches = append(matches, pathMatch{
				hit:   SearchHit{Path: p, Line: 0, Match: p, FileHash: fm.Hash},
				score: score,
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].hit.Path < matches[j].hit.Path
	})
	hits := make([]SearchHit, 0, limit)
	for _, m := range matches {
		if len(hits) >= limit {
			break
		}
		hits = append(hits, m.hit)
	}
	return SearchResult{
		Hits:         hits,
		FilesMatched: len(matches),
		Truncated:    len(hits) < len(matches),
	}
}

// defKeywords are language-agnostic prefixes that introduce a definition, used
// only as a ranking signal (not for correctness). It spans Go, JS/TS, PHP,
// Python, Ruby, Rust, Java/C#, Perl, and more.
var defKeywords = []string{
	"func", "function", "fn", "def", "sub", "proc", "method",
	"type", "class", "interface", "struct", "enum", "trait", "impl",
	"record", "object", "module", "namespace", "package",
	"const", "var", "let",
}

// defModifiers are access/qualifier keywords that can precede a definition keyword
// (PHP/Java/C#/TS/Rust); stripping them lets "public function foo", "export const
// x", or "async function y" still read as a definition.
var defModifiers = map[string]bool{
	"public": true, "private": true, "protected": true, "static": true,
	"abstract": true, "final": true, "async": true, "export": true,
	"default": true, "pub": true, "open": true, "internal": true,
	"override": true, "virtual": true, "readonly": true,
}

// scoreFile assigns a relevance score to a file's matches from cheap,
// deterministic signals; higher is better.
func scoreFile(fm repo.FileMeta, hits []SearchHit, q SearchQuery) float64 {
	// More matches help, with diminishing returns so a huge file can't dominate.
	score := 2 * math.Log2(1+float64(len(hits)))

	// A name/path match is a strong relevance signal, but only meaningful when
	// the query is a literal token — a regex isn't a path fragment.
	if q.Mode == ModeLiteral || q.Mode == ModeWord {
		token := strings.ToLower(strings.TrimSpace(q.Pattern))
		if token != "" {
			switch {
			case strings.Contains(strings.ToLower(path.Base(fm.Path)), token):
				score += 10
			case strings.Contains(strings.ToLower(fm.Path), token):
				score += 4
			}
		}
		// Identifier-aware: a whole sub-token match ("user" in "user_service.go",
		// "http" in "HTTPServer.ts") is a stronger signal than an incidental
		// substring, across any naming convention.
		if matchesSubtoken(path.Base(fm.Path), q.Pattern) {
			score += 3
		}
	}

	// A definition-shaped match line usually answers "where is this defined?".
	for _, h := range hits {
		if looksLikeDefinition(h.Match, q.Pattern, q.Mode) {
			score += 6
			break
		}
	}

	// Markdown relevance: the query appearing in a heading or front-matter
	// title/tags line is the prose analogue of a definition — it usually answers
	// the query, which makes this the core of good Markdown/lessons ranking.
	if isMarkdown(fm) {
		for _, h := range hits {
			if markdownRelevantLine(h.Match) {
				score += 6
				break
			}
		}
	}

	// De-emphasize files that are usually not the answer.
	if fm.Generated || fm.Vendored || isTestPath(fm.Path) {
		score -= 3
	}
	return score
}

// isMarkdown reports whether a file is Markdown, by detected language or
// extension (covering .md, .markdown, .mdx).
func isMarkdown(fm repo.FileMeta) bool {
	if fm.Language == "Markdown" {
		return true
	}
	switch strings.ToLower(path.Ext(fm.Path)) {
	case ".md", ".markdown", ".mdx":
		return true
	}
	return false
}

// matchesSubtoken reports whether every sub-token of the query token appears as a
// whole sub-token of the file's basename.
func matchesSubtoken(basename, token string) bool {
	qt := SplitIdentifier(token)
	if len(qt) == 0 {
		return false
	}
	bt := SplitIdentifier(basename)
	set := make(map[string]struct{}, len(bt))
	for _, t := range bt {
		set[t] = struct{}{}
	}
	for _, t := range qt {
		if _, ok := set[t]; !ok {
			return false
		}
	}
	return true
}

// frontMatterKeys are high-signal YAML/TOML front-matter fields common in doc and
// lesson systems; a match on one of these lines is treated like a heading match.
var frontMatterKeys = []string{
	"title:", "tags:", "description:", "summary:",
	"keywords:", "name:", "category:", "aliases:",
}

// markdownRelevantLine reports whether a matched line is a Markdown heading or a
// front-matter field — the high-signal locations in a doc.
func markdownRelevantLine(line string) bool {
	if _, ok := markdownHeading([]byte(line)); ok {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(line))
	for _, k := range frontMatterKeys {
		if strings.HasPrefix(lower, k) {
			return true
		}
	}
	return false
}

// annotateSections fills SearchHit.Section with the nearest enclosing Markdown
// heading for each hit in a Markdown file; it is a no-op for other files. It
// tracks fenced code blocks (so a '#' comment inside ```/~~~ is not mistaken for a
// heading) and recognizes both ATX (# Heading) and setext (underlined with ===)
// headings. This lets a result locate itself within a document, which matters for
// searching Markdown lesson corpora.
func annotateSections(fm repo.FileMeta, fc repo.FileContent, hits []SearchHit) {
	if !isMarkdown(fm) || len(hits) == 0 {
		return
	}
	offsets := fc.LineOffsets
	if offsets == nil {
		offsets = repo.LineOffsets(fc.Bytes)
	}
	type heading struct {
		line int
		text string
	}
	var headings []heading
	inFence := false
	var fenceChar byte
	fenceLen := 0
	prevText := ""
	prevWasText := false
	for i := 0; i < len(offsets); i++ {
		raw := lineAt(fc.Bytes, offsets, i)
		s := string(raw)
		indent := 0
		for indent < len(s) && s[indent] == ' ' {
			indent++
		}
		trimmed := strings.TrimSpace(s)
		// Fences are recognized only with fewer than 4 spaces of indent (4+ spaces
		// is an indented code block, not a fence).
		if indent < 4 {
			if ch, n, rest, ok := fenceMarker(trimmed); ok {
				switch {
				case !inFence:
					inFence, fenceChar, fenceLen = true, ch, n
				case ch == fenceChar && n >= fenceLen && rest == "":
					// A closing fence must use the same character, be at least as long,
					// and carry no info string; a mismatched inner fence is content.
					inFence = false
				}
				prevWasText = false
				continue
			}
		}
		if inFence {
			prevWasText = false
			continue
		}
		if text, ok := markdownHeading(raw); ok {
			headings = append(headings, heading{line: i + 1, text: text})
			prevWasText = false
			continue
		}
		// Setext H1: a line of only '=' directly under a non-blank text line. The
		// heading is attributed to the text line above (1-based line number i).
		if prevWasText && isSetextUnderline(trimmed) {
			headings = append(headings, heading{line: i, text: prevText})
			prevWasText = false
			continue
		}
		if trimmed == "" {
			prevWasText = false
		} else {
			prevText, prevWasText = trimmed, true
		}
	}
	if len(headings) == 0 {
		return
	}
	sort.Slice(headings, func(i, j int) bool { return headings[i].line < headings[j].line })
	for hi := range hits {
		// The enclosing heading: the last one at or above the hit's line, so a hit
		// on a heading line gets that heading rather than its parent.
		idx := sort.Search(len(headings), func(k int) bool { return headings[k].line > hits[hi].Line })
		if idx > 0 {
			hits[hi].Section = headings[idx-1].text
		}
	}
}

// fenceMarker parses a code-fence line: a run of at least three '`' or '~'. It
// returns the fence character, the run length, the trimmed remainder after the
// run (an info string on an opening fence; required empty for a closing fence),
// and whether the line is a fence at all.
func fenceMarker(trimmed string) (ch byte, runLen int, rest string, ok bool) {
	if trimmed == "" || (trimmed[0] != '`' && trimmed[0] != '~') {
		return 0, 0, "", false
	}
	c := trimmed[0]
	n := 0
	for n < len(trimmed) && trimmed[n] == c {
		n++
	}
	if n < 3 {
		return 0, 0, "", false
	}
	return c, n, strings.TrimSpace(trimmed[n:]), true
}

// isSetextUnderline reports whether a trimmed line is a setext H1 underline (only
// '=' characters). '-' underlines are intentionally not recognized to avoid
// ambiguity with thematic breaks and YAML front-matter fences.
func isSetextUnderline(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] != '=' {
			return false
		}
	}
	return true
}

// markdownHeading returns the text of an ATX heading line ("## Title" -> "Title")
// and whether the line is one, following CommonMark: at most 3 spaces of
// indentation, 1–6 leading '#', and a required space/tab (or end of line) after
// the '#' run. An optional closing '#' run is stripped. It does not track fenced
// code blocks itself — annotateSections handles that.
func markdownHeading(line []byte) (string, bool) {
	s := string(line)
	indent := 0
	for indent < len(s) && s[indent] == ' ' {
		indent++
	}
	if indent >= 4 { // 4+ spaces is an indented code block, not a heading
		return "", false
	}
	s = s[indent:]
	if s == "" || s[0] != '#' {
		return "", false
	}
	i := 0
	for i < len(s) && s[i] == '#' {
		i++
	}
	if i > 6 {
		return "", false
	}
	if i < len(s) && s[i] != ' ' && s[i] != '\t' {
		return "", false // CommonMark requires whitespace after the '#' run
	}
	rest := strings.TrimRight(s[i:], " \t")
	// Strip an optional closing '#' run, but only when it is whitespace-separated
	// from the text — otherwise the '#' chars are literal ("# C#" -> "C#").
	end := len(rest)
	h := end
	for h > 0 && rest[h-1] == '#' {
		h--
	}
	if h < end && h > 0 && (rest[h-1] == ' ' || rest[h-1] == '\t') {
		rest = rest[:h]
	}
	text := strings.TrimSpace(rest)
	if text == "" {
		return "", false
	}
	return text, true
}

// looksLikeDefinition reports whether a matched line appears to define the
// queried token — a keyword-led declaration, or the token bound with = or :. It
// is a deliberately conservative ranking hint; regex queries are skipped since
// there is no single token to reason about.
func looksLikeDefinition(line, token string, mode SearchMode) bool {
	if mode == ModeRegex {
		return false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	lt := strings.ToLower(token)
	// Strip leading access/qualifier modifiers so "public function foo", "export
	// const x", "async function y" still resolve to their definition keyword.
	kwLine := stripDefModifiers(lower)
	for _, kw := range defKeywords {
		if strings.HasPrefix(kwLine, kw+" ") && strings.Contains(lower, lt) {
			return true
		}
	}
	// Assignment or field/key binding: "<token> =", "<token> :=", "<token>:".
	if strings.HasPrefix(lower, lt) {
		rest := strings.TrimSpace(trimmed[len(lt):])
		if strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, ":") {
			return true
		}
	}
	return false
}

// stripDefModifiers removes a leading run of access/qualifier modifier words from
// an already-lowercased line.
func stripDefModifiers(lower string) string {
	for {
		sp := strings.IndexByte(lower, ' ')
		if sp <= 0 {
			return lower
		}
		if defModifiers[lower[:sp]] {
			lower = strings.TrimLeft(lower[sp+1:], " ")
			continue
		}
		return lower
	}
}

// isTestPath reports whether a path looks like test code, by common naming and
// directory conventions across ecosystems.
func isTestPath(p string) bool {
	lp := strings.ToLower(p)
	switch {
	case strings.Contains(lp, "_test."),
		strings.Contains(lp, ".test."),
		strings.Contains(lp, ".spec."),
		strings.Contains(lp, "_spec."),
		strings.Contains(lp, "__tests__/"),
		strings.HasPrefix(lp, "test/"),
		strings.HasPrefix(lp, "tests/"),
		strings.Contains(lp, "/test/"),
		strings.Contains(lp, "/tests/"):
		return true
	}
	return false
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

// matchGlobs reports whether path is included given exclude (which takes
// precedence) and include (empty means include everything) globs. Unlike
// repo.matchAny it matches the full path only, with no basename fallback; the
// two matchers are intentionally kept separate.
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
