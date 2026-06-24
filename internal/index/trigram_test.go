package index

import (
	"reflect"
	"sort"
	"testing"
)

func triset(s string) []uint32 {
	return byteTrigrams([]byte(s))
}

func sortedKeys(k []uint32) []uint32 {
	out := append([]uint32(nil), k...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestByteTrigrams(t *testing.T) {
	if got := byteTrigrams([]byte("ab")); got != nil {
		t.Errorf("len<3 should yield no trigrams, got %v", got)
	}
	got := byteTrigrams([]byte("abcd"))
	want := []uint32{triKey('a', 'b', 'c'), triKey('b', 'c', 'd')}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("byteTrigrams(abcd) = %v, want %v", got, want)
	}
	// Duplicates collapse.
	if got := byteTrigrams([]byte("aaaa")); len(got) != 1 {
		t.Errorf("byteTrigrams(aaaa) should dedup to 1, got %v", got)
	}
}

func TestAsciiLower(t *testing.T) {
	if asciiLowerByte('A') != 'a' || asciiLowerByte('Z') != 'z' {
		t.Error("A-Z must fold to a-z")
	}
	if asciiLowerByte('a') != 'a' || asciiLowerByte('5') != '5' || asciiLowerByte(0xE9) != 0xE9 {
		t.Error("non-uppercase bytes must be unchanged")
	}
}

func TestCollectTrigramsDegradation(t *testing.T) {
	set := map[uint32]struct{}{}
	if collectTrigrams([]byte("ab"), set, 1000) {
		t.Error("file shorter than a trigram must report degraded (false)")
	}
	set = map[uint32]struct{}{}
	if !collectTrigrams([]byte("abcdef"), set, 1000) {
		t.Error("normal file should index ok")
	}
	// Budget exceeded -> degrade.
	set = map[uint32]struct{}{}
	if collectTrigrams([]byte("abcdefghij"), set, 2) {
		t.Error("exceeding the per-file budget must report degraded (false)")
	}
}

func TestRequiredTrigramsLiteral(t *testing.T) {
	if _, scan := requiredTrigramsLiteral("ab", false); !scan {
		t.Error("token <3 must force full scan")
	}
	if _, scan := requiredTrigramsLiteral("café", true); !scan {
		t.Error("case-insensitive non-ASCII must force full scan")
	}
	tris, scan := requiredTrigramsLiteral("abc", false)
	if scan {
		t.Fatal("3-char literal should yield trigrams")
	}
	if !reflect.DeepEqual(tris, triset("abc")) {
		t.Errorf("requiredTrigramsLiteral(abc) = %v, want %v", tris, triset("abc"))
	}
	// Case folds: "ABC" required trigrams equal "abc" trigrams (index is lowercased).
	tA, _ := requiredTrigramsLiteral("ABC", false)
	if !reflect.DeepEqual(sortedKeys(tA), sortedKeys(triset("abc"))) {
		t.Errorf("upper/lower literal trigrams must match: %v vs %v", tA, triset("abc"))
	}
}

func TestRequiredTrigramsRegex(t *testing.T) {
	mustScan := func(pat string) {
		t.Helper()
		if _, scan := requiredTrigramsRegex(pat, false); !scan {
			t.Errorf("regex %q should fall back to full scan", pat)
		}
	}
	mustNarrow := func(pat string, wantSets int) [][]uint32 {
		t.Helper()
		sets, scan := requiredTrigramsRegex(pat, false)
		if scan {
			t.Fatalf("regex %q should narrow, got full scan", pat)
		}
		if len(sets) != wantSets {
			t.Fatalf("regex %q: got %d sets, want %d", pat, len(sets), wantSets)
		}
		return sets
	}

	// No mandatory >=3 literal -> scan.
	mustScan(".*")
	mustScan("a.*b")     // both literals <3
	mustScan("[a-z]+")   // char class only
	mustScan("(abc)?")   // optional
	mustScan("(abc)*")   // star
	mustScan("^$")       // anchors only
	mustScan("foo|x")    // one alternation branch too weak
	mustScan("(")        // parse error
	mustScan("a{0,3}xy") // optional repeat + short literal

	// Single mandatory literal run.
	sets := mustNarrow("foobar", 1)
	if !reflect.DeepEqual(sortedKeys(sets[0]), sortedKeys(triset("foobar"))) {
		t.Errorf("foobar trigrams = %v, want %v", sets[0], triset("foobar"))
	}
	// Literal survives surrounding wildcards.
	mustNarrow("foo.*bar", 1)
	mustNarrow("[a-z]+abc", 1)
	mustNarrow("a{0,3}bcd", 1) // "bcd" is mandatory
	mustNarrow("(abc)+", 1)    // at least one "abc"

	// Validated top-level alternation -> OR of sets.
	mustNarrow("foo|barbaz", 2)
}

func TestIntersectAndAlwaysCandidate(t *testing.T) {
	ix := &trigramIndex{
		postings: map[uint32][]int32{
			triKey('a', 'b', 'c'): {1, 2, 5},
			triKey('b', 'c', 'd'): {2, 5, 9},
		},
		alwaysCandidate: []int32{7},
		fileCount:       10,
	}
	// AND of the two trigrams -> {2,5}; always-candidate {7} unioned in.
	got := ix.candidatesForSets([][]uint32{{triKey('a', 'b', 'c'), triKey('b', 'c', 'd')}})
	want := []int32{2, 5, 7}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidatesForSets = %v, want %v", got, want)
	}
	// A trigram absent from postings -> only always-candidate survives.
	got = ix.candidatesForSets([][]uint32{{triKey('z', 'z', 'z')}})
	if !reflect.DeepEqual(got, []int32{7}) {
		t.Errorf("absent trigram should leave only always-candidate, got %v", got)
	}
	// OR of two single-trigram sets -> union of postings + always-candidate.
	got = ix.candidatesForSets([][]uint32{{triKey('a', 'b', 'c')}, {triKey('b', 'c', 'd')}})
	if want := []int32{1, 2, 5, 7, 9}; !reflect.DeepEqual(got, want) {
		t.Errorf("OR union = %v, want %v", got, want)
	}
}

func TestSortedSetOps(t *testing.T) {
	if got := sortedIntersect([]int32{1, 3, 5, 7}, []int32{3, 4, 5, 9}); !reflect.DeepEqual(got, []int32{3, 5}) {
		t.Errorf("sortedIntersect = %v, want [3 5]", got)
	}
	if got := sortedUnion([]int32{1, 3, 5}, []int32{2, 3, 6}); !reflect.DeepEqual(got, []int32{1, 2, 3, 5, 6}) {
		t.Errorf("sortedUnion = %v, want [1 2 3 5 6]", got)
	}
	if got := sortedUnion(nil, []int32{2, 3}); !reflect.DeepEqual(got, []int32{2, 3}) {
		t.Errorf("sortedUnion(nil, x) = %v", got)
	}
}
