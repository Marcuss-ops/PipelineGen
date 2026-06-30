// Package main implements the Blocco C2-C architectural gate (June 2026):
//
//	every SOURCE-KIND DISPATCH (switch on a source-type with
//	canonical-kind case arms, or if/else-if chain whose conditions
//	compare against a canonical source-kind) MUST live in one of
//	the two Source Catalog canonical files:
//
//	  - internal/application/assets/artifacts/source_resolver.go
//	  - internal/application/scripts/adapters/source_registry.go
//
// All other occurrences are hard-fail (exit 1, unless
// --baseline=N ratchets a transitional allowance per AGENTS.md
// "transitional baselines" convention; final target is zero).
//
// This gate is the dispatch-side companion to the Blocco C2-A gate
// (`gate_c2_registry_only.go`) which enforces composition-point
// canon. Together they pin the source-catalog canonical surface to
// exactly two files: the resolver map (lookup dispatch) and the
// registry (per-source-kind resolvers). Any in-place switch / if
// chain that dispatches on a source-kind value outside those two
// files violates the Source Catalog canonical pattern documented in
// godlike/07 (zero-legacy-policy: "no fake availability") +
// godlike/06 (data ownership: Source Catalog is the SSOT for
// source-kind dispatch).
//
// ── Patterns detected ────────────────────────────────────────────────
//
//  1. SwitchStmt with CaseClause.List containing:
//
//     - BasicLit strings matching canonical source-kind values
//     (e.g. `case "artlist":`, `case "youtube":`, `case
//     "sound_effect":`, ...).
//     - SelectorExpr references matching the canonical
//     `scriptpkg.Source*` / `<prefix>.Source*` constant pattern
//     (e.g. `case scriptpkg.SourceCatalog:`, `case
//     asset.SourceArtlist:`).
//     - Ident references matching the unqualified `Source<Kind>`
//     enum-constant pattern (e.g. `case SourceText:`,
//     `case SourceStock,`).
//
//  2. IfStmt.Cond (or recursively IfStmt.Else which is itself an
//     *ast.IfStmt for else-if chains) where the BinaryExpr.EQL/NEQ
//     operand matches any of the canonical source-kind values
//     (string, SelectorExpr, or unqualified Ident).
//
// ── False-positive surface (correctly NOT matched) ──────────────────
//
//   - Comment prose mentioning the patterns (`// case
//     scriptpkg.SourceCatalog ...`) — ast.Inspect does not walk
//     Comment nodes.
//   - String literals OUTSIDE switch case arms + if-conditions
//     (e.g. map key in source_resolver.go's `reg.byCanonical["artlist"]
//     = ...`) — never inspected as case-arm; positional context
//     controls inclusion.
//   - Test fixtures (`*_test.go`) — entire subtree excluded by the
//     file walker.
//   - Generated code (`generated/` subdirectories) — excluded by
//     the walker.
//
// ── Allowlist ───────────────────────────────────────────────────────
//
// Production code in these two files is exempted:
//
//   - internal/application/assets/artifacts/source_resolver.go
//     (SourceCatalog central source-metadata + typed-port dispatcher
//     for the asset-side namespace).
//
//   - internal/application/scripts/adapters/source_registry.go
//     (SourceRegistry map SourceType -> SourceResolver for the
//     script-side namespace).
//
// New canonical-file additions MUST be done via the EXPAND →
// BACKFILL → CUTOVER sequence documented in godlike/07; updating
// this allowlist requires a co-equal entry in
// architecture/capability_inventory.yaml::gates_baseline::C2-C
// with owner + deadline. The gate intentionally treats the
// allowlist as a closed set at compile-time (var ... = "; the
// walker compares repo-relative paths against this list during
// traversal).
//
// ── Transitional baseline ──────────────────────────────────────────
//
// Per AGENTS.md "transitional baselines" convention (canonical
// example: WithoutCancel gate, 8 unlisted sites → 0 by 2026-07-15),
// this gate supports a `--baseline=N` flag that ratchets a
// documented transitional allowance. The live count
// (gates_baseline::C2-C::baseline_current) MUST monotonically
// decrease over time; the final acceptable count is zero. The CI
// runner reads the baseline from architecture/capability_inventory.yaml
// via a constant in scripts/ci-architectural-checks.sh. A mismatch
// between the runner-baseline constant and the yaml-baseline is a
// per-PR validation failure.
//
// ── Scope ──────────────────────────────────────────────────────────
//
// internal/**/*.go, excluding *_test.go (test fixtures may freely
// reference the patterns for documentation) and the generated/
// directory (generated code is exempt).
//
// ── Exit codes ─────────────────────────────────────────────────────
//
//	0 — no violations, gate PASS (or within baseline)
//	1 — violations > baseline, gate FAIL
//	2 — invocation error (bad arg / missing path / parse-fatal cascade)
//
// ── Usage ──────────────────────────────────────────────────────────
//
//	go run ./scripts/archcheck/gates/gate_c2_source_catalog_only.go [root_dir] [--baseline=N]
//
// where root_dir defaults to "." (repo root) and baseline defaults
// to 0 (strict — used for verifications during the pre-landing
// testing of this gate; the CI runner sets baseline explicitly).
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ── Canonical source-kind sets ──────────────────────────────────────

