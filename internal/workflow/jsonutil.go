package workflow

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/gregpriday/codeexpert/internal/provider"
)

var nameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// inferSchema builds a best-effort, non-strict JSON schema for a Go type, used
// as a soft structured-output hint. Go validation remains the authoritative gate.
func inferSchema[T any](name string) *provider.JSONSchema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		return nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	return &provider.JSONSchema{
		Name:   nameSanitizer.ReplaceAllString(name, "_"),
		Schema: raw,
		Strict: false,
	}
}

// extractJSON pulls the first balanced JSON object/array out of model output,
// tolerating Markdown code fences and surrounding prose.
func extractJSON(b []byte) []byte {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil
	}
	// Strip a leading ```json fence if present.
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	if json.Valid([]byte(s)) {
		return []byte(s)
	}
	// Find the first balanced object or array.
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return nil
	}
	open := s[start]
	close := byte('}')
	if open == '[' {
		close = ']'
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				cand := s[start : i+1]
				if json.Valid([]byte(cand)) {
					return []byte(cand)
				}
				return nil
			}
		}
	}
	return nil
}
