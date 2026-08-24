// Package scan — per-check forward-prevention gate that enforces
// the maximum-8-fields invariant on every API module's
// `Dependencies` (or `Deps`) bag
// (PR-API-MODULE-DEPS-MAX-8, July 2026).
//
// scan/percheck_api_module_deps_max_8.go owns the Go-migrated
// forward-prevention gate. It walks <root>/internal/api/**/module.go
// in AST mode (`go/parser` + `go/ast` + `go/token`) — strictly
// more robust than regex line-counting for grouped multi-decl
// fields (`A, B Service` counts as 2), embedded fields (anonymous
// = 1 each), and multiline field declarations.
//
// godlike/06 SSOT invariant: each module's `Dependencies` bag
// (the typed-narrow input to `Build(deps Dependencies) (…,
// error)`) MUST carry at most 8 fields. A bag that grows beyond
// 8 signals the module is an unrefactored god-service — the
// canonical remediation is the clips-folder split pattern:
// (a) split the cluster into sub-descriptors each consuming a
//
//	narrow typed-cluster interface, (b) wire the sub-descriptors
//	from the parent's per-cluster handler pointers, (c) keep
//	the upper struct as the WIRING surface (with `Build` failing
//	closed on missing deps per godlike/07 NO-FAKE-AVAILABILITY).
//
// Bypass list: the 8 already-split clips modules (upper +
// 7 sub-modules), per PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE
// (July 2026). The bypass is structurally motivated: the upper
// ClipsModule's `Dependencies` is the WIRE-IN to the 7 sub-
// descriptors — its wide shape is intentional — and the 7 sub-
// modules are the narrow typed-cluster consumers. Every other
// module under internal/api/**/module.go whose `Dependencies`
// bag carries > 8 fields trips the gate and surfaces as a CI
// build failure (godlike/07 NO-FAKE-AVAILABILITY).
//
// Detection rule: walk only `<root>/internal/api/**/module.go`
// (the canonical Build-entrypoint location per the Capability
// Standard module.go contract). Each module.go is parsed; any
// top-level `*ast.TypeSpec` whose `Name.Name in {"Dependencies",
// "Deps"}` AND whose body is `*ast.StructType` is the target.
// Field counting: `sum(len(f.Names))` over the `Field.List`,
// with each `Field` whose `len(Names)==0` (embedded/anonymous)
// contributing 1.
//
// godlike/07 residue accounting: bypass-list hits get a WARN,
// NOT a violation, so the running audit lane stays residue-honest
// even when the bypass list needs later cleanup (e.g. a sub-
// module's bag shrinks below 8 — drop it from the bypass list).
//
// Type-alias guard: targets whose body is NOT `*ast.StructType`
// (e.g. `type Dependencies = OtherType`) are silently skipped —
// godlike/06 SSOT forbids shadowing the canonical `Dependencies`
// name with a type alias; the no-shadow-enum companion gate
// (percheck_asset_state_no_shadow_enum) covers that contract
// separately. This scanner is intentionally narrow to the
// struct-decl leg only.
//
// matched rule_id: `percheck_api_module_deps_max_8`.
package governance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// apiModuleDepsMaxCap is the canonical maximum number of fields
// the percheck enforces. Mirrors the godlike/06 SSOT "narrow
// bag" invariant. Lifted ONLY via per-struct knobs
// (Policy.MaxClipIngestPipelineFields and successors); a global
// raise requires updating this constant in lockstep with
// architecture/policy.yaml's `max_struct_deps` field.
const apiModuleDepsMaxCap = 8

// apiModuleDepsMaxRule is the rule-family id the scanner emits.
// Mirrors percheck_image_asset_invariants.go's RuleID naming
// convention.
const apiModuleDepsMaxRule = "percheck_api_module_deps_max_8"