// canonicalStringKinds is the lower-cased keyword set that
// legitimately denotes a canonical source-kind value when
// referenced as a string literal in a switch case arm or if
// condition. Two namespaces overlap by design:
//
//	script-source namespace (internal/domain/script/source_spec.go):
//	  "text", "clips", "catalog", "search", "curate",
//	  "artlist", "youtube"
//
//	asset-source namespace (internal/domain/asset/lifecycle_core.go):
//	  "artlist", "stock", "youtube_clip", "clip_drive",
//	  "image", "generated", "sound_effect"
//
// Both namespaces must converge through the canonical Source
// Catalog allowlist. Adding a new canonical kind is a deliberate
// cross-capability change (C1-Step 16 territory) and requires an
// addition here + a deprecation manifest update (godlike/07 §"deprecation ID
// + owner + replacement" rule) + an entry in
// architecture/capability_inventory.yaml::gates_baseline::C2-C.
var canonicalStringKinds = map[string]bool{
	// script sources
	"text":    true,
	"clips":   true,
	"catalog": true,
	"search":  true,
	"curate":  true,
	"artlist": true,
	"youtube": true,
	// asset sources
	"stock":        true,
	"youtube_clip": true,
	"clip_drive":   true,
	"image":        true,
	"generated":    true,
	"sound_effect": true,
}

// canonicalIdentSuffixes is the upper-cased suffix set that, when
// found as the .Sel.Name of an *ast.SelectorExpr or as the .Name
// of an *ast.Ident in a switch case arm or if-condition,
// denotes a canonical Source* enum-constant reference.
//
// Each suffix maps to a single canonical enum constant declared
// in either internal/domain/script/source_spec.go (script
// names) or internal/domain/asset/lifecycle_core.go (asset
// names); the union covers both namespaces.
var canonicalIdentSuffixes = map[string]bool{
	// script-source enum constants (internal/domain/script)
	"Text":    true,
	"Clips":   true,
	"Catalog": true,
	"Search":  true,
	"Curate":  true,
	// asset-source enum constants (internal/domain/asset/lifecycle_core.go)
	"Stock":       true,
	"Artlist":     true,
	"YoutubeClip": true,
	"ClipDrive":   true,
	"Image":       true,
	"Generated":   true,
	"SoundEffect": true,
}

// ── Allowlist ───────────────────────────────────────────────────────

// allowlist is the closed set of paths where source-kind dispatch
// is canonically permitted. Two files, both owning the Source
// Catalog canonical registry pattern.
//
// Path convention: repo-relative (no leading "./"), forward-slash
// normalized. The walker applies the same normalization during
// comparison.
var allowlist = map[string]bool{
	"internal/application/assets/artifacts/source_resolver.go": true,
	"internal/application/scripts/adapters/source_registry.go": true,
}

// ── Violation record ────────────────────────────────────────────────

type violationRow struct {
	file     string
	line     int
	col      int
	nodeKind string // "switch case" | "if condition"
	match    string // what specifically matched (string literal value, ident name, selector expression)
}

// ── main ────────────────────────────────────────────────────────────

