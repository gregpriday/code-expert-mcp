package index

import (
	"context"
	"math/rand"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/gregpriday/codeexpert/internal/repo"
)

// compileForTest mirrors LexicalEngine.compile exactly, so the brute-force oracle
// verifies with the same regex the engine would.
func compileForTest(mode SearchMode, pattern string, ci bool) (*regexp.Regexp, error) {
	var p string
	switch mode {
	case ModeRegex:
		p = pattern
	case ModeWord:
		p = `\b` + regexp.QuoteMeta(pattern) + `\b`
	default:
		p = regexp.QuoteMeta(pattern)
	}
	if ci {
		p = "(?i)" + p
	}
	return regexp.Compile(p)
}

// bruteForceSet is the ground truth: the set of non-binary files that actually
// contain a match, scanning every file's normalized bytes.
func bruteForceSet(snap *repo.Snapshot, files []repo.FileMeta, re *regexp.Regexp) map[int32]bool {
	out := map[int32]bool{}
	for id, fm := range files {
		if fm.Binary {
			continue
		}
		fc, err := snap.ReadFile(context.Background(), fm.Path)
		if err != nil || fc.Meta.Binary {
			continue
		}
		if re.Find(fc.Bytes) != nil {
			out[int32(id)] = true
		}
	}
	return out
}