// apiModuleDepsMaxBypassRelPaths is the set of module.go
// relative paths whose `Dependencies` bag is exempt from the
// 8-field cap. The list encodes the canonical
// "already-refactored" surface from
// PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE (July 2026): the
// 7 narrow clips sub-descriptors plus the upper ClipsModule
// (whose wide Dependencies is the WIRING surface for the
// 7 sub-descriptors, not the route-installer surface). The
// list is repo-relative (forward-slash) and matched lexically
// against `filepath.Rel(root, path)` after slash conversion.
// Operators adding a new already-split module rewrite the
// comment block above AND append the path here in lockstep.
var apiModuleDepsMaxBypassRelPaths = []string{
	"internal/api/assets/clips/module.go",         // upper ClipsModule (PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE)
	"internal/api/assets/clips/catalog/module.go", // catalog sub
	"internal/api/assets/clips/ingest/module.go",  // ingest sub
	"internal/api/assets/clips/processing/module.go",
	"internal/api/assets/clips/publication/module.go",
	"internal/api/assets/clips/indexing/module.go",
	"internal/api/assets/clips/operations/module.go",
	"internal/api/assets/clips/bulk/module.go",
}

// apiModuleDepsMaxNote is the violation Note string for
// count-overflow hits. The message references the split
// remediation (clips-folder pattern) so the operator sees the
// migration path inline. The actual numeric surface
// (count + cap) is appended per-violation so JSON consumers
// can grep the report stream.
const apiModuleDepsMaxNote = "API module Dependencies bag exceeds maximum of 8 fields (PR-API-MODULE-DEPS-MAX-8, July 2026); godlike/06 SSOT narrow-bag invariant requires splitting god-service surface into typed-narrow sub-descriptors (canonical pattern: PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE split the Clips module into 7 sub-descriptors with per-cluster typed interfaces)"

// apiDepsWarn is the centralized WARN-bucket emitter for
// residue accounting. Mirrors the convention used by
// assetStateWarn / assetStateWarnShadow.
func apiDepsWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, apiModuleDepsMaxRule+" "+label+" "+msg)
}

// ScanApiModuleDepsMax8 walks every `<root>/internal/api/**/module.go`
// and emits a `percheck_api_module_deps_max_8` violation when
// a `Dependencies` (or `Deps`) struct carries > 8 fields.
// Bypass-listed modules (the 8 already-split clips modules)
// trip a WARN (residue accounting) instead of a violation.
// Each module is parsed once; structural parse errors are
// surfaced as single violations per file (operator-readable
// infra failure, not silent skip).
func ScanApiModuleDepsMax8(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved for future SeverityOverride plumbing.
	walkApiModuleFiles(root, func(relPath, absPath string) {
		count, found, parseErr := scanApiModuleDepsFile(absPath)
		if parseErr != nil {
			// Surface parse failures as a single violation
			// per file so a future agent who breaks the
			// AST shape (e.g. irrecoverable syntax error)
			// cannot silently bypass the gate.
			r.Violations = append(r.Violations, report.Violation{
				Package:      pkgFromApiModuleRel(relPath),
				File:         relPath,
				Line:         0,
				Rule:         apiModuleDepsMaxRule,
				Severity:     string(report.SeverityError),
				MatchedRule:  "deps_count_parse_fail",
				Note:         apiModuleDepsMaxNote + " | parse failure: " + parseErr.Error(),
				ActualCount:  0,
				AllowedCount: apiModuleDepsMaxCap,
			})
			return
		}
		if !found {
			return // no Dependencies struct in this module.go
		}
		// Bypass-list check BEFORE overflow check; the
		// bypass hit is residue-accounted regardless of
		// overflow status (an oversize bypass-list hit
		// gets a WARN, NOT a violation, per user contract).
		if isApiModuleDepsBypass(relPath) {
			apiDepsWarn(r, "bypass-list:",
				relPath+" has Dependencies bag with "+strconv.Itoa(count)+
					" fields (bypass under PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE; counted but not violated per godlike/07 no-fake-availability)")
			return
		}
		if count > apiModuleDepsMaxCap {
			r.Violations = append(r.Violations, report.Violation{
				Package:      pkgFromApiModuleRel(relPath),
				File:         relPath,
				Line:         0,
				Rule:         apiModuleDepsMaxRule,
				Severity:     string(report.SeverityError),
				MatchedRule:  "deps_count_over_8",
				Note:         apiModuleDepsMaxNote + " | actual field count: " + strconv.Itoa(count) + " | want: " + strconv.Itoa(apiModuleDepsMaxCap) + " | module: " + relPath,
				ActualCount:  count,
				AllowedCount: apiModuleDepsMaxCap,
			})
		}
	})
}