func main() {
	var baseline int
	flag.IntVar(&baseline, "baseline", 0, "transitional baseline allowance (final target: 0, see AGENTS.md transitional baselines)")
	flag.Parse()

	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	fset := token.NewFileSet()
	files, err := discoverGoFiles(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "C2-C gate: %v\n", err)
		os.Exit(2)
	}
	sort.Strings(files)

	var violations []violationRow

	for _, path := range files {
		normalized := relPath(root, path)
		if allowlist[normalized] {
			continue
		}

		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "WARN: C2-C parse error in %s: %v\n", path, parseErr)
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			switch stmt := n.(type) {
			case *ast.SwitchStmt:
				inspectSwitch(fset, root, path, stmt, &violations)
			case *ast.IfStmt:
				inspectIfChain(fset, root, path, stmt, &violations)
			}
			return true
		})
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].file != violations[j].file {
			return violations[i].file < violations[j].file
		}
		return violations[i].line < violations[j].line
	})

	if len(violations) > baseline {
		for _, v := range violations {
			fmt.Printf("%s:%d:%d: %s matches canonical source kind (%q) — dispatch outside Source Catalog allowlist\n",
				v.file, v.line, v.col, v.nodeKind, v.match)
		}
		fmt.Fprintf(os.Stderr,
			"\nC2-C gate: %d violation(s) found, baseline=%d — every source-kind switch/if must live in the canonical Source Catalog allowlist:\n",
			len(violations), baseline)
		fmt.Fprintf(os.Stderr,
			"  - internal/application/assets/artifacts/source_resolver.go\n"+
				"  - internal/application/scripts/adapters/source_registry.go\n\n")
		fmt.Fprintf(os.Stderr,
			"Remediation:\n"+
				"  1. Replace the switch/if dispatch with a call into the\n"+
				"     SourceCatalog.Resolve or SourceRegistry.Resolve surface.\n"+
				"  2. OR, where the dispatch is structural-validation (e.g.\n"+
				"     IsValid on an enum type), migrate the boundary check\n"+
				"     into the canonical SourceType.IsValid / SourceKind.IsValid\n"+
				"     helper colocated with the type declaration.\n"+
				"  3. OR, if the file should be added to the canonical\n"+
				"     Source Catalog allowlist, follow the godlike/07\n"+
				"     EXPAND → BACKFILL → CUTOVER sequence and update\n"+
				"     architecture/capability_inventory.yaml::gates_baseline::C2-C\n"+
				"     (requires owner + deadline; --baseline=ratchets only,\n"+
				"     does NOT silently widen the allowlist).\n\n")
		fmt.Fprintf(os.Stderr,
			"To advance the transitional baseline (e.g. after migrating\n"+
				"3 violations in this PR), lower --baseline accordingly\n"+
				"in scripts/ci-architectural-checks.sh and update the\n"+
				"baseline_current row in architecture/capability_inventory.yaml.\n")
		os.Exit(1)
	}

	remaining := baseline - len(violations)
	if remaining > 0 {
		fmt.Fprintf(os.Stderr,
			"C2-C gate: 0 violations; baseline=%d → %d remaining transitional allowance(s) before gate goes hard\n",
			baseline, remaining)
		fmt.Fprintf(os.Stderr,
			"C2-C gate: migrate %d more site(s) into the Source Catalog allowlist (read godlike/07 + capability_inventory.yaml::gates_baseline::C2-C).\n",
			remaining)
	} else {
		fmt.Fprintf(os.Stderr,
			"C2-C gate: 0 violations — every source-kind dispatch lives in the canonical Source Catalog allowlist (baseline=%d)\n",
			baseline)
	}
}

// ── Switch inspection ───────────────────────────────────────────────

// inspectSwitch walks the case-arm list of a SwitchStmt, recording
// one violation row per `case <expr>` whose expr matches a
// canonical source-kind value. Multi-value case arms
// (e.g. `case SourceClips, scriptpkg.SourceCatalog:`) produce N
// rows — one per matched expr — which yields a useful per-arm
// granularity for migration tracking.
func inspectSwitch(fset *token.FileSet, root, path string, sw *ast.SwitchStmt, out *[]violationRow) {
	for _, clause := range sw.Body.List {
		caseClause, ok := clause.(*ast.CaseClause)
		if !ok {
			// default: clause (no expr) — irrelevant.
			continue
		}
		for _, expr := range caseClause.List {
			if m, ok := matchSourceKindExpr(expr); ok {
				pos := fset.Position(expr.Pos())
				*out = append(*out, violationRow{
					file:     relPath(root, path),
					line:     pos.Line,
					col:      pos.Column,
					nodeKind: "switch case",
					match:    m,
				})
			}
		}
	}
}

