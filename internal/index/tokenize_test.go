package index

import (
	"reflect"
	"testing"
)

func TestSplitIdentifier(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"lower", []string{"lower"}},
		{"getUserByID", []string{"get", "user", "by", "id"}},
		{"HTTPServer", []string{"http", "server"}},
		{"XMLHttpRequest", []string{"xml", "http", "request"}},
		{"user_service", []string{"user", "service"}},
		{"kebab-case-name", []string{"kebab", "case", "name"}},
		{"dotted.path.name", []string{"dotted", "path", "name"}},
		{"snake_Case_Mixed", []string{"snake", "case", "mixed"}},
		{"utf8Decode", []string{"utf8", "decode"}},
		{"ALLCAPS", []string{"allcaps"}},
		{"cache.go", []string{"cache", "go"}},
		{"two words", []string{"two", "words"}},
	}
	for _, tc := range cases {
		got := SplitIdentifier(tc.in)
		if len(got) == 0 && len(tc.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitIdentifier(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestMatchesSubtoken(t *testing.T) {
	cases := []struct {
		base, token string
		want        bool
	}{
		{"user_service.go", "user", true},
		{"HTTPServer.ts", "http", true},
		{"HTTPServer.ts", "server", true},
		{"cache.go", "cache", true},
		{"handler.go", "cache", false},
		{"getUserById.js", "user", true},
		{"getUserById.js", "id", true},
		{"payments.go", "pay", false}, // substring, not a whole sub-token
	}
	for _, tc := range cases {
		if got := matchesSubtoken(tc.base, tc.token); got != tc.want {
			t.Errorf("matchesSubtoken(%q, %q) = %v, want %v", tc.base, tc.token, got, tc.want)
		}
	}
}
