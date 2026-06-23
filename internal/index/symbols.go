package index

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"

	"github.com/gregpriday/codeexpert/internal/repo"
)

// Symbol is a definition discovered in the repository. Heuristic is true when it
// came from the generic structural index rather than a semantic parser.
type Symbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"` // func, method, type, const, var, class, def
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Signature string `json:"signature,omitempty"`
	Heuristic bool   `json:"heuristic"`
}

// SymbolIndex extracts symbols from snapshot files on demand.
type SymbolIndex struct {
	snap *repo.Snapshot
}

// NewSymbolIndex builds a symbol index over a snapshot.
func NewSymbolIndex(snap *repo.Snapshot) *SymbolIndex { return &SymbolIndex{snap: snap} }

// SymbolsInFile returns the symbols defined in a single file.
func (si *SymbolIndex) SymbolsInFile(ctx context.Context, path string) ([]Symbol, error) {
	fc, err := si.snap.ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	if fc.Meta.Binary {
		return nil, nil
	}
	if strings.HasSuffix(path, ".go") {
		if syms, ok := goSymbols(path, fc.Bytes); ok {
			return syms, nil
		}
	}
	return genericSymbols(path, fc.Bytes), nil
}

// FindSymbol searches all eligible files for a symbol by name (and optional
// kind). It scans file-by-file and is bounded by maxFiles.
func (si *SymbolIndex) FindSymbol(ctx context.Context, name, kind string, maxFiles int) ([]Symbol, error) {
	var out []Symbol
	scanned := 0
	for _, fm := range si.snap.ListFiles() {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if fm.Binary || fm.Vendored || fm.Language == "" {
			continue
		}
		// Cheap pre-filter: only parse files whose content mentions the name.
		fc, err := si.snap.ReadFile(ctx, fm.Path)
		if err != nil || !strings.Contains(string(fc.Bytes), name) {
			continue
		}
		syms, _ := si.SymbolsInFile(ctx, fm.Path)
		for _, s := range syms {
			if s.Name == name && (kind == "" || s.Kind == kind) {
				out = append(out, s)
			}
		}
		scanned++
		if maxFiles > 0 && scanned >= maxFiles {
			break
		}
	}
	return out, nil
}

// goSymbols parses a Go file with the standard library AST for precise symbols.
func goSymbols(path string, content []byte) ([]Symbol, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, content, parser.SkipObjectResolution)
	if err != nil {
		return nil, false // fall back to heuristic on parse error
	}
	var syms []Symbol
	pos := func(p token.Pos) int { return fset.Position(p).Line }
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			kind := "func"
			sig := "func " + d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind = "method"
				sig = "method " + d.Name.Name
			}
			syms = append(syms, Symbol{
				Name: d.Name.Name, Kind: kind, Path: path,
				StartLine: pos(d.Pos()), EndLine: pos(d.End()), Signature: sig,
			})
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					syms = append(syms, Symbol{Name: s.Name.Name, Kind: "type", Path: path,
						StartLine: pos(s.Pos()), EndLine: pos(s.End()), Signature: "type " + s.Name.Name})
				case *ast.ValueSpec:
					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					for _, n := range s.Names {
						if n.Name == "_" {
							continue
						}
						syms = append(syms, Symbol{Name: n.Name, Kind: kind, Path: path,
							StartLine: pos(n.Pos()), EndLine: pos(n.End())})
					}
				}
			}
		}
	}
	return syms, true
}

// genericSymbols applies language-agnostic regexes for a heuristic structural
// index. Results are flagged Heuristic.
var genericPatterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"func", regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`)},
	{"func", regexp.MustCompile(`^\s*(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_][\w]*)`)},
	{"def", regexp.MustCompile(`^\s*def\s+([A-Za-z_][\w]*)`)},
	{"class", regexp.MustCompile(`^\s*(?:export\s+)?(?:public\s+|abstract\s+|final\s+)*class\s+([A-Za-z_][\w]*)`)},
	{"type", regexp.MustCompile(`^\s*(?:export\s+)?(?:interface|type|struct|enum|trait)\s+([A-Za-z_][\w]*)`)},
	{"func", regexp.MustCompile(`^\s*(?:public|private|protected|static|\s)*[\w<>\[\]]+\s+([A-Za-z_]\w*)\s*\([^;{]*\)\s*\{`)},
	{"const", regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=`)},
}

func genericSymbols(path string, content []byte) []Symbol {
	lines := splitLines(content)
	var syms []Symbol
	seen := map[string]bool{}
	for i, line := range lines {
		if len(line) > 500 {
			continue
		}
		for _, p := range genericPatterns {
			if m := p.re.FindStringSubmatch(line); m != nil {
				key := m[1] + ":" + p.kind
				if seen[key] {
					continue
				}
				seen[key] = true
				syms = append(syms, Symbol{
					Name: m[1], Kind: p.kind, Path: path,
					StartLine: i + 1, EndLine: i + 1,
					Signature: trimLine(strings.TrimSpace(line)), Heuristic: true,
				})
				break
			}
		}
	}
	return syms
}
