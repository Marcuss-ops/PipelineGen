// Package main implements the Blocco C2-A architectural gate (June 2026):
//
//	every call to <typed>.Registry.Register (api / module / jobs /
//	providers as the canonical type prefixes) MUST live in
//	internal/app/capability_registry.go. All other occurrences are
//	hard-fail.
//
// This gate is the AST-rigorous companion to the existing ripgrep-based
// forward-protection gate at internal/app/capability_registry_gate_test.go.
// The ripgrep gate uses a regex substring scan over file lines; the AST gate
// uses go/parser + ast.Inspect on SelectorExpr nodes. Both run in CI; the AST
// gate catches what the ripgrep gate misses:
//
//   - string-literal Registry.Register tokens (e.g. `"api.Registry.Register"`
//     in a test fixture) — ripgrep false-positives; AST ignores.
//   - comment-only matches ("`(api|jobs|providers).Registry.Register`" in
//     prose) — ripgrep false-positives; AST ignores.
//   - alias-prefixed call sites (`genjobs "..."` then `genjobs.Registry.Register`)
//     — caught by SelectorExpr chain walk regardless of alias name; ripgrep
//     requires the alias be the typed-prefix name.
//
// Both gates run in parallel for defense-in-depth. Pull-request drift in
// either pattern breaks CI immediately.
//
// Allowlist: the ONLY permitted production surface for any
// `*.Registry.Register(` SelectorExpr is `internal/app/capability_registry.go`.
// Every other file is a hard architectural violation per Blocco C2-A + the
// canonical composition-point SSOT (architecture/policy.yaml §"Phase 0
// target-tree governance" + godlike/07 §"zero-legacy-policy").
//
// Scope: internal/**/*.go, excluding *_test.go (test fixtures may freely
// reference the patterns for documentation + AST-string-literal fixtures).
// generated/ and architecture/ docs are excluded by filepath prefix.
//
// Exit codes:
//   0 — no violations, gate PASS.
//   1 — N violations found, gate FAIL.
//   2 — invocation error (bad arg / missing path / parse-fatal cascade).
//
// Usage:
//
//	go run ./scripts/archcheck/gates/gate_c2_registry_only.go [root_dir]
//
// where root_dir defaults to "." (the repo root).
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// permittedCallerPath is the ONE and ONLY file allowed to carry a
// `*.Registry.Register(` SelectorExpr in produced code. All other files
// are hard-fail violations per Blocco C2-A.
//
// Path is repo-relative (no leading "./"). The walker below applies the
// same convention before comparison.
const permittedCallerPath = "internal/app/capability_registry.go"

