package index

import "regexp/syntax"

// requiredTrigramsRegex derives, conservatively, the trigrams an RE2 pattern
// guarantees in every matching string. The result is an OR (across validated
// top-level alternation branches) of ANDed trigram sets. It returns scan=true
// whenever no trigram can be proven mandatory — the engine then scans all files.
//
// The walk only ever claims a trigram drawn from a literal run the pattern MUST
// produce, so it can never require a trigram a true match might lack. When in
// doubt it claims nothing, which only loosens the filter. Correctness over
// selectivity, always.
func requiredTrigramsRegex(pattern string, caseInsensitive bool) (sets [][]uint32, scan bool) {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, true
	}
	re = re.Simplify()
	// Case folding is active via the engine flag or an inline (?i) in the pattern.
	foldActive := caseInsensitive || hasFold(re)
	// ASCII folding can't cover Unicode case variants of non-ASCII content, so any
	// non-ASCII in a case-insensitive pattern is unsafe to reason about.
	if containsNonASCII(pattern) && foldActive {
		return nil, true
	}
	re = unwrapCapture(re)

	if re.Op == syntax.OpAlternate {
		out := make([][]uint32, 0, len(re.Sub))
		for _, br := range re.Sub {
			tris := trigramsOfRuns(mustRuns(br), foldActive)
			if len(tris) == 0 {
				// A branch with no usable mandatory trigram matches "anything", so
				// the union can't narrow — scan everything.
				return nil, true
			}
			out = append(out, tris)
		}
		return out, false
	}

	tris := trigramsOfRuns(mustRuns(re), foldActive)
	if len(tris) == 0 {
		return nil, true
	}
	return [][]uint32{tris}, false
}

// unwrapCapture peels transparent capture groups so a top-level "(a|b)" is seen
// as an alternation.
func unwrapCapture(re *syntax.Regexp) *syntax.Regexp {
	for re.Op == syntax.OpCapture && len(re.Sub) == 1 {
		re = re.Sub[0]
	}
	return re
}

// hasFold reports whether any node carries the case-fold flag.
func hasFold(re *syntax.Regexp) bool {
	if re.Flags&syntax.FoldCase != 0 {
		return true
	}
	for _, sub := range re.Sub {
		if hasFold(sub) {
			return true
		}
	}
	return false
}

// mustRuns returns the lowercased literal byte runs that every string matched by
// re is guaranteed to contain. It is deliberately conservative: any operator that
// makes content optional or variable contributes nothing.
func mustRuns(re *syntax.Regexp) []string {
	switch re.Op {
	case syntax.OpLiteral:
		return []string{string(asciiLowerBytes([]byte(string(re.Rune))))}
	case syntax.OpCapture:
		return mustRuns(re.Sub[0])
	case syntax.OpConcat:
		var runs []string
		var cur []byte
		flush := func() {
			if len(cur) > 0 {
				runs = append(runs, string(cur))
				cur = nil
			}
		}
		for _, sub := range re.Sub {
			if sub.Op == syntax.OpLiteral {
				// Adjacent literals merge so a trigram can span the seam.
				cur = append(cur, asciiLowerBytes([]byte(string(sub.Rune)))...)
				continue
			}
			flush()
			runs = append(runs, mustRuns(sub)...)
		}
		flush()
		return runs
	case syntax.OpPlus:
		// At least one occurrence is mandatory; keep the child's runs (without
		// merging across the repetition boundary).
		return mustRuns(re.Sub[0])
	case syntax.OpRepeat:
		if re.Min >= 1 {
			return mustRuns(re.Sub[0])
		}
		return nil
	default:
		// OpStar, OpQuest, OpRepeat{0,_}, OpAlternate (nested), OpCharClass,
		// OpAnyChar(NotNL), every anchor, OpEmptyMatch, OpNoMatch: nothing is a
		// mandatory literal here.
		return nil
	}
}

// trigramsOfRuns returns the de-duplicated trigrams across all runs of length
// >= 3 (shorter runs can't require a trigram). When foldActive is true it drops
// trigrams containing a fold-unsafe ASCII byte, keeping the filter sound under
// Unicode case folding.
func trigramsOfRuns(runs []string, foldActive bool) []uint32 {
	seen := make(map[uint32]struct{})
	var out []uint32
	for _, r := range runs {
		for _, k := range byteTrigramsFiltered([]byte(r), foldActive) {
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				out = append(out, k)
			}
		}
	}
	return out
}
