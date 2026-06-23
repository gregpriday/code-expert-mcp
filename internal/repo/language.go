package repo

import (
	"bytes"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// extLanguages maps file extensions to language names.
var extLanguages = map[string]string{
	".go": "Go", ".mod": "Go", ".sum": "Go",
	".js": "JavaScript", ".jsx": "JavaScript", ".mjs": "JavaScript", ".cjs": "JavaScript",
	".ts": "TypeScript", ".tsx": "TypeScript",
	".py": "Python", ".pyi": "Python",
	".rb": "Ruby", ".rs": "Rust", ".java": "Java", ".kt": "Kotlin", ".kts": "Kotlin",
	".c": "C", ".h": "C", ".cc": "C++", ".cpp": "C++", ".cxx": "C++", ".hpp": "C++", ".hh": "C++",
	".cs": "C#", ".php": "PHP", ".swift": "Swift", ".m": "Objective-C", ".mm": "Objective-C++",
	".scala": "Scala", ".clj": "Clojure", ".ex": "Elixir", ".exs": "Elixir", ".erl": "Erlang",
	".hs": "Haskell", ".ml": "OCaml", ".dart": "Dart", ".lua": "Lua", ".pl": "Perl", ".r": "R",
	".sh": "Shell", ".bash": "Shell", ".zsh": "Shell", ".fish": "Shell",
	".sql": "SQL", ".html": "HTML", ".htm": "HTML", ".css": "CSS", ".scss": "SCSS", ".sass": "Sass",
	".vue": "Vue", ".svelte": "Svelte",
	".json": "JSON", ".yaml": "YAML", ".yml": "YAML", ".toml": "TOML", ".xml": "XML", ".ini": "INI",
	".md": "Markdown", ".markdown": "Markdown", ".rst": "reStructuredText",
	".proto": "Protobuf", ".graphql": "GraphQL", ".gql": "GraphQL",
	".tf": "Terraform", ".dockerfile": "Dockerfile",
}

// nameLanguages maps exact filenames to languages.
var nameLanguages = map[string]string{
	"Dockerfile": "Dockerfile", "Makefile": "Makefile", "GNUmakefile": "Makefile",
	"go.mod": "Go", "go.sum": "Go", "Cargo.toml": "Rust", "package.json": "JavaScript",
	"requirements.txt": "Python", "Gemfile": "Ruby", "pom.xml": "Java", "build.gradle": "Gradle",
}

// DetectLanguage classifies a file from its name, extension, and (for shebangs)
// its first line.
func DetectLanguage(p string, content []byte) string {
	base := path.Base(p)
	if lang, ok := nameLanguages[base]; ok {
		return lang
	}
	ext := strings.ToLower(path.Ext(p))
	if lang, ok := extLanguages[ext]; ok {
		return lang
	}
	if len(content) > 2 && content[0] == '#' && content[1] == '!' {
		first := content
		if i := bytes.IndexByte(content, '\n'); i >= 0 {
			first = content[:i]
		}
		line := strings.ToLower(string(first))
		switch {
		case strings.Contains(line, "python"):
			return "Python"
		case strings.Contains(line, "bash"), strings.Contains(line, "/sh"), strings.Contains(line, "zsh"):
			return "Shell"
		case strings.Contains(line, "node"):
			return "JavaScript"
		case strings.Contains(line, "ruby"):
			return "Ruby"
		case strings.Contains(line, "perl"):
			return "Perl"
		}
	}
	return ""
}

// IsBinary heuristically detects binary content by looking for NUL bytes in the
// first 8 KiB.
func IsBinary(content []byte) bool {
	n := len(content)
	if n > 8192 {
		n = 8192
	}
	return bytes.IndexByte(content[:n], 0) >= 0
}

// classifier marks files as vendored or generated using configured globs.
type classifier struct {
	vendor    []string
	generated []string
}

func newClassifier(vendor, generated []string) classifier {
	return classifier{vendor: vendor, generated: generated}
}

func (c classifier) isVendored(p string) bool  { return matchAny(c.vendor, p) }
func (c classifier) isGenerated(p string) bool { return matchAny(c.generated, p) }

func matchAny(globs []string, p string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(g, p); ok {
			return true
		}
		// Also match against the basename for simple patterns.
		if ok, _ := doublestar.Match(g, path.Base(p)); ok {
			return true
		}
	}
	return false
}