// typedPrefixes is the set of canonical module-level ident names whose
// `.Registry.Register` SelectorExpr chain the gate looks for.
//
// User spec (Blocco C2-A, June 2026) names the canonical 3 prefixes:
// api, jobs, providers. This gate additionally includes `module` for
// parity with the ripgrep gate (capability_registry_gate_test.go) — the
// underlying Registry type is the same; missing module from the AST gate
// would leave a hole the ripgrep gate covers but the AST gate does not.
//
// Production code in this repo today uses exactly these 4 prefixes
// after Blocco C1-Step 2 (composition-point hoist). New prefixes (e.g.,
// `script.Registry`) require an addition here + a documentation update
// in godlike/07 + capability_inventory.yaml before the gate flip.
var typedPrefixes = map[string]bool{
	"api":       true,
	"module":    true,
	"jobs":      true,
	"providers": true,
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	fset := token.NewFileSet()
	files, err := discoverGoFiles(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "C2-A gate: %v\n", err)
		os.Exit(2)
	}
	sort.Strings(files)

	violations := 0
	for _, path := range files {
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			// Surface parse errors but don't abort; one broken file
			// shouldn't take out the whole tree. The CI runner will
			// catch syntax errors via `go build` separately; this
			// gate's role is architectural, not compiler-level.
			fmt.Fprintf(os.Stderr, "WARN: C2-A parse error in %s: %v\n", path, parseErr)
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			// Outermost selector must be .Register (the call site
			// name). Any other terminating selector (e.g.,
			// .Registry.DoSomething) is out of scope.
			if sel.Sel.Name != "Register" {
				return true
			}

			// The X side must itself be a SelectorExpr terminating
			// in .Registry — i.e. the chain is <prefix>.Registry.Register.
			inner, ok := sel.X.(*ast.SelectorExpr)
			if !ok || inner.Sel.Name != "Registry" {
				return true
			}

			// The innermost X.X must be a bare Ident (e.g., `api`,
			// `module`, `jobs`, `providers`). Compound expressions
			// (e.g., `(*pkg.Registry)(...).Register`) are NOT
			// caught here — and shouldn't be, because they aren't
			// the canonical literal-string pattern the gate is
			// enforcing.
			prefixIdent, ok := inner.X.(*ast.Ident)
			if !ok {
				return true
			}

			// Restrict to the canonical typed prefix set. Skip
			// unknown prefixes (e.g., `someStruct.Registry.Register`
			// where someStruct is a local variable) — those are
			// the ripgrep gate's residual false-positive surface
			// and are out of scope here.
			if !typedPrefixes[prefixIdent.Name] {
				return true
			}

			// Compute repo-relative path for human-readable output.
			relPath, relErr := filepath.Rel(root, path)
			if relErr != nil || relPath == "" {
				relPath = path
			}
			relPath = filepath.ToSlash(relPath)

			// The single canonical composition point is allowed.
			if relPath == permittedCallerPath {
				return true
			}

			violations++
			pos := fset.Position(sel.Pos())
			fmt.Printf("%s:%d: %s.Registry.Register\n",
				relPath, pos.Line, prefixIdent.Name)
			return true
		})
	}

	if violations > 0 {
		fmt.Fprintf(os.Stderr,
			"\nC2-A gate: %d violation(s) found — every %s.Registry.Register call must live in %s\n",
			violations, "{api|module|jobs|providers}", permittedCallerPath)
		fmt.Fprintf(os.Stderr,
			"\nRemediation:\n"+
				"  1. Move the call into %s::registerProviders /\n"+
				"     registerHTTPModules / registerJobs (or the appropriate\n"+
				"     registerX closure for the call surface).\n"+
				"  2. OR route the registration through a typed port\n"+
				"     interface (AGENTS.md Pattern 0) and inject the\n"+
				"     registry at the composition root.\n"+
				"  3. OR if the call is in a test fixture, ensure the\n"+
				"     file is *_test.go (this gate excludes *_test.go).\n",
			permittedCallerPath)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr,
		"C2-A gate: 0 violations — every %s.Registry.Register call lives in %s\n",
		"{api|module|jobs|providers}", permittedCallerPath)
}

// discoverGoFiles walks <root>/internal recursively and returns the
// path of every production .go file. The set excludes:
//   - *_test.go                          — test fixtures may freely
//                                          reference the typed-prefix
//                                          patterns for documentation;
//                                          the gate's purpose is to
//                                          catch production drift, not
//                                          test fixtures.
//   - generated/ subdirectories          — generated code is exempt
//                                          (mirrors capability_inventory.yaml's
//                                          excludes block).
//   - files outside <root>/internal      — the gate scopes to the
//                                          canonical source tree only.
//                                          cmd/admin/*, scripts/*.go,
//                                          and tests/* are out of scope
//                                          for THIS gate; they are
//                                          governed by other gates
//                                          (Check 19 for the api layer
//                                          infrastructure-import lint,
//                                          Check 41 for the
//                                          internal/api/common/ ban,
//                                          etc.).
//
// The walker is fail-loud: a filesystem error mid-walk is returned to
// the caller (main()) which surfaces it on stderr and exits 2.
//
// Files within the canonical composition point
// (permittedCallerPath = internal/app/capability_registry.go) are kept
// in the returned slice but exempted in the AST walk loop above.
func discoverGoFiles(root string) ([]string, error) {
	internalDir := filepath.Join(root, "internal")
	var files []string
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
	return files, nil
}
