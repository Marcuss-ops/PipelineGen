// Package scan — percheck_governance_artifacts_scanners.go: the two
// enforcement-surface checks of the governance-artifact gate that look at the
// SCANNERS rather than at the artifacts they read.
//
// percheck_governance_artifacts.go validates the artifact ledger (orphan
// allowlists, dangling references, ghost entries, expired deadlines). This file
// validates the gate definitions themselves, because a scanner can go green for
// the wrong reason and a dead gate is indistinguishable from a satisfied
// invariant:
//
//	rule_id_not_single_owner — one rule id declared in one file and emitted as
//	  a literal from a different file (the shape that let two independent
//	  scanners, a `map[string]any` ban and the metadata-KEY registry, share
//	  `percheck_metadata_registry`; architecture/policy.yaml promoted that id to
//	  a hard gate, so the promotion silently covered both and the report could
//	  not tell an operator which one fired).
//
//	scan_root_missing — a scanner walks a literal directory that does not exist
//	  in the worktree. filepath.WalkDir on a missing path returns immediately, so
//	  the check walks zero files, emits zero violations and reports "passed" for
//	  an invariant nobody enforces. This is how `percheck_metadata_registry`
//	  stayed green while its declared scope, <root>/internal/domain, had been
//	  deleted weeks earlier, and how its nine-entry allowlist became nine
//	  unmatchable ghost entries.
//
// Both are reported under the single `percheck_governance_artifacts` rule id:
// there is one owner for the "the enforcement layer is subject to godlike/06
// too" fact, and these are two of its checks. Creating a second gate for the
// same fact would reproduce the duplication this check exists to catch.
//
// KNOWN LIMITATION: a scanner that resolves its walk root through a function
// parameter, a struct field or a computed expression is not resolved here and
// is therefore not audited. The resolver covers literal roots, local
// assignments, package-level constants and range-over-string-literal bindings —
// every shape used by the registered scanners today.
package governance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// governanceGateSrcRoot is the tree that holds the gate definitions.
const governanceGateSrcRoot = "cmd/archcheck"

// governanceRuleIDLiteralRE matches a standalone Go string literal that is a
// rule id. Requiring a full-literal match is what keeps prose out of the
// detector: a note that merely mentions another gate by name inside a longer
// sentence is not a standalone literal.
var governanceRuleIDLiteralRE = regexp.MustCompile(`^percheck_[a-z0-9_]+$`)

// absentScanRoots lists scan roots whose ABSENCE is the pass state: forward-
// prevention bans on a retired path, where a walk that yields nothing IS the
// enforced invariant. These are the only legitimate reason for a registered
// scanner to walk a path that is not on disk, and the declaration is an
// assertion, not an exemption: the gate verifies every entry is still absent
// and fails closed the moment a declared-absent path reappears (at which point
// the owning ban scanner is the thing that must be trusted, not this list).
//
// A root that is NOT declared here and does not exist is a defect: it means the
// scanner migrated away from a deleted tree and nobody noticed, which is
// exactly how `percheck_metadata_registry` stayed green while its declared
// scope, <root>/internal/domain, had been deleted weeks earlier.
var absentScanRoots = map[string]string{
	"internal/application/assets/monitor":   "percheck_monitor_infra_import (retired monitor package; the live package is internal/capabilities/assets/monitor)",
	"internal/platform/sqlite/assets/clips": "percheck_sqlite_assets_clips_duplicate (retired duplicate SQLite clips repository; the surviving idempotency adapter lives at internal/platform/sqlite/idempotency)",
	// C2-C (cmd/archcheck/gates/gate_c2_source_catalog_only_main.go) walks the
	// retired migration-only roots. Their absence IS the pass state: the gate
	// detects any source-kind dispatch that would reappear inside them.
	"internal/application": "gate_c2_source_catalog_only (retired migration-only root; its absence is the C2-C pass state)",
	"internal/api":         "gate_c2_source_catalog_only (retired migration-only root; its absence is the C2-C pass state)",
}