// ── If-chain inspection ─────────────────────────────────────────────

// inspectIfChain walks an IfStmt and recursively descends Else
// chains. IfStmt.Else is itself an *ast.IfStmt for `else if X { ...}`,
// so a natural recursion collects every else-if condition. Each
// matched condition contributes one violation row.
func inspectIfChain(fset *token.FileSet, root, path string, stmt *ast.IfStmt, out *[]violationRow) {
	if stmt.Cond != nil {
		if m, ok := matchSourceKindCondition(stmt.Cond); ok {
			pos := fset.Position(stmt.Cond.Pos())
			*out = append(*out, violationRow{
				file:     relPath(root, path),
				line:     pos.Line,
				col:      pos.Column,
				nodeKind: "if condition",
				match:    m,
			})
		}
	}
	if stmt.Else != nil {
		// Else may be a *ast.BlockStmt (final else) or a *ast.IfStmt
		// (else-if). Only the latter carries another condition.
		if elseIf, ok := stmt.Else.(*ast.IfStmt); ok {
			inspectIfChain(fset, root, path, elseIf, out)
		}
	}
}

// ── Source-kind matching ────────────────────────────────────────────

// matchSourceKindExpr reports whether expr is a canonical
// source-kind value suitable for switch/if dispatch, returning
// the canonical match text (string literal value, ident name, or
// selector expression) on success.
//
// Recognized forms:
//
//	*ast.BasicLit  (STR)        → string literal whose value
//	                               (lower-cased) is in canonicalStringKinds
//	*ast.Ident                  → Ident.Name == "Source<Kind>"
//	                               (unqualified enum constant in same
//	                               package; Kind ∈ canonicalIdentSuffixes
//	                               values).
//	*ast.SelectorExpr           → outer .Sel.Name == "Source<Kind>"
//	                               (qualified constant like
//	                               scriptpkg.<...>.SourceCatalog).
//
// Anything else returns ("", false) — no match. Note that values-
// map index expressions (reg.byCanonical["artlist"]) and function
// arguments are NOT *ast.SwitchStmt case-arm / *ast.IfStmt.Cond
// nodes, so the AST walk never reaches them; this function is
// purely positional in its callers.
func matchSourceKindExpr(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		unquoted, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		key := strings.ToLower(unquoted)
		if canonicalStringKinds[key] {
			return unquoted, true
		}
		return "", false
	case *ast.SelectorExpr:
		name := e.Sel.Name
		if strings.HasPrefix(name, "Source") {
			suffix := strings.TrimPrefix(name, "Source")
			if canonicalIdentSuffixes[suffix] {
				return fmt.Sprintf("%s.%s", selectorRootName(e), name), true
			}
		}
		return "", false
	case *ast.Ident:
		name := e.Name
		if strings.HasPrefix(name, "Source") {
			suffix := strings.TrimPrefix(name, "Source")
			if canonicalIdentSuffixes[suffix] {
				return name, true
			}
		}
		return "", false
	}
	return "", false
}

// matchSourceKindCondition reports whether an IfStmt condition
// `X == Y` (or `X != Y`) has either operand matching a
// canonical source-kind value. go/ast renders both directions of
// `==` as *ast.BinaryExpr with Op == token.EQL so both sides are
// checked symmetrically.
func matchSourceKindCondition(expr ast.Expr) (string, bool) {
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return "", false
	}
	if bin.Op != token.EQL && bin.Op != token.NEQ {
		return "", false
	}
	if m, ok := matchSourceKindExpr(bin.X); ok {
		return fmt.Sprintf("== %s", m), true
	}
	if m, ok := matchSourceKindExpr(bin.Y); ok {
		return fmt.Sprintf("== %s", m), true
	}
	return "", false
}

