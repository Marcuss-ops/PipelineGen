package iobinder

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Violation represents a direct I/O binding found by the AST scanner.
// Line is 1-indexed and matches the position of the offending AST node.
type Violation struct {
	File   string // repo-relative path (forward-slash)
	Line   int    // 1-indexed line number
	Symbol string // canonical symbol, e.g. "os.Open" or "database/sql"
}

// ScannerConfig configures which files the AST scanner skips.
type ScannerConfig struct {
	// Root is the directory used to compute relative file paths.
	// If empty, absolute paths are reported.
	Root string
	// SkipSubstrings is a list of path substrings that cause a file to be skipped.
	SkipSubstrings []string
}

// ScanDirectory walks the given directory, parses every production .go file
// (non-_test.go), and returns all forbidden symbols found.
func ScanDirectory(dir string, symbols []string, cfg ScannerConfig) ([]Violation, error) {
	var all []Violation
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		for _, sub := range cfg.SkipSubstrings {
			if strings.Contains(filepath.ToSlash(path), sub) {
				return nil
			}
		}
		v, err := ScanFile(path, symbols, cfg.Root)
		if err != nil {
			return err
		}
		all = append(all, v...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

// ScanFile parses a single Go file and returns violations for the given
// canonical forbidden symbols.
func ScanFile(path string, symbols []string, root string) ([]Violation, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	rel := path
	if root != "" {
		if r, err := filepath.Rel(root, path); err == nil {
			rel = filepath.ToSlash(r)
		}
	}

	// importOnly tracks import paths that are forbidden on their own.
	importOnly := map[string]bool{}
	// callRules maps import path -> selector name -> canonical symbol.
	callRules := map[string]map[string]string{}
	// defaultAlias maps an import path to its conventional short alias.
	defaultAlias := map[string]string{}

	for _, s := range symbols {
		if s == "database/sql" {
			importOnly["database/sql"] = true
			defaultAlias["database/sql"] = "sql"
			continue
		}
		parts := strings.Split(s, ".")
		if len(parts) != 2 {
			continue
		}
		var importPath string
		switch parts[0] {
		case "os":
			importPath = "os"
		case "sql":
			importPath = "database/sql"
		}
		if importPath == "" {
			continue
		}
		defaultAlias[importPath] = parts[0]
		if callRules[importPath] == nil {
			callRules[importPath] = map[string]string{}
		}
		callRules[importPath][parts[1]] = s
	}

	aliasToPath := map[string]string{}

	var violations []Violation

	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ImportSpec:
			path := strings.Trim(x.Path.Value, `"`)
			if importOnly[path] {
				pos := fset.Position(x.Pos())
				violations = append(violations, Violation{
					File:   rel,
					Line:   pos.Line,
					Symbol: "database/sql",
				})
			}
			// Record alias -> import path for selector resolution.
			alias := defaultAlias[path]
			if x.Name != nil && x.Name.Name != "" {
				alias = x.Name.Name
			}
			if alias != "" {
				aliasToPath[alias] = path
			}
		case *ast.SelectorExpr:
			ident, ok := x.X.(*ast.Ident)
			if !ok {
				return true
			}
			importPath, ok := aliasToPath[ident.Name]
			if !ok {
				return true
			}
			selRules := callRules[importPath]
			if selRules == nil {
				return true
			}
			sym, ok := selRules[x.Sel.Name]
			if !ok {
				return true
			}
			pos := fset.Position(x.Pos())
			violations = append(violations, Violation{
				File:   rel,
				Line:   pos.Line,
				Symbol: sym,
			})
		}
		return true
	})

	return violations, nil
}

// AllowlistEntry is one allowed direct-IO binding with the required
// ownership metadata. The canonical key is Path:Symbol.
type AllowlistEntry struct {
	Path      string
	Symbol    string
	Owner     string
	Deadline  string
	Rationale string
	Seen      bool // populated during scan
}

// Allowlist is keyed by "<path>:<symbol>".
type Allowlist map[string]*AllowlistEntry

// LoadAllowlist parses a tab-separated allowlist file.
// Each non-comment line must have 5 fields: path symbol owner deadline rationale.
// It returns an map keyed by "path:symbol".
func LoadAllowlist(path string) (Allowlist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read allowlist %s: %w", path, err)
	}
	out := make(Allowlist)
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 5 {
			return nil, fmt.Errorf("%s:%d: allowlist line must have 5 tab-separated fields (path symbol owner deadline rationale): %q", path, i+1, line)
		}
		entry := &AllowlistEntry{
			Path:      strings.TrimSpace(parts[0]),
			Symbol:    strings.TrimSpace(parts[1]),
			Owner:     strings.TrimSpace(parts[2]),
			Deadline:  strings.TrimSpace(parts[3]),
			Rationale: strings.TrimSpace(parts[4]),
		}
		if entry.Path == "" || entry.Symbol == "" || entry.Owner == "" || entry.Deadline == "" || entry.Rationale == "" {
			return nil, fmt.Errorf("%s:%d: allowlist fields must be non-empty: %q", path, i+1, line)
		}
		key := entry.Path + ":" + entry.Symbol
		if _, ok := out[key]; ok {
			return nil, fmt.Errorf("%s:%d: duplicate allowlist key %s", path, i+1, key)
		}
		out[key] = entry
	}
	return out, nil
}