// governanceDispatchIsEmission reports whether the literal at this position is a
// `Rule:` emission rather than a declaration. Emissions are compare-only here:
// the duplicate detector flags a rule id that is DECLARED in one file and
// EMITTED from another, so a single fact legitimately emitted by two scanners
// through one shared owner is not reported.
func scanRuleIDOwnership(root string, r *report.Report) {
	declFiles := map[string]map[string]bool{}
	emitFiles := map[string]map[string]bool{}

	for _, path := range governanceGateSources(root) {
		rel := governanceRelSlash(root, path)
		// checks.go holds the registry of check NAMES: one name per check is the
		// intended shape, so it is not a rule-id declaration site.
		if rel == "cmd/archcheck/checks.go" {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.GenDecl:
				if node.Tok != token.CONST && node.Tok != token.VAR {
					return true
				}
				for _, spec := range node.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, value := range vs.Values {
						if id, ok := governanceRuleIDLiteral(value); ok {
							addGovernanceSite(declFiles, id, rel)
						}
					}
				}
			case *ast.KeyValueExpr:
				key, ok := node.Key.(*ast.Ident)
				if !ok || key.Name != "Rule" {
					return true
				}
				if id, ok := governanceRuleIDLiteral(node.Value); ok {
					addGovernanceSite(emitFiles, id, rel)
				}
			}
			return true
		})
	}

	for _, id := range governanceSortedKeys(declFiles) {
		declared := declFiles[id]
		emitted := emitFiles[id]

		if len(declared) >= 2 {
			emitGovernanceViolation(r, governanceGateSrcRoot, 0, "rule_id_not_single_owner",
				fmt.Sprintf("rule id %q is declared as a constant/variable in %d files (%s). One rule id must have one declaration site: two declarations drift independently and make the policy hard-gate list and the CI report ambiguous. Move the id into a single owner and reference it.",
					id, len(declared), strings.Join(governanceSortedSet(declared), ", ")))
			continue
		}
		for _, file := range governanceSortedSet(declared) {
			for _, emitFile := range governanceSortedSet(emitted) {
				if emitFile == file {
					continue
				}
				emitGovernanceViolation(r, emitFile, 0, "rule_id_not_single_owner",
					fmt.Sprintf("rule id %q is declared in %s but emitted from %s. Two gate sources claiming one rule id means a hard-gate promotion, a rename or a dashboard query silently covers both. Export the declaring constant and reference it, or give the second scanner its own id.",
						id, file, emitFile))
			}
		}
	}
}

// scanGateScanRoots resolves the directory literal passed to
// filepath.WalkDir / filepath.Walk / os.ReadDir in every gate source and reports
// the call when that directory is absent (or is not a directory) in the
// worktree, plus any canonical file read whose literal target is absent.
func scanGateScanRoots(root string, r *report.Report) {
	for _, path := range governanceGateSources(root) {
		rel := governanceRelSlash(root, path)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}
		scope := map[string][]string{}

		ast.Inspect(file, func(n ast.Node) bool {
			// Bind scope before visiting children so a call inside an
			// assignment or a range body can resolve its argument.
			bindGovernanceLiteralScope(n, scope)

			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			wantDir, wantFile := false, false
			switch governanceCalleeName(call.Fun) {
			case "filepath.WalkDir", "filepath.Walk", "os.ReadDir":
				wantDir = true
			case "os.ReadFile":
				wantFile = true
			default:
				return true
			}

			line := fset.Position(call.Pos()).Line
			for _, lit := range resolveGovernanceLiteralPaths(call.Args[0], scope) {
				if lit == "" || lit == "." || strings.ContainsAny(lit, "*?") {
					continue
				}
				info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(lit)))
				if reason, declaredAbsent := absentScanRoots[lit]; declaredAbsent {
					if statErr == nil {
						emitGovernanceViolation(r, rel, line, "absent_root_declaration_stale",
							fmt.Sprintf("the scan root %q is declared absent for %s, but it exists in the worktree again. Remove the absent-root declaration and let the owning ban scanner do the enforcing (the declaration now hides a live path).", lit, reason))
					}
					continue
				}
				if statErr != nil {
					if !os.IsNotExist(statErr) {
						continue
					}
					if wantDir {
						emitGovernanceViolation(r, rel, line, "scan_root_missing",
							fmt.Sprintf("scanner walks %q, which does not exist in the worktree. WalkDir returns immediately on a missing path, so this check walks zero files and reports passed for an invariant nothing enforces. Rescope it to a live target root, or delete the check together with its allowlist.", lit))
					} else if wantFile {
						emitGovernanceViolation(r, rel, line, "scan_root_missing",
							fmt.Sprintf("scanner reads %q, which does not exist in the worktree. The read degrades to the error path, so either the artifact moved or this check is dead.", lit))
					}
					continue
				}
				if wantDir && !info.IsDir() {
					emitGovernanceViolation(r, rel, line, "scan_root_missing",
						fmt.Sprintf("scanner walks %q, which is a file, not a directory. WalkDir yields nothing and the check cannot fire.", lit))
				}
			}
			return true
		})
	}
}

