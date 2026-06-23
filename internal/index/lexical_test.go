package index

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/repo"
)

// fileContent builds a FileContent the way the snapshot read path does, with a
// precomputed offset table.
func fileContent(data string) repo.FileContent {
	b := []byte(data)
	return repo.FileContent{
		Meta:        repo.FileMeta{Path: "test.go", Hash: "h1"},
		Bytes:       b,
		LineOffsets: repo.LineOffsets(b),
	}
}

// referenceSearch is the pre-refactor line-by-line implementation, kept here as
// an oracle: the offset-based searchFile must produce byte-identical results.
func referenceSearch(content []byte, re *regexp.Regexp, path, hash string, ctxLines, maxPerFile int) []SearchHit {
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

func TestSearchFileMatchesReference(t *testing.T) {
	cases := []struct {
		name    string
		content string
		re      *regexp.Regexp
		ctx     int
		max     int
	}{
		{"trailing newline", "package main\nfunc Foo() {}\nbar\n", regexp.MustCompile("Foo"), 1, 20},
		{"no trailing newline", "package main\nfunc Foo() {}\nbar", regexp.MustCompile("Foo"), 1, 20},
		{"empty file", "", regexp.MustCompile("Foo"), 2, 20},
		{"single line no newline", "the only line", regexp.MustCompile("only"), 2, 20},
		{"crlf line endings", "alpha\r\nFoo bar\r\ngamma\r\n", regexp.MustCompile("Foo"), 1, 20},
		{"match on first line", "Foo\nb\nc\n", regexp.MustCompile("Foo"), 2, 20},
		{"match on last line", "a\nb\nFoo", regexp.MustCompile("Foo"), 2, 20},
		{"empty trailing lines", "a\nFoo\n\n", regexp.MustCompile("Foo"), 2, 20},
		{"multiple matches one line", "FooFoo bar", regexp.MustCompile("Foo"), 0, 20},
		{"caret anchor", "func a\n  func b\nfunc c\n", regexp.MustCompile("^func"), 0, 20},
		{"word boundary", "foobar foo\nfoo\n", regexp.MustCompile(`\bfoo\b`), 0, 20},
		{"case insensitive", "FOO\nfoo\nFoO\n", regexp.MustCompile("(?i)foo"), 0, 20},
		{"maxPerFile cap", "x\nx\nx\nx\nx\n", regexp.MustCompile("x"), 0, 2},
		{"context clamps at ends", "Foo\nb\nc\nd\nFoo\n", regexp.MustCompile("Foo"), 8, 20},
		{"long line trimmed", strings.Repeat("a", 500) + "needle\n", regexp.MustCompile("needle"), 0, 20},
		{"no match", "alpha\nbeta\ngamma\n", regexp.MustCompile("zzz"), 2, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := referenceSearch([]byte(tc.content), tc.re, "test.go", "h1", tc.ctx, tc.max)

			gotPre := searchFile(fileContent(tc.content), tc.re, tc.ctx, tc.max)
			if !reflect.DeepEqual(gotPre, want) {
				t.Errorf("precomputed offsets:\n got=%#v\nwant=%#v", gotPre, want)
			}

			// nil LineOffsets must fall back to rebuilding and match identically.
			fc := repo.FileContent{Meta: repo.FileMeta{Path: "test.go", Hash: "h1"}, Bytes: []byte(tc.content)}
			gotNil := searchFile(fc, tc.re, tc.ctx, tc.max)
			if !reflect.DeepEqual(gotNil, want) {
				t.Errorf("nil offsets fallback:\n got=%#v\nwant=%#v", gotNil, want)
			}
		})
	}
}

func TestSearchFileColumnAndMatch(t *testing.T) {
	hits := searchFile(fileContent("a\n  xxFoo\n"), regexp.MustCompile("Foo"), 0, 20)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d: %#v", len(hits), hits)
	}
	h := hits[0]
	if h.Line != 2 {
		t.Errorf("Line = %d, want 2", h.Line)
	}
	if h.Column != 5 { // "  xx" then Foo -> byte 4 (0-based), +1
		t.Errorf("Column = %d, want 5", h.Column)
	}
	if h.Match != "  xxFoo" {
		t.Errorf("Match = %q, want %q", h.Match, "  xxFoo")
	}
}

func TestSearchFileStripsCR(t *testing.T) {
	hits := searchFile(fileContent("Foo\r\nbar\r\n"), regexp.MustCompile("Foo"), 1, 20)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if strings.Contains(hits[0].Match, "\r") {
		t.Errorf("Match retained carriage return: %q", hits[0].Match)
	}
	for _, c := range hits[0].ContextAfter {
		if strings.Contains(c, "\r") {
			t.Errorf("context retained carriage return: %q", c)
		}
	}
}

func TestSearchFileLongLineTrim(t *testing.T) {
	line := strings.Repeat("a", 500)
	hits := searchFile(fileContent(line+"\n"), regexp.MustCompile("a"), 0, 20)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	// trimLineBytes caps at 400 bytes and appends the ellipsis rune.
	if !strings.HasSuffix(hits[0].Match, "…") {
		t.Errorf("expected trimmed match to end with ellipsis, got %q", hits[0].Match)
	}
	if want := strings.Repeat("a", 400) + "…"; hits[0].Match != want {
		t.Errorf("Match = %q, want 400 a's + ellipsis", hits[0].Match)
	}
}

func TestSearchFileContext(t *testing.T) {
	hits := searchFile(fileContent("l1\nl2\nFoo\nl4\nl5\n"), regexp.MustCompile("Foo"), 1, 20)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	h := hits[0]
	if !reflect.DeepEqual(h.ContextBefore, []string{"l2"}) {
		t.Errorf("ContextBefore = %#v, want [l2]", h.ContextBefore)
	}
	if !reflect.DeepEqual(h.ContextAfter, []string{"l4"}) {
		t.Errorf("ContextAfter = %#v, want [l4]", h.ContextAfter)
	}
}

// BenchmarkSearchFile_10KLines exercises the all-lines scan path (a rare match
// so every line is inspected). The offset-based scan should allocate per
// reported hit, not per file line.
func BenchmarkSearchFile_10KLines(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 10000; i++ {
		if i%2500 == 0 {
			sb.WriteString("needle marker line\n")
		} else {
			sb.WriteString("the quick brown fox jumps over the lazy dog\n")
		}
	}
	fc := fileContent(sb.String())
	re := regexp.MustCompile("needle")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = searchFile(fc, re, 2, 20)
	}
}