// selectorRootName returns the dotted prefix of a SelectorExpr
// (e.g. `scriptpkg.SourceCatalog` → `scriptpkg`, walking nested
// selectors for chains like `scriptpkg.subpkg.SourceCatalog`).
// Used purely for human-readable violation output.
func selectorRootName(e *ast.SelectorExpr) string {
	parts := []string{}
	cur := ast.Expr(e)
	for {
		s, ok := cur.(*ast.SelectorExpr)
		if !ok {
			if id, ok := cur.(*ast.Ident); ok {
				parts = append(parts, id.Name)
			}
			break
		}
		parts = append(parts, s.Sel.Name)
		cur = s.X
	}
	// Reverse-back-to-front chain
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, ".")
}

// relPath applies the same normalization as the walker: takes the
// repo root (the value passed via --root=<dir> to the gate) and the
// walker-supplied path, returning a repo-relative, forward-slash
// normalized path string. Used both by the main allowlist-check
// path and by the inspectSwitch / inspectIfChain violation-emit
// paths so all file:line emissions flow through a single
// normalization function.
func relPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

// ── File walker ─────────────────────────────────────────────────────

// ── Walker scope ────────────────────────────────────────────────────
//
// The C2-C gate's walker is NARROWER than C2-A's. C2-A walks every
// file in `internal/` because its target (TypedRegistry.Register) is
// a composition-point literal that targets the entire dependency
// graph. C2-C is targeted: it enforces the Source Catalog canonical
// dispatch surface, which lives in `internal/application/` (source
// resolver + registry) and is consumed by `internal/api/` (HTTP
// handler routes) and `internal/domain/` (enum + validation
// declarations).
//
// `internal/infrastructure/` is EXCLUDED. Infrastructure files
// (database adapters, qdrant payload mappers, drive SDK wrappers,
// ai sub-services) sometimes have switch statements or if/else
// chains that LOOK like source-kind dispatch (e.g. a payload mapper
// branching on `case "youtube_clip"` to map a Qdrant vector type).
// These are NOT canonical dispatch — they're adapter-level decoding
// patterns. Including them in the gate would force premature
// adapter rewrites that don't change the canonical Source Catalog
// ownership. They are governed by other gates (C2-D / future work)
// instead.
//
// `pkg/` is EXCLUDED. `pkg/*` is a leaf utility tree with no source
// dispatch surface (carries helpers like retry + textutil). The
// gate's purpose is to enforce the canonical Source Catalog flow,
// which terminates in `internal/application/scripts/usecase/` and
// `internal/application/assets/artifacts/`; leaf utilities
// downstream of those flows are out of scope.
//
// `cmd/` is EXCLUDED. One-shot admin CLIs occasionally have a
// switch on source kind (e.g. qdrant_maintenance.go dispatch on
// collection source), but these are operational tooling, not
// production dispatch sites. The C2-C gate's purpose is to enforce
// the canonical application-layer dispatch contract; CLI tools
// are out of scope and are governed by their own CLI-level
// consistency checks.
//
// ── discoverGoFiles ─────────────────────────────────────────────────

// discoverGoFiles walks the three Application + API + Domain
// subtrees of <root>/internal recursively and returns the path of
// every production .go file. The set excludes:
//
//   - *_test.go                          — test fixtures may freely
//     reference the source-kind
//     patterns for documentation.
//   - generated/ subdirectories          — generated code is exempt.
//   - files outside the Application+API+Domain subtrees — the gate
//     governs the canonical
//     source-catalog consumer
//     surface only; infra-layer
//     adapters, leaf utilities,
//     and one-shot CLI tools
//     are scoped out (see the
//     "Walker scope" comment
//     above for the rationale).
//
// If any of the three subtrees is missing (a partial-checkout CI
// fixture, a sandboxed test repo), that subtree is silently skipped
// rather than aborting the entire gate. A missing subtree is
// effectively zero violations by definition, so the gate
// continues with whatever files it could collect from the
// remaining subtrees.
func discoverGoFiles(root string) ([]string, error) {
	scoped := []string{"application", "api", "domain"}
	var files []string
	for _, sub := range scoped {
		internalDir := filepath.Join(root, "internal", sub)
		if _, statErr := os.Stat(internalDir); statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return nil, statErr
		}
		walkErr := filepath.Walk(internalDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info == nil {
				return nil
			}
			if info.IsDir() {
				basename := filepath.Base(path)
				if basename == "generated" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}
	return files, nil
}
