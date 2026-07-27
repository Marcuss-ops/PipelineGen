// Package scan — Step 9.c forward-prevention gate (July 2026,
// handler-purity SSOT enforcement): the canonical HandlerGenerate
// struct (POST /api/script/generate) must own ONLY the canonical
// handler-port field set {submitter, validator, log, application-port}.
//
// godlike/06 SSOT rationale:                       +----------------+
//
//	internal/api/ owns transport only. The +---------------->+ HandlerGenerate
//	canonical /generate handler is a thin  | submitter        |
//	delegate to internal/application/typed | scriptgenSvc     |
//	ports. Any field NOT in the allowed     | factory          |
//	set means the transport has drifted     | log              |
//	into owning application-layer concerns  | validator        |
//	(godlike/07 NO-FAKE-AVAILABILITY regime). +----------------+
//
// godlike/07 fail-closed: a missing HandlerGenerate type
// declaration emits a single SeverityError under rule id
// `percheck_handler_generate_fields_decl_missing` so the
// runner's hard-gate promotion escalates it.
//
// AST-walked (vs the simpler line-scan used by other percheck
// gates). The HandlerGenerate declaration is the ONLY struct
// shape we police — float additions to ScriptFlowHandler /
// HandlerShorts still trip godlike/06 SSOT through the wider
// file-size + struct-deps gates (ScanStructDeps).
package scan

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// handlerGenerateFieldScanScope is the SINGLE file we audit
// (godlike/06 SSOT: one owner per fact). The canonical
// script.generate handler struct lives in handler_generate_handler.go.
const handlerGenerateFieldScanScope = "internal/api/script/handler_generate_handler.go"

// handlerGenerateFieldRule is the rule-family id.
const handlerGenerateFieldRule = "percheck_handler_generate_fields"

// handlerGenerateFieldNote is the canonical violation Note.
// Kept terse so the JSON report stays small, with a forward-
// pointer at the bottom that explains the remediation pattern.
const handlerGenerateFieldNote = "forbidden field in HandlerGenerate (godlike/07 NO-FAKE-AVAILABILITY handler purity, July 2026)." +
	" The canonical /generate handler must own ONLY the allowed port set {submitter, validator, log, application-port}." +
	" Application-ports are typed fields whose canonical ownership is internal/application/*/* or internal/scriptgeneration/*." +
	" Move the field OUT of HandlerGenerate: fold the responsibility into an existing application-port, OR extract a typed port into internal/application/scripts/usecase, OR relocate to a sibling handler (HandlerShorts / JobsHandler) per the FASE 2 split topology."

// applicationPortPrefixes is the BROAD application-port
// category. Any field typed under one of these canonical
// package prefixes (incl. pointer-prefix * and selector-style
// `pkg.Type`) counts as an application-port.
//
// godlike/06 SSOT: the list is closed & audited. New canonical
// application-tier packages require an explicit addition here
// (the gate is otherwise the mechanism that locks the
// transport boundary).
var applicationPortPrefixes = []string{
	"submission.", // internal/application/scripts/submission (PR-SUBMISSION-FACTORY)
	"scriptgen.",  // internal/scriptgeneration (GenerationRunStarter, RunRepository)
	"usecase.",    // internal/application/scripts/usecase (PayloadValidator, GenerateOneUseCase)
	"adapters.",   // internal/application/scripts/adapters (registry surfaces)
	"opsapp.",     // internal/application/operations (SubmitRequest, SubmitResult)
	"ops.",        // alias / shorthand of opsapp
}

// handlerGenerateFieldHardAllowedNames is the strict-named
// exception set. Fields by these names are ALWAYS allowed
// regardless of type — they are the canonical /generate
// handler's irreducible concerns.
//
// godlike/06 SSOT: the set is closed. Adding a new name here
// is a deliberate boundary decision; promoting a name from
// here to a typed application-port must occur in lockstep.
var handlerGenerateFieldHardAllowedNames = map[string]bool{
	"submitter": true, // canonical submission port (interface from this package)
	"validator": true, // payload-validator port (PR-AZIONE-1)
	"log":       true, // canonical zap.Logger
}

// handlerGenerateFieldMissingDeclRule is the fail-closed rule-id
// extension when the canonical HandlerGenerate struct is missing
// from the audit target file.
const handlerGenerateFieldMissingDeclRule = handlerGenerateFieldRule + "_decl_missing"