func randContent(rng *rand.Rand) string {
	switch r := rng.Intn(100); {
	case r < 10: // empty / 1-2 bytes (sub-trigram)
		return string([]rune("ab")[:rng.Intn(3)])
	case r < 18: // binary (NUL bytes)
		return "abc\x00def\x00ghijklmnop"
	default:
		// Small alphabet to force trigram collisions; mixed case, multibyte runes,
		// whitespace, separators, AND Unicode case-fold variants of ASCII letters
		// ('ſ' U+017F folds with 's', 'K' U+212A KELVIN folds with 'k', 'Å' U+212B
		// folds with the å family) to exercise the fold-safety path.
		alphabet := []rune("abcABCksKSxyz 01\n_é•ſKÅ")
		n := rng.Intn(180)
		rs := make([]rune, n)
		for i := range rs {
			rs[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(rs)
	}
}

func randSubstring(rng *rand.Rand, content string) string {
	rs := []rune(content)
	if len(rs) == 0 {
		return ""
	}
	start := rng.Intn(len(rs))
	maxLen := len(rs) - start
	if maxLen > 6 {
		maxLen = 6
	}
	return string(rs[start : start+1+rng.Intn(maxLen)])
}

func randQuery(rng *rand.Rand, corpus []string) (SearchMode, string, bool) {
	ci := rng.Intn(2) == 0
	pick := func() string {
		if rng.Intn(2) == 0 && len(corpus) > 0 {
			return randSubstring(rng, corpus[rng.Intn(len(corpus))])
		}
		alphabet := []rune("abcABksKSxyé ")
		rs := make([]rune, rng.Intn(6))
		for i := range rs {
			rs[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(rs)
	}
	q := regexp.QuoteMeta
	switch rng.Intn(10) {
	case 0:
		return ModeLiteral, pick(), ci
	case 1:
		return ModeWord, pick(), ci
	case 2:
		return ModeRegex, q(pick()), ci
	case 3:
		return ModeRegex, q(pick()) + ".*" + q(pick()), ci
	case 4:
		return ModeRegex, q(pick()) + "|" + q(pick()), ci
	case 5:
		return ModeRegex, "[a-c]+" + q(pick()), ci
	case 6:
		return ModeRegex, "(?i)" + q(pick()), ci // inline case-fold
	case 7:
		return ModeRegex, "^" + q(pick()) + "$", ci // anchors
	case 8:
		// A non-ASCII rune injected via an escape keeps the pattern string ASCII;
		// under (?i) its fold orbit can use different bytes, so the filter must not
		// require its trigram.
		return ModeRegex, q(pick()) + foldEscape(rng), ci
	default:
		return ModeRegex, "(" + q(pick()) + ")+" + q(pick()), ci
	}
}

func foldEscape(rng *rand.Rand) string {
	escapes := []string{`\x{212b}`, `\x{212a}`, `\x{17f}`, `\x{1e9e}`}
	return escapes[rng.Intn(len(escapes))]
}

// TestTrigramFilterSuperset is the soundness gate: for randomized corpora and
// queries, the candidate set the filter keeps must be a SUPERSET of the files
// that actually match. A failure here is a release blocker — it means search
// could silently miss a real result.
func TestTrigramFilterSuperset(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		rng := rand.New(rand.NewSource(seed))
		contents := make([]string, 0, 40)
		fileMap := map[string]string{}
		for i := 0; i < 40; i++ {
			c := randContent(rng)
			contents = append(contents, c)
			fileMap[sprintfName(i)] = c
		}
		snap := buildSnapshot(t, fileMap)
		files := snap.ListFiles()
		// A deliberately small budget so many files degrade to always-candidate,
		// exercising that path alongside real trigram intersection.
		idx := buildTrigramIndex(snap, 48)

		for iter := 0; iter < 3000; iter++ {
			mode, pattern, ci := randQuery(rng, contents)
			re, err := compileForTest(mode, pattern, ci)
			if err != nil {
				continue
			}
			want := bruteForceSet(snap, files, re)
			keep := map[int32]bool{}
			if cand, ok := idx.candidates(mode, pattern, ci); ok {
				for _, id := range cand {
					keep[id] = true
				}
			} else {
				for id := range files {
					keep[int32(id)] = true
				}
			}
			for id := range want {
				if !keep[id] {
					t.Fatalf("FALSE NEGATIVE: seed=%d mode=%s pattern=%q ci=%v dropped %s", seed, mode, pattern, ci, files[id].Path)
				}
			}
		}
	}
}

func sprintfName(i int) string {
	const digits = "0123456789"
	return "f" + string([]byte{digits[i/10], digits[i%10]}) + ".txt"
}

// TestTrigramFilterMatchesFullScan proves the filter changes nothing but speed:
// for a realistic polyglot corpus, SearchDetailed with the filter on returns the
// byte-identical ranked result it returns with the filter off.
func TestTrigramFilterMatchesFullScan(t *testing.T) {
	files := map[string]string{
		"cache/cache.go":      "package cache\n\n// LRU cache implementation\nfunc NewCache() *Cache { return &Cache{} }\ntype Cache struct{ items map[string]int }\n",
		"server/handler.go":   "package server\n\nfunc handleUser(w, r) { cache.Get(); cache.Set() }\nfunc handleAdmin() {}\n",
		"web/app.js":          "function getUser(id) { return fetch('/u/' + id) }\nconst CACHE = new Map()\nfunction setUser(u){ CACHE.set(u.id, u) }\n",
		"legacy/order.php":    "<?php\nfunction get_user($id) { return $db->find($id); }\nclass OrderService { public function total() {} }\n",
		"docs/guide.md":       "# User Guide\n\n## Caching\n\nThe cache stores users.\n\n## Orders\n\nOrders reference users.\n",
		"lib/util.py":         "def get_user(uid):\n    return db.lookup(uid)\n\nclass Cache:\n    pass\n",
		"unrelated/readme.md": "# Readme\n\nNothing to see here.\n",
	}
	snap := buildSnapshot(t, files)
	on := NewLexicalEngine(snap, 100, WithTrigramFilter(true, 64))
	off := NewLexicalEngine(snap, 100, WithTrigramFilter(false, 0))

	queries := []struct {
		mode SearchMode
		pat  string
		ci   bool
	}{
		{ModeLiteral, "cache", false},
		{ModeLiteral, "Cache", true},
		{ModeLiteral, "user", false},
		{ModeWord, "get_user", false},
		{ModeWord, "User", false},
		{ModeRegex, `func \w+`, false},
		{ModeRegex, `get|set`, false},
		{ModeRegex, `class \w+`, false},
		{ModeLiteral, "fn", false}, // 2 chars -> filter falls back to scan
		{ModeRegex, `.*`, false},   // no mandatory trigram -> scan
		{ModeLiteral, "users", false},
		{ModeLiteral, "Orders", false},
	}
	for _, q := range queries {
		sq := SearchQuery{Pattern: q.pat, Mode: q.mode, CaseInsensitive: q.ci, MaxResults: 1000, ContextLines: 1}
		ra, err := on.SearchDetailed(context.Background(), sq)
		if err != nil {
			t.Fatalf("filter-on %s %q: %v", q.mode, q.pat, err)
		}
		rb, err := off.SearchDetailed(context.Background(), sq)
		if err != nil {
			t.Fatalf("filter-off %s %q: %v", q.mode, q.pat, err)
		}
		if ra.FilesMatched != rb.FilesMatched || ra.Truncated != rb.Truncated {
			t.Errorf("%s %q: metadata differs on/off: %+v vs %+v", q.mode, q.pat, ra, rb)
		}
		if !reflect.DeepEqual(ra.Hits, rb.Hits) {
			t.Errorf("%s %q ci=%v: filtered result differs from full scan\n on=%+v\noff=%+v", q.mode, q.pat, q.ci, ra.Hits, rb.Hits)
		}
	}
}

// TestEncodingAndTrigramConsistency guards the subtle interaction: a non-UTF-8
// file is normalized on read, and the trigram index (built from the same read
// path) must still find it by its UTF-8 search term.
func TestSearchFindsNonUTF8File(t *testing.T) {
	// "café" encoded as Windows-1252: 'é' is the single byte 0xE9.
	latin1 := "package x\n// café latte recipe\nconst price = 5\n"
	latin1 = strings.Replace(latin1, "é", string([]byte{0xE9}), 1)
	snap := buildSnapshot(t, map[string]string{"recipe.go": latin1, "other.go": "package y\n"})
	e := NewLexicalEngine(snap, 100, WithTrigramFilter(true, 0))
	res, err := e.SearchDetailed(context.Background(), SearchQuery{Pattern: "café", Mode: ModeLiteral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Path != "recipe.go" {
		t.Fatalf("expected to find normalized 'café' in recipe.go, got %+v", res.Hits)
	}
}
