// Package aliasgen builds, verifies and writes the generated facade over the
// overlay model leaf. It is the only writer of
// internal/capabilities/overlays/model_alias.go and the only reader that
// decides which exported model symbols must be aliased; the command in the
// parent directory is a thin flag/exit-code shell around it.
package aliasgen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
)

// Paths are relative to the module root (the working directory of the command).
const (
	moduleRoot    = "."
	modelDir      = "internal/capabilities/overlays/model"
	facadePath    = "internal/capabilities/overlays/model_alias.go"
	modelImporter = "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// ExitError carries the process exit code the command must return: 1 for facade
// drift (the fail-closed gate result), 2 for an I/O or parse failure.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }

func (e *ExitError) Unwrap() error { return e.Err }

// symKind classifies one package-level model symbol.
type symKind string

const (
	kindType  symKind = "type"
	kindConst symKind = "const"
	kindVar   symKind = "var"
)

// Run verifies the checked-in facade, or rewrites it when generate is set. A
// drift is reported as an ExitError with Code 1 so the command exits 1.
func Run(generate bool) error {
	content, err := render()
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}

	if generate {
		if err := atomicwrite.WriteFile(facadePath, content, 0o644); err != nil {
			return &ExitError{Code: 2, Err: fmt.Errorf("write %s: %w", facadePath, err)}
		}
		fmt.Printf("overlay-model-alias-gen: regenerated %s\n", facadePath)
		return nil
	}

	existing, err := os.ReadFile(facadePath)
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}
	if !bytes.Equal(existing, content) {
		return &ExitError{Code: 1, Err: fmt.Errorf("%s drifted from %s — run `go run ./cmd/overlay-model-alias-gen --generate`", facadePath, modelDir)}
	}
	fmt.Printf("overlay-model-alias-gen: facade in sync (%s)\n", facadePath)
	return nil
}

// render rebuilds the facade: every EXPORTED model symbol that is referenced
// outside the model package becomes an alias in package overlays. Methods are
// not aliased (they travel with their type), and the facade under generation is
// never counted as a consumer of itself.
func render() ([]byte, error) {
	symbols, err := modelSymbols()
	if err != nil {
		return nil, err
	}
	used, err := usedOutsideModel(symbols)
	if err != nil {
		return nil, err
	}

	var types, consts, vars []string
	for name, kind := range symbols {
		if !used[name] {
			continue
		}
		switch kind {
		case kindType:
			types = append(types, name)
		case kindConst:
			consts = append(consts, name)
		case kindVar:
			vars = append(vars, name)
		}
	}
	sort.Strings(types)
	sort.Strings(consts)
	sort.Strings(vars)

	var b bytes.Buffer
	b.WriteString("package overlays\n\n")
	fmt.Fprintf(&b, "import capmodel %q\n\n", modelImporter)
	// Names the planning layer uses in their unexported model spelling cannot be
	// aliased by scan (the model side is exported, the planner side is not), so
	// they are pinned here: keep them in sync by hand with the model package.
	b.WriteString("// Model-side limits re-exported under their planner-facing unexported names.\n")
	b.WriteString("const (\n\tmaxMapRasterWidth  = capmodel.MaxMapRasterWidth\n\tmaxMapRasterHeight = capmodel.MaxMapRasterHeight\n)\n\n")
	b.WriteString("// intentID aliases the model-level id derivation (exported IntentID).\n")
	b.WriteString("var intentID = capmodel.IntentID\n\n")
	b.WriteString("// overlay_model_alias.go is the GENERATED facade over the model leaf.\n")
	b.WriteString("// It re-exports exactly the model identifiers referenced outside the model\n")
	b.WriteString("// package (overlays planning layer, scripts, platform, plus overlay tests).\n")
	b.WriteString("// Regenerate by rescanning, never by hand-editing types.\n")
	b.WriteString("// Regenerate with: go run ./cmd/overlay-model-alias-gen --generate\n")

	writeBlock := func(keyword string, names []string) {
		if len(names) == 0 {
			return
		}
		b.WriteString("\n" + keyword + " (\n")
		for _, name := range names {
			fmt.Fprintf(&b, "\t%s = capmodel.%s\n", name, name)
		}
		b.WriteString(")\n")
	}
	writeBlock("type", types)
	writeBlock("const", consts)
	writeBlock("var", vars)

	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format facade: %w", err)
	}
	return formatted, nil
}

// modelSymbols maps every exported package-level type/const/var/func declared in
// the model package to the block it must be aliased in. Functions and variables
// both alias as `var` (a call site keeps calling through the alias).
func modelSymbols() (map[string]symKind, error) {
	fset := token.NewFileSet()
	symbols := map[string]symKind{}

	entries, err := os.ReadDir(modelDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", modelDir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(modelDir, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				// Methods stay with their type in the model package.
				if d.Recv != nil && len(d.Recv.List) > 0 {
					continue
				}
				if ast.IsExported(d.Name.String()) {
					symbols[d.Name.String()] = kindVar
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if ast.IsExported(s.Name.String()) {
							symbols[s.Name.String()] = kindType
						}
					case *ast.ValueSpec:
						kind := kindVar
						if d.Tok == token.CONST {
							kind = kindConst
						}
						for _, name := range s.Names {
							if ast.IsExported(name.String()) {
								symbols[name.String()] = kind
							}
						}
					}
				}
			}
		}
	}
	return symbols, nil
}

// usedOutsideModel walks the module and reports which model symbols are
// referenced by an identifier outside the model package. The facade itself is
// excluded: it is the artifact under generation, and counting its own aliases as
// usages would make the alias set grow monotonically and never shrink.
func usedOutsideModel(symbols map[string]symKind) (map[string]bool, error) {
	fset := token.NewFileSet()
	used := map[string]bool{}
	facade := filepath.Clean(facadePath)

	err := filepath.WalkDir(moduleRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			switch name := d.Name(); {
			case name == ".git", name == "out", name == "test_renders", strings.HasPrefix(name, "renders_"):
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		clean := filepath.Clean(path)
		if clean == facade {
			return nil
		}
		if rel, err := filepath.Rel(modelDir, clean); err == nil && !strings.HasPrefix(rel, "..") {
			return nil
		}
		file, err := parser.ParseFile(fset, clean, nil, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok {
				if _, ok := symbols[ident.String()]; ok {
					used[ident.String()] = true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return used, nil
}
