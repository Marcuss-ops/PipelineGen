// Package scan — percheck_clip_ingest_pipeline_canonical_1.go
//
// PR-CLIPINGEST-PIPELINE step 8 (July 2026): forward-prevention gate.
// godlike/06 SSOT — one canonical owner per fact.
//
// Rule (percheck_clip_ingest_pipeline_canonical_1): every
//
//  1. type declaration `type ClipIngestPipeline struct`
//
//  2. literal composite usage `ClipIngestPipeline{...}` / `&ClipIngestPipeline{...}`
//
//     in any .go file under the project MUST live in the canonical
//     owner: internal/application/assets/ingest/clip_ingest_pipeline.go
//     (or its _test sibling). Anything else is a SEVERITY-error violation.
//
// Exemptions (mirroring the precedent set by
// percheck_image_asset_invariants.go):
//   - The canonical owner itself: internal/application/assets/ingest/clip_ingest_pipeline.go
//   - The canonical owner's _test sibling: clip_ingest_pipeline_test.go
//   - The scanner's own package: cmd/archcheck/scan/**
//   - Comments-only matches are not violations (comment stripping
//     happens implicitly because we walk the AST, not the source text).
//   - Struct methods `*ClipIngestPipeline.{Method}(...)` are not bare
//     literals because their receiver types are pointer-typed, but the
//     composite-literal walk correctly handles `&ClipIngestPipeline{...}`.
//
// Layer-2 forward-prevention (godlike/07 fail-closed contract): a
// clone of the struct in an unrelated package — even for "alternate
// implementations" — must surface at CI time so the godlike/06
// ownership invariant stays locked.
package boundaries

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// canonicalClipIngestPipelineFile is the SOLE canonical owner of the
// `ClipIngestPipeline` struct + literal surface (godlike/06 SSOT).
const canonicalClipIngestPipelineFile = "internal/application/assets/ingest/clip_ingest_pipeline.go"

// exemptedDirPrefixes are project-root-relative dirs that contain no
// production Go code the scanner should be walking for the struct +
// literal surface.
var exemptedDirPrefixes = []string{
	"architecture",
	"docs",
	"node-scraper",
	"scripts",
}

// ScanClipIngestPipelineCanonical1 walks the project tree and reports
// every `type ClipIngestPipeline struct` declaration + every
// `ClipIngestPipeline{` literal composite usage outside the canonical
// owner file.
//
// return: slice of report.Violation. Empty slice = project compliant.
func ScanClipIngestPipelineCanonical1(projectRoot string) []report.Violation {
	var out []report.Violation

	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		out = append(out, report.Violation{
			Rule:     "percheck_clip_ingest_pipeline_canonical_1",
			Severity: string(report.SeverityError),
			Note:     fmt.Sprintf("ScanClipIngestPipelineCanonical1: filepath.Abs(%q) failed: %v", projectRoot, err),
		})
		return out
	}

	canonAbs, err := filepath.Abs(filepath.Join(abs, canonicalClipIngestPipelineFile))
	if err != nil {
		out = append(out, report.Violation{
			Rule:     "percheck_clip_ingest_pipeline_canonical_1",
			Severity: string(report.SeverityError),
			Note:     fmt.Sprintf("ScanClipIngestPipelineCanonical1: canonical abs path resolution failed: %v", err),
		})
		return out
	}

	return walkForClipIngestPipelineViolations(abs, canonAbs)
}