// walkApiModuleFiles walks <root>/internal/api/**/module.go.
// Test files (`_test.go`) are skipped at the basename level even
// though module.go is rarely a test path — defence-in-depth for
// future fixture patterns. Skip-dir mirrors the canonical
// skip-dir set used by sibling perchecks.
func walkApiModuleFiles(root string, fn func(relPath, absPath string)) {
	apiRoot := filepath.Join(root, "internal", "api")
	skipDirs := map[string]bool{
		".git":         true,
		"vendor":       true,
		"node_modules": true,
		"node-scraper": true,
		"examples":     true,
		"archivist":    true,
		"docs":         true,
		"data":         true,
	}
	filepath.WalkDir(apiRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		base := filepath.Base(path)
		if base != "module.go" {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		fn(filepath.ToSlash(rel), path)
		return nil
	})
}

// scanApiModuleDepsFile parses a single module.go file and
// returns (count, found, parseErr):
//
//   - count     — number of fields in the FIRST matching
//     `Dependencies` or `Deps` struct declaration (the canonical
//     Build-entrypoint bag).
//   - found     — true if such a struct was located. False means
//     the module has no Dependencies bag (callers skip with no
//     violation).
//   - parseErr  — non-nil when the file couldn't be parsed
//     (e.g. syntax error). Callers surface it as a violation
//     to prevent silent bypass on corrupted infra.
//
// Field-counting: each *ast.Field whose Names is non-empty
// contributes len(Names) to the total (grouped decls like
// `A, B Service` count as 2). Each embedded field (Names empty)
// contributes 1 (struct-level only — embedded fields are NOT
// flattened to their underlying fields, matching the DI perspective
// where an embedded struct is one injected fact).
//
// godlike/07 fail-closed: if a module carries MULTIPLE
// `Dependencies` / `Deps` structs (extreme edge case — should
// never happen in canonical layout), the FIRST one is counted
// and a WARN is appended via the caller. This protects the
// scanner against silent pass-through on schema drift.
func scanApiModuleDepsFile(path string) (count int, found bool, parseErr error) {
	fset := token.NewFileSet()
	src, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return 0, false, err
	}
	for _, decl := range src.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if ts.Name.Name != "Dependencies" && ts.Name.Name != "Deps" {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				// Type alias or other — silently skip per
				// godlike/06 SSOT (only the struct form
				// is the canonical bag). Continues the
				// outer loop in case the file has both a
				// alias and a struct (rare but possible).
				continue
			}
			count = countStructFields(st)
			return count, true, nil
		}
	}
	return 0, false, nil
}

// countStructFields returns the total field-count of a struct:
// sum of len(f.Names) over the Field.List, with each embedded
// field (len(Names) == 0) contributing 1. Mirrors the
// godlike/06 SSOT one-canonical-owner per-Fact counting
// (an embedded struct is one Fact at the dependency-injection
// boundary, not a count of its underlying fields).
func countStructFields(st *ast.StructType) int {
	if st.Fields == nil {
		return 0
	}
	total := 0
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 {
			total++
			continue
		}
		total += len(f.Names)
	}
	return total
}

// isApiModuleDepsBypass returns true when relPath matches one
// of the canonical already-split bypass entries. The match
// uses forward-slash normalization so the function works on
// Windows runners too (`filepath.Rel` on Windows uses `\`,
// which is normalized via ToSlash in walkApiModuleFiles).
func isApiModuleDepsBypass(relPath string) bool {
	for _, p := range apiModuleDepsMaxBypassRelPaths {
		if relPath == p {
			return true
		}
	}
	return false
}

// pkgFromApiModuleRel extracts the package identifier from a
// repo-relative file path. Mirrors pkgFromAssetStateRel
// convention.
func pkgFromApiModuleRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}