// ── helpers ───────────────────────────────────────────────────────────────

// governanceGateSources returns every non-test .go file under the gate tree.
func governanceGateSources(root string) []string {
	dir := filepath.Join(root, governanceGateSrcRoot)
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if policy.StandardSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	sort.Strings(out)
	return out
}

// governanceRuleIDLiteral returns the rule-id value of a standalone string
// literal expression.
func governanceRuleIDLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil || !governanceRuleIDLiteralRE.MatchString(v) {
		return "", false
	}
	return v, true
}

func addGovernanceSite(m map[string]map[string]bool, id, file string) {
	if m[id] == nil {
		m[id] = map[string]bool{}
	}
	m[id][file] = true
}

// bindGovernanceLiteralScope records identifier → literal repo paths for the
// shapes the registered scanners use: package-level const/var declarations,
// local `:=`/`=` assignments and `for … := range []string{…}` bindings.
func bindGovernanceLiteralScope(n ast.Node, scope map[string][]string) {
	switch node := n.(type) {
	case *ast.GenDecl:
		for _, spec := range node.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				if paths := resolveGovernanceLiteralPaths(vs.Values[i], scope); len(paths) > 0 {
					scope[name.Name] = paths
				}
			}
		}
	case *ast.AssignStmt:
		for i, lhs := range node.Lhs {
			if i >= len(node.Rhs) {
				continue
			}
			ident, ok := lhs.(*ast.Ident)
			if !ok {
				continue
			}
			if paths := resolveGovernanceLiteralPaths(node.Rhs[i], scope); len(paths) > 0 {
				scope[ident.Name] = paths
			}
		}
	case *ast.RangeStmt:
		ident, ok := node.Value.(*ast.Ident)
		if !ok {
			return
		}
		if paths := resolveGovernanceLiteralPaths(node.X, scope); len(paths) > 0 {
			scope[ident.Name] = paths
		}
	}
}

// resolveGovernanceLiteralPaths resolves an expression to the set of
// repo-relative literal paths it can denote, or nil when it cannot be resolved.
// Returning nil (rather than a guess) is the fail-open direction documented in
// the file header.
func resolveGovernanceLiteralPaths(e ast.Expr, scope map[string][]string) []string {
	switch expr := e.(type) {
	case *ast.BasicLit:
		if expr.Kind != token.STRING {
			return nil
		}
		v, err := strconv.Unquote(expr.Value)
		if err != nil {
			return nil
		}
		return []string{v}
	case *ast.Ident:
		if expr.Name == "root" {
			// `root` is the scan root itself, always present.
			return []string{"."}
		}
		return scope[expr.Name]
	case *ast.ParenExpr:
		return resolveGovernanceLiteralPaths(expr.X, scope)
	case *ast.CallExpr:
		switch governanceCalleeName(expr.Fun) {
		case "filepath.FromSlash", "filepath.ToSlash", "filepath.Clean":
			if len(expr.Args) == 1 {
				return resolveGovernanceLiteralPaths(expr.Args[0], scope)
			}
		case "filepath.Join":
			if len(expr.Args) < 2 {
				return nil
			}
			// The first component must be the scan root; every following
			// component must resolve to literal segments, otherwise the result
			// would be a guess.
			base := resolveGovernanceLiteralPaths(expr.Args[0], scope)
			if len(base) != 1 || base[0] != "." {
				return nil
			}
			out := []string{""}
			for _, arg := range expr.Args[1:] {
				parts := resolveGovernanceLiteralPaths(arg, scope)
				if len(parts) == 0 {
					return nil
				}
				var next []string
				for _, prefix := range out {
					for _, part := range parts {
						if prefix == "" {
							next = append(next, part)
							continue
						}
						next = append(next, prefix+"/"+part)
					}
				}
				out = next
			}
			return out
		}
	case *ast.CompositeLit:
		// []string{"a", "b"} — the range source of a multi-root scanner.
		var out []string
		for _, elt := range expr.Elts {
			paths := resolveGovernanceLiteralPaths(elt, scope)
			if len(paths) == 0 {
				return nil
			}
			out = append(out, paths...)
		}
		return out
	}
	return nil
}

// governanceCalleeName returns "pkg.Func" for a selector call and "Func" for a
// plain identifier call.
func governanceCalleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			return id.Name + "." + f.Sel.Name
		}
		return f.Sel.Name
	case *ast.Ident:
		return f.Name
	}
	return ""
}

func governanceRelSlash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func governanceSortedKeys(m map[string]map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func governanceSortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
