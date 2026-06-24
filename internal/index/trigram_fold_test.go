package index

import (
	"context"
	"reflect"
	"sync"
	"testing"
)

func TestAsciiFoldEscapes(t *testing.T) {
	// These ASCII letters fold to non-ASCII codepoints under Unicode simple
	// folding, which RE2 (?i) honors: 'k'<->U+212A, 's'<->U+017F.
	for _, c := range []byte{'k', 'K', 's', 'S'} {
		if !asciiFoldEscapes[c] {
			t.Errorf("expected %q to be fold-unsafe", c)
		}
	}
	// Ordinary letters/digits are fold-stable.
	for _, c := range []byte{'a', 'b', 'c', 'z', 'A', 'Z', '0', '_'} {
		if asciiFoldEscapes[c] {
			t.Errorf("expected %q to be fold-stable", c)
		}
	}
}

func TestRequiredTrigramsLiteralFold(t *testing.T) {
	// Case-sensitive: no folding, normal trigrams.
	if tris, scan := requiredTrigramsLiteral("keys", false); scan || len(tris) != 2 {
		t.Errorf("case-sensitive keys: scan=%v tris=%v, want scan=false, 2 trigrams", scan, tris)
	}
	// Case-insensitive "keys": both trigrams ("key","eys") touch a fold-unsafe
	// byte ('k' or 's'), so none are usable -> full scan.
	if _, scan := requiredTrigramsLiteral("keys", true); !scan {
		t.Error("case-insensitive 'keys' must fall back to scan (all trigrams fold-unsafe)")
	}
	// Case-insensitive "class": "cla" is fold-stable and kept; "las"/"ass" drop.
	tris, scan := requiredTrigramsLiteral("class", true)
	if scan {
		t.Fatal("case-insensitive 'class' should keep the fold-stable 'cla' trigram")
	}
	if !reflect.DeepEqual(tris, triset("cla")) {
		t.Errorf("case-insensitive 'class' trigrams = %v, want just %v", tris, triset("cla"))
	}
}

// TestSearchFoldVariants is the end-to-end regression for the critical bug: a
// case-insensitive ASCII query must still find files whose only match is via a
// non-ASCII Unicode case-fold variant. The filter-on result must equal filter-off.
func TestSearchFoldVariants(t *testing.T) {
	files := map[string]string{
		"a.txt": "prefix keyſ suffix",    // "keyſ" — long s folds with 's'
		"b.txt": "nothing relevant here", //
		"c.txt": "weight is 5 milK now",  // "milK" — KELVIN folds with 'k'
	}
	snap := buildSnapshot(t, files)
	on := NewLexicalEngine(snap, 100, WithTrigramFilter(true, 0))
	off := NewLexicalEngine(snap, 100, WithTrigramFilter(false, 0))
	cases := []struct {
		mode SearchMode
		pat  string
		find bool // whether the verifier itself matches the fold variant
	}{
		{ModeLiteral, "keys", true},
		{ModeWord, "keys", false}, // Go \b is ASCII-only, so \bkeys\b doesn't span "keyſ"
		{ModeRegex, "keys", true},
		{ModeLiteral, "milk", true},
	}
	for _, c := range cases {
		sq := SearchQuery{Pattern: c.pat, Mode: c.mode, CaseInsensitive: true, MaxResults: 100}
		ron, err := on.SearchDetailed(context.Background(), sq)
		if err != nil {
			t.Fatalf("on %s %q: %v", c.mode, c.pat, err)
		}
		roff, err := off.SearchDetailed(context.Background(), sq)
		if err != nil {
			t.Fatalf("off %s %q: %v", c.mode, c.pat, err)
		}
		// The core guarantee: the filter never drops a match the verifier finds.
		if !reflect.DeepEqual(ron.Hits, roff.Hits) {
			t.Errorf("%s %q: filter on/off differ\n on=%+v\noff=%+v", c.mode, c.pat, ron.Hits, roff.Hits)
		}
		if c.find && len(ron.Hits) == 0 {
			t.Errorf("%s %q: expected a fold-variant match, got none", c.mode, c.pat)
		}
	}
}

// TestRegexEscapeFoldSafety guards the second critical false-negative class: a
// case-insensitive regex with a non-ASCII rune injected via an escape (\x{...}).
// The pattern string stays ASCII, so the parsed literal becomes a multibyte rune
// whose (?i) fold orbit uses different bytes — the filter must not require it.
func TestRegexEscapeFoldSafety(t *testing.T) {
	// Case-insensitive: U+212B (ANGSTROM) folds with Å/å, so its trigram must be
	// dropped -> no usable trigram -> scan.
	if _, scan := requiredTrigramsRegex(`(zy)+\x{212b}`, true); !scan {
		t.Error("ci regex with \\x{212b} must fall back to scan (fold orbit spans bytes)")
	}
	// Case-sensitive: exact bytes are sound and selective, so it must NOT scan.
	if sets, scan := requiredTrigramsRegex(`(zy)+\x{212b}`, false); scan || len(sets) == 0 {
		t.Errorf("case-sensitive \\x{212b} should keep its exact trigram, scan=%v sets=%v", scan, sets)
	}

	// End-to-end: a file whose only match is via the fold variant must still be
	// found (filter on == off).
	files := map[string]string{
		"hit.txt":  "zyÅ here",           // zy + Å (U+00C5) — folds with \x{212b}
		"miss.txt": "nothing to see now", //
	}
	snap := buildSnapshot(t, files)
	on := NewLexicalEngine(snap, 100, WithTrigramFilter(true, 0))
	off := NewLexicalEngine(snap, 100, WithTrigramFilter(false, 0))
	sq := SearchQuery{Pattern: `(zy)+\x{212b}`, Mode: ModeRegex, CaseInsensitive: true, MaxResults: 100}
	ron, err := on.SearchDetailed(context.Background(), sq)
	if err != nil {
		t.Fatalf("on: %v", err)
	}
	roff, err := off.SearchDetailed(context.Background(), sq)
	if err != nil {
		t.Fatalf("off: %v", err)
	}
	if !reflect.DeepEqual(ron.Hits, roff.Hits) {
		t.Errorf("filter on/off differ for fold-escape regex\n on=%+v\noff=%+v", ron.Hits, roff.Hits)
	}
	if len(ron.Hits) != 1 || ron.Hits[0].Path != "hit.txt" {
		t.Errorf("expected hit.txt via fold variant, got %+v", ron.Hits)
	}
}

// TestTrigramConcurrentSearch drives the production path: many goroutines hitting
// a shared trigram-enabled engine, racing the lazy sync.Once build against
// concurrent readers (with some cancelled contexts). Run under -race.
func TestTrigramConcurrentSearch(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 30; i++ {
		files[sprintfName(i)] = "package p\n\nfunc handler() { cache.Get(); cache.Set() }\ntype Cache struct{}\n"
	}
	snap := buildSnapshot(t, files)
	e := NewLexicalEngine(snap, 100, WithTrigramFilter(true, 64))

	patterns := []string{"func", "cache", "handler", "package", "Cache"}
	var wg sync.WaitGroup
	for g := 0; g < 40; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			ctx := context.Background()
			if g%5 == 0 {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			mode := ModeLiteral
			if g%3 == 0 {
				mode = ModeRegex
			}
			_, _ = e.SearchDetailed(ctx, SearchQuery{
				Pattern: patterns[g%len(patterns)], Mode: mode, MaxResults: 50, CaseInsensitive: g%2 == 0,
			})
		}(g)
	}
	wg.Wait()
}