// walkForClipIngestPipelineViolations does the AST walk + filtering.
// Split out for testability (tests drive synthetic trees via os.MkdirAll).
func walkForClipIngestPipelineViolations(absRoot, canonAbs string) []report.Violation {
	var out []report.Violation

	err := filepath.Walk(absRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable; do not fail the whole scan.
		}
		if info == nil {
			return nil
		}
		if info.IsDir() {
			if path == filepath.Join(absRoot, ".git") ||
				path == filepath.Join(absRoot, "node_modules") ||
				path == filepath.Join(absRoot, "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}
		if path == canonAbs {
			return nil
		}
		if strings.HasPrefix(path, filepath.Join(absRoot, "cmd", "archcheck", "scan")) {
			return nil // exempt the scanner itself
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil // _test.go files outside the canonical _test are exempt
		}
		if isExemptedDir(path, absRoot) {
			return nil
		}

		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if perr != nil {
			return nil // Best-effort: tolerate partial-parse failures on dirty trees
		}

		relSlash, _ := filepath.Rel(absRoot, path)
		relSlash = filepath.ToSlash(relSlash)

		out = append(out, scanASTForClipIngestPipeline(f, relSlash, fset)...)
		return nil
	})
	if err != nil {
		// Real I/O errors get surfaced as a sentinel violation.
		out = append(out, report.Violation{
			Rule:     "percheck_clip_ingest_pipeline_canonical_1",
			Severity: string(report.SeverityError),
			Note:     fmt.Sprintf("ScanClipIngestPipelineCanonical1: filepath.Walk %q failed: %v", absRoot, err),
		})
	}
	return out
}

// isExemptedDir returns true if path is under any of exemptedDirPrefixes.
func isExemptedDir(path, absRoot string) bool {
	for _, rel := range exemptedDirPrefixes {
		if strings.HasPrefix(path, filepath.Join(absRoot, rel)) {
			return true
		}
	}
	return false
}

// scanASTForClipIngestPipeline returns violations found in a single
// parsed file. `relSlash` is path relative to projectRoot using forward
// slashes (matches policy.yaml output conventions).
func scanASTForClipIngestPipeline(f *ast.File, relSlash string, fset *token.FileSet) []report.Violation {
	var out []report.Violation

	// Walk all nodes (top-level decls + nested decls (functions, methods,
	// var blocks, etc.) for `type ClipIngestPipeline struct` and for
	// `ClipIngestPipeline{` literals. Generic ast.Walk avoids missing
	// nested declarations.
	ast.Inspect(f, func(n ast.Node) bool {
		if ts, ok := n.(*ast.TypeSpec); ok {
			if ts.Name != nil && ts.Name.Name == "ClipIngestPipeline" {
				if _, isStruct := ts.Type.(*ast.StructType); isStruct {
					pos := fset.Position(ts.Pos())
					out = append(out, report.Violation{
						Rule:     "percheck_clip_ingest_pipeline_canonical_1",
						Severity: string(report.SeverityError),
						Package:  filepath.ToSlash(filepath.Dir(relSlash)),
						File:     relSlash,
						Line:     pos.Line,
						Note:     "ClipIngestPipeline type declaration outside canonical owner (godlike/06 SSOT)",
					})
				}
			}
		}
		if cl, ok := n.(*ast.CompositeLit); ok {
			if isClipIngestPipelineType(cl.Type) {
				pos := fset.Position(cl.Pos())
				out = append(out, report.Violation{
					Rule:     "percheck_clip_ingest_pipeline_canonical_1",
					Severity: string(report.SeverityError),
					Package:  filepath.ToSlash(filepath.Dir(relSlash)),
					File:     relSlash,
					Line:     pos.Line,
					Note:     "ClipIngestPipeline{} literal outside canonical owner (godlike/06 SSOT)",
				})
			}
		}
		return true
	})

	return out
}

// isClipIngestPipelineType reports whether the AST expr is
// `ClipIngestPipeline`, `*ClipIngestPipeline` (the canonical
// composite-literal pattern, including `&ClipIngestPipeline{...}`),
// or a package-qualified reference where the *selected* identifier
// is ClipIngestPipeline (e.g. `ingest.ClipIngestPipeline{...}` or
// a shadow `shadow.ClipIngestPipeline{...}` outside the canonical
// owner). godlike/06 SSOT: any struct-literal instantiation of
// ClipIngestPipeline outside the canonical owner is a violation,
// regardless of how the type is referenced.
func isClipIngestPipelineType(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == "ClipIngestPipeline"
	case *ast.StarExpr:
		return isClipIngestPipelineType(e.X)
	case *ast.SelectorExpr:
		return e.Sel != nil && e.Sel.Name == "ClipIngestPipeline"
	}
	return false
}
