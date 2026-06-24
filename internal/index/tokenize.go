package index

import (
	"strings"
	"unicode"
)

// SplitIdentifier breaks an identifier or path fragment into its lowercased
// sub-tokens across the conventions used in any language: camelCase, PascalCase,
// snake_case, kebab-case, dotted.names, and slash/space separators. Acronym runs
// are kept whole but split before a trailing capitalized word, so "HTTPServer"
// yields ["http", "server"] and "getUserByID" yields ["get", "user", "by", "id"].
//
// It is language-agnostic — it keys off identifier *shape*, not grammar — so it
// works equally for Go, JavaScript, PHP, Ruby, and prose. Digits attach to the
// preceding alphabetic run ("utf8" stays one token) but start a new token after
// a separator.
func SplitIdentifier(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || r == '.' || r == '/' || r == ' ' || r == '\t':
			flush()
		case unicode.IsUpper(r):
			if len(cur) > 0 {
				prev := cur[len(cur)-1]
				// lower|digit -> Upper is a camelCase boundary ("getUser").
				if unicode.IsLower(prev) || unicode.IsDigit(prev) {
					flush()
				} else if unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
					// Acronym -> Word boundary ("HTTPServer" -> "HTTP" | "Server").
					flush()
				}
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}