// ScanHandlerGenerateFields audits the HandlerGenerate struct
// in the canonical /generate handler file. Any field whose
// name is NOT in the strict-named set AND whose type is NOT
// an application-port (broad category) is a SeverityError
// violation. A missing type declaration emits one fail-closed
// SeverityError.
//
// Has no productionOnly interaction: the AST walk produces no
// comment-only residue (comments are NOT parsed as field
// declarations).
func ScanHandlerGenerateFields(root string, pol *policy.Policy, r *report.Report) {
	_ = pol

	full := root + "/" + handlerGenerateFieldScanScope

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, full, nil, parser.ParseComments)
	if err != nil {
		// File missing or unparsable. The wider
		// file_size/pkg_size gates cover the \"file
		// does not exist\" case. Stay silent here so
		// the report is not duplicated.
		return
	}

	hgType := findHandlerGenerateType(file, fset, r)
	if hgType == nil {
		// Already emitted the fail-closed violation
		// inside findHandlerGenerateType if needed.
		return
	}

	for _, field := range hgType.Fields.List {
		// Embedded fields (no Names). On HandlerGenerate
		// these are godlike/07 NO-FAKE-AVAILABILITY
		// boundary violations: every field must have
		// a name + a canonical owner.
		if field.Names == nil {
			r.Violations = append(r.Violations, report.Violation{
				File:        handlerGenerateFieldScanScope,
				Line:        fset.Position(field.Pos()).Line,
				MatchedRule: "handler_generate_embedded_field",
				Rule:        handlerGenerateFieldRule,
				Severity:    string(report.SeverityError),
				Note:        handlerGenerateFieldNote + " | snippet: embedded field (no identifier)",
			})
			continue
		}

		typeStr := typeNameFromAST(field.Type)
		for _, name := range field.Names {
			if handlerGenerateFieldHardAllowedNames[name.Name] {
				continue
			}
			if isApplicationPort(typeStr) {
				continue
			}
			r.Violations = append(r.Violations, report.Violation{
				File:        handlerGenerateFieldScanScope,
				Line:        fset.Position(name.Pos()).Line,
				MatchedRule: "handler_generate_forbidden_field",
				Rule:        handlerGenerateFieldRule,
				Severity:    string(report.SeverityError),
				Note: handlerGenerateFieldNote +
					" | snippet: field=" + name.Name + " type=" + typeStr,
			})
		}
	}
}

// findHandlerGenerateType locates the HandlerGenerate struct
// type declaration in the parsed file. Emits a single
// fail-closed violation if the declaration is absent.
func findHandlerGenerateType(file *ast.File, fset *token.FileSet, r *report.Report) *ast.StructType {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if ts.Name.Name != "HandlerGenerate" {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			return st
		}
	}

	// No HandlerGenerate type in the canonical file.
	r.Violations = append(r.Violations, report.Violation{
		File:        handlerGenerateFieldScanScope,
		Line:        fset.Position(file.Pos()).Line,
		MatchedRule: "handler_generate_decl_missing",
		Rule:        handlerGenerateFieldMissingDeclRule,
		Severity:    string(report.SeverityError),
		Note: "fail-closed: HandlerGenerate type declaration not found in " +
			handlerGenerateFieldScanScope +
			" — the canonical /generate handler is missing its struct declaration;" +
			" either restore the anchor or move the gate's target via policy.yaml",
	})
	return nil
}

// typeNameFromAST renders the canonical type string for an
// ast.Expr. Handles the shapes the real codebase uses:
//
//	*ast.Ident              → \"log\"
//	*ast.StarExpr           → \"*scriptgen.GenerationRunStarter\"
//	*ast.SelectorExpr       → \"opsapp.SubmitRequest\"
//	*ast.ArrayType          → \"[]X\"
//	*ast.MapType            → \"map[K]V\"
//	(nested, recursive)
//
// Anything else falls back to a fmt %T render so a regression
// emits an obviously-shaped (and informative) violation rather
// than silently go un-evaluated.
func typeNameFromAST(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + typeNameFromAST(e.X)
	case *ast.SelectorExpr:
		return typeNameFromAST(e.X) + "." + e.Sel.Name
	case *ast.ArrayType:
		return "[]" + typeNameFromAST(e.Elt)
	case *ast.MapType:
		return "map[" + typeNameFromAST(e.Key) + "]" + typeNameFromAST(e.Value)
	default:
		return fmt.Sprintf("<%T>", e)
	}
}

// isApplicationPort reports whether `typeStr` is from a
// canonical application-port package. We test PREFIX match
// (NOT suffix) so package aliases like `scriptgen` or
// `opsapp` are caught at their canonical prefix. Pointer-
// qualified strings (e.g. `*scriptgen.X`) are tested against
// the post-`*` prefix AND the raw string starting with `*`
// for the rare case where the trimmed form is identical
// to the un-trimmed form.
func isApplicationPort(typeStr string) bool {
	s := strings.TrimPrefix(typeStr, "*")
	for _, p := range applicationPortPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
		if strings.HasPrefix(typeStr, "*"+p) {
			return true
		}
	}
	return false
}
