package index

import (
	"context"
	"testing"
)

func TestMarkdownSectionBreadcrumb(t *testing.T) {
	md := "# Title\n\nintro line\n\n## Caching\n\nthe cache is fast here\n\n## Storage\n\nit stores things\n"
	snap := buildSnapshot(t, map[string]string{"guide.md": md})
	e := NewLexicalEngine(snap, 100)
	res, err := e.SearchDetailed(context.Background(), SearchQuery{Pattern: "fast", Mode: ModeLiteral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("want 1 hit, got %d: %+v", len(res.Hits), res.Hits)
	}
	if res.Hits[0].Section != "Caching" {
		t.Errorf("Section = %q, want %q", res.Hits[0].Section, "Caching")
	}
}

func TestMarkdownSectionAbsentBeforeFirstHeading(t *testing.T) {
	md := "preamble before any heading mentions widget\n\n# Later\n\nbody\n"
	snap := buildSnapshot(t, map[string]string{"doc.md": md})
	e := NewLexicalEngine(snap, 100)
	res, err := e.SearchDetailed(context.Background(), SearchQuery{Pattern: "widget", Mode: ModeLiteral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Section != "" {
		t.Fatalf("hit before any heading should have empty Section, got %+v", res.Hits)
	}
}

func TestMarkdownHeadingRanking(t *testing.T) {
	files := map[string]string{
		"a.md": "# Intro\n\nwe mention Caching once in body text here\n",
		"b.md": "# Caching\n\nlots of detail about it\nmore notes here\n",
	}
	snap := buildSnapshot(t, files)
	e := NewLexicalEngine(snap, 100)
	res, err := e.SearchDetailed(context.Background(), SearchQuery{Pattern: "Caching", Mode: ModeLiteral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatal("expected hits")
	}
	if res.Hits[0].Path != "b.md" {
		t.Errorf("expected b.md (term in heading) to rank first, got %q", res.Hits[0].Path)
	}
}

func TestMarkdownHitOnHeadingLineGetsOwnSection(t *testing.T) {
	// A hit that lands on a heading line must be attributed to that heading, not
	// its parent section.
	md := "# Guide\n\n## Caching\n\nbody text\n"
	snap := buildSnapshot(t, map[string]string{"g.md": md})
	e := NewLexicalEngine(snap, 100)
	res, err := e.SearchDetailed(context.Background(), SearchQuery{Pattern: "Caching", Mode: ModeLiteral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Section != "Caching" {
		t.Fatalf("hit on '## Caching' should have Section 'Caching', got %+v", res.Hits)
	}
}

func TestMarkdownIndentedFenceIsNotAFence(t *testing.T) {
	// A 4-space-indented fence marker is indented code, not a fence; it must not
	// open a stale block that swallows the following heading.
	md := "# A\n\n    ```\n\n## B\n\nneedle here\n"
	snap := buildSnapshot(t, map[string]string{"d.md": md})
	e := NewLexicalEngine(snap, 100)
	res, err := e.SearchDetailed(context.Background(), SearchQuery{Pattern: "needle", Mode: ModeLiteral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Section != "B" {
		t.Fatalf("indented ``` must not open a fence; want Section 'B', got %+v", res.Hits)
	}
}

func TestMarkdownFenceTracking(t *testing.T) {
	// A '#' comment inside a fenced code block must NOT become a section, even when
	// a mismatched inner fence (~~~ inside a ```-block) appears.
	md := "# Real Heading\n\n```sh\n# not a heading\n~~~\n## still code\n```\n\nthe needle is here\n"
	snap := buildSnapshot(t, map[string]string{"doc.md": md})
	e := NewLexicalEngine(snap, 100)
	res, err := e.SearchDetailed(context.Background(), SearchQuery{Pattern: "needle", Mode: ModeLiteral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("want 1 hit, got %d: %+v", len(res.Hits), res.Hits)
	}
	if res.Hits[0].Section != "Real Heading" {
		t.Errorf("Section = %q, want %q (code-fence '#' lines must not be headings)", res.Hits[0].Section, "Real Heading")
	}
}

func TestMarkdownHeadingParse(t *testing.T) {
	cases := []struct {
		line   string
		want   string
		isHead bool
	}{
		{"# Title", "Title", true},
		{"###  Spaced  ", "Spaced", true},
		{"   ## Indented", "Indented", true},
		{"## Closed ##", "Closed", true},
		{"# bar #", "bar", true},
		{"# C#", "C#", true},          // closing '#' not space-separated = literal
		{"## foo###", "foo###", true}, // ditto
		{"####### too many", "", false},
		{"#", "", false},
		{"not a heading", "", false},
		{"#nospace", "", false},            // CommonMark requires a space after '#'
		{"#include <stdio.h>", "", false},  // C preprocessor, not a heading
		{"#!/bin/bash", "", false},         // shebang
		{"    # Indented code", "", false}, // 4-space indent = code block
	}
	for _, tc := range cases {
		got, ok := markdownHeading([]byte(tc.line))
		if ok != tc.isHead || got != tc.want {
			t.Errorf("markdownHeading(%q) = (%q,%v), want (%q,%v)", tc.line, got, ok, tc.want, tc.isHead)
		}
	}
}
