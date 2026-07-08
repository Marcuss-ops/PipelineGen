// Package scan — per-check forward-prevention gate that bans
// references to the canonical `POST /api/script-docs/generate`
// route from any internal/api/** package OTHER than the
// canonical surface at internal/api/script-docs/.
//
// scan/percheck_script_docs_route.go owns the Go migration of
// "Check 63" — a new forward-prevention gate added in
// PR-CHECK-63-SCRIPT-DOCS-ROUTE-2026-07-08. The gate codifies
// the canonical Pattern 0 / godlike/06 SSOT invariant: the
// route lives at exactly one location
// (internal/api/script-docs/handler.go::Handler.RegisterRoutes
// → rg.POST("/generate", h.Generate)), and NO other internal/
// api/ package may string-reference the route, redirect to it,
// or build implicit dependencies on its surface.
//
// Rationale (godlike/06 SSOT + godlike/07 NO-FAKE-AVAILABILITY):
// the script-docs handler was just shipped
// (PR-SCRIPT-DOCS-DRIFT-2026-07-08 closure, 2026-07-08) at the
// canonical surface internal/api/script-docs/. Prior to the
// closure, AGENTS.md documented the route as "planned, ships
// with PR-X" — a doc-only drift that operator code that string-
// referenced the route would have silently hit as 404. The
// drift notice was a real footgun: an agent that wrote
// `r.POST("/api/script-docs/generate", h.ForwardToScriptDocs)`
// in internal/api/script/handler.go as a "redirect" would have
// shadowed the canonical surface with a non-functional
// fallback (404 on the new package, 200 on the old stub). This
// gate is the forward-prevention seam: future agents that try
// to add cross-package string references to the route surface
// as a build failure (`--strict` mode exit 1) BEFORE the
// regression reaches production.
//
// Excluded paths (mirrors the percheck_player_client exemption
// pattern):
//
//   - internal/api/script-docs/** — the canonical SOLE owner of
//     the route. The literal `/api/script-docs/generate` MUST
//     appear in handler.go (the RegisterRoutes call + the
//     godoc) and in module.go (the Descriptor Name() string).
//     All other production Go files MUST route through this
//     package; no string-redirect / string-gateway / string-
//     fallback is allowed.
//
//   - All *_test.go files — regression guards legitimately
//     reference the literal for invariant pinning (the
//     handler_test.go has a TestRegisterRoutes_PinsCanonicalRoute
//     that asserts the literal is the only route registration
//     in the package). Excluding tests prevents false positives
//     on the regression-guard surface.
//
//   - cmd/archcheck/scan/** — out of scope by construction
//     (this scanner walks only internal/api/**, so the scanner
//     directory is naturally never visited). No special
//     exemption row is needed for the scanner; the path-scope
//     filter at WalkDir entry point is the load-bearing
//     mechanism.
//
// Comment-only hits are WARNED (not violation) per the same
// godlike/07 no-fake-availability residue-accounting pattern
// used by percheck_player_client + percheck_monitor: descriptive
// prose that mentions the literal is not a real re-declaration,
// but is logged so future drift is visible in CI output every
// run.
//
// Path-scope rationale (internal/api/** only):
// the user spec was explicit: "bans rg-style references to
// /api/script-docs/generate in any internal/api/** package".
// Scoping the walker to internal/api/** is the narrowest
// correct implementation — references in cmd/, pkg/,
// internal/application/, internal/infrastructure/ are out of
// scope (a CLI script that calls the URL is a different
// concern, governed by the arch policy.yaml allowed-hosts
// list, NOT by this gate).
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// scriptDocsRouteLiteral is the canonical substring the gate
// looks for. The literal is `/api/script-docs/generate` (the
// full route path, not just the package prefix) so we catch
// both route registrations and string-redirect/gateway code
// that would shadow the canonical surface.
//
// godlike/06 SSOT: this literal mirrors the canonical route
// registration at internal/api/script-docs/handler.go:222
// (`rg.POST("/generate", h.Generate)` mounted under the
// `/api/script-docs` group registered by Build). A drift that
// diverges from this literal (e.g. a typo `/api/script-docs/
// generate/` or `/api/scriptdocs/generate`) would surface as
// a separate drift class — a future PR can extend the gate
// to catch the typo class if needed (today the typo is caught
// by the route-registration self-test in handler_test.go).
const scriptDocsRouteLiteral = "/api/script-docs/generate"

// scriptDocsCanonicalRelPathPrefix is the repo-relative path
// prefix of the canonical SOLE owner package. Every other
// internal/api/** Go file MUST NOT contain the literal.
//
// godlike/06 SSOT: the entire internal/api/script-docs/
// package is exempt (NOT a single canonical file). The
// canonical surface spans handler.go (route registration +
// godoc) + module.go (Descriptor.Name() + Descriptor.
// RegisterRoutes) + handler_test.go (regression guards).
// Future expansion (e.g. internal/api/script-docs/state.go
// for GET /api/script-docs/state) is automatically covered
// by the directory-level exemption without needing a
// per-file allowlist row.
const scriptDocsCanonicalRelPathPrefix = "internal/api/script-docs/"

// scriptDocsScopeRelPathPrefix is the repo-relative path
// prefix that defines the GATE SCOPE (the directory the
// walker visits). Per user spec, the gate is scoped to
// internal/api/** ONLY. References in cmd/, pkg/,
// internal/application/, internal/infrastructure/ are out
// of scope and are governed by other gates (e.g.
// allowed-hosts for HTTP clients, percheck_root_override_ban
// for application/api).
const scriptDocsScopeRelPathPrefix = "internal/api/"

// scriptDocsScanNote is the violation Note string. The
// message references the canonical SSOT package + the
// forward-prevention rationale + the PR-SCRIPT-DOCS-DRIFT
// closure so future agents reading the CI failure have the
// full context inline. Mirrors the percheck_player_client
// Note structure for consistency with the established
// pattern.
const scriptDocsScanNote = "forbidden `/api/script-docs/generate` route reference outside canonical SSOT (internal/api/script-docs/); godlike/06 SSOT requires every consumer to route through the canonical Handler.RegisterRoutes — no string-redirect / string-gateway / string-fallback is allowed (PR-CHECK-63-SCRIPT-DOCS-ROUTE-2026-07-08 forward-prevention gate; the drift notice that motivated this gate is closed in PR-SCRIPT-DOCS-DRIFT-2026-07-08)"

// scriptDocsSkipDirs is the standard skip-list for whole-repo
// walks. Mirrors the skipDirs pattern in percheck_typeredecl.go
// + percheck_monitor.go + percheck_player_client.go: .git +
// vendor + node_modules + the node-scraper frontend + examples
// + scripts. Entries are matched against filepath.Base(path) —
// i.e., the IMMEDIATE directory name.
var scriptDocsSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"scripts":      true,
}

// ScanScriptDocsRoute walks every .go file under
// <root>/internal/api/ (excluding *_test.go + the canonical
// internal/api/script-docs/ package) and emits an
// error-severity violation for any file containing the
// literal substring `/api/script-docs/generate`.
//
// Path-scope discipline (godlike/06 SSOT): the walker ONLY
// descends into directories that are on the path from the
// project root to any file in the internal/api/ subtree
// (i.e., the scope root, ancestors of the scope, or
// descendants of the scope). This is the load-bearing
// forward-prevention: the canonical invariant is
// "the route lives at exactly one internal/api/** package";
// references from other parts of the codebase (cmd/,
// pkg/, internal/application/, etc.) are out of scope for
// this gate (a CLI script that calls the URL is a different
// concern, governed by other policy surfaces).
//
// godlike/07 minimum-blast-radius: the directory-branch scope
// filter is the load-bearing path-scope. A previous version
// of this function used a naive string-prefix check
// (`HasPrefix(relSlash, "internal/api/")`) which incorrectly
// skipped the entire `internal/` directory when the walker
// reached it as a parent of the scope (the string "internal"
// does not start with "internal/api/"). The current
// path-component-aware check (shouldDescendIntoScope) descends
// into the scope root, ancestors of the scope, and descendants
// of the scope — but NOT into unrelated subtrees like
// `internal/app/`, `internal/application/`, `cmd/`, `pkg/`.
//
// Severity is `error` (forward-prevention gate; the runner
// --strict mode promotes to ExitViolations). For non-strict
// mode, the runner still prints the report; the exit code
// remains 0 unless --strict is on.
//
// Comment-only hits are logged as warnings via r.Warnings
// (godlike/07 no-fake-availability residue accounting) but
// do NOT contribute to the hard-fail set.
func ScanScriptDocsRoute(root string, pol *policy.Policy, r *report.Report) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// Compute the repo-relative path ONCE per directory so
		// the scope filter is O(1) per entry. The WalkDir
		// callback fires for both directories and files; we
		// want the scope filter to apply to BOTH (descend only
		// into the in-scope subtree).
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)

		// Path-scope filter: only descend into the in-scope
		// subtree OR into ancestors of the in-scope subtree
		// (so we can reach the scope via the parent chain).
		// shouldDescendIntoScope is the load-bearing
		// forward-prevention: out-of-scope paths (cmd/,
		// pkg/, internal/application/, internal/app/, etc.)
		// are never visited, so they cannot trigger
		// violations OR be reported as Warnings. References
		// outside the scope are governed by other policy
		// surfaces (allowed-hosts, percheck_root_override_ban,
		// etc.).
		if d.IsDir() {
			// Top-level dir entries: skipDirs exemptions
			// apply regardless of the path-scope filter
			// (don't even recurse into .git, vendor, etc.).
			if scriptDocsSkipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			// Path-scope gate: skip the directory if it is
			// NOT on the path from root to the scope
			// (i.e., not the scope root, not an ancestor of
			// the scope, not a descendant of the scope).
			if !shouldDescendIntoScope(relSlash, scriptDocsScopeRelPathPrefix) {
				return filepath.SkipDir
			}
			return nil
		}

		// File: skip non-Go files (.md, .sql, etc.) — the gate
		// is about .go source drift, not documentation drift
		// (documentation drift is governed by other gates like
		// scanStaleProsePaths + the AGENTS.md forward-pointer
		// table).
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Exclude test files (regression guards legitimately
		// reference the literal for invariant pinning; handler
		// _test.go has a TestRegisterRoutes_PinsCanonicalRoute
		// guard that would otherwise trigger a false positive).
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Exclude the canonical SSOT package (the literal MUST
		// appear in handler.go for the route registration, in
		// module.go for the Descriptor.Name() string, and in
		// any future file under this package; the gate exists
		// to enforce that NO OTHER internal/api/** package
		// may string-reference the route).
		if strings.HasPrefix(relSlash, scriptDocsCanonicalRelPathPrefix) {
			return nil
		}

		scanScriptDocsRouteFile(path, relSlash, r)
		return nil
	})
}

// shouldDescendIntoScope returns true iff `dir` is on the
// path from the project root to any file in the scope
// subtree — i.e., dir IS the scope, dir is a strict ancestor
// of the scope (so the walker needs to descend through dir to
// reach the scope), or dir is a strict descendant of the scope
// (so the walker needs to descend further into the scope).
//
// Path-component-aware: a directory like "internal/app" is
// NOT in the path to "internal/api/" (the string "internal/app"
// does not start with "internal/api/"), and "internal/api" is
// NOT in the path to a hypothetical scope "internal/apix/" (the
// string "internal/api" is a string-prefix of "internal/apix/"
// but not a path-component prefix). The +"/" concatenation in
// the strict-ancestor check enforces the path-component
// boundary.
//
// Special-cased: the project root (".") is always allowed (it
// is a strict ancestor of every directory).
//
// godlike/06 SSOT load-bearing: this is the canonical
// path-scope filter for the script-docs route gate. Mirrors
// the discipline of percheck_player_client's `shouldDescend`
// helper (which uses a similar HasPrefix+"/" composition) but
// scoped narrowly to internal/api/** per the user spec.
func shouldDescendIntoScope(dir, scope string) bool {
	if dir == "." || dir == "" {
		return true
	}
	// dir == scope: the scope root itself; descend.
	if dir == scope {
		return true
	}
	// dir is a strict ancestor of scope: scope starts with
	// "dir/" (path-component check via +"/" composition).
	if strings.HasPrefix(scope, dir+"/") {
		return true
	}
	// dir is a strict descendant of scope: dir starts with
	// scope AND the next char (if any) is "/" (otherwise
	// "internal/api" would falsely match scope "internal/apix").
	if strings.HasPrefix(dir, scope) {
		// scope always has a trailing "/" (the canonical
		// const scriptDocsScopeRelPathPrefix ends with "/"),
		// so any descendant of scope is already
		// path-component-bounded.
		return true
	}
	return false
}

// scanScriptDocsRouteFile reads a single .go file line-by-line
// and emits violations / warnings per the gate contract.
// See ScanScriptDocsRoute for the full semantics.
//
// Mirrors scanPlayerClientFile structure: same scanner
// surface, same comment-bucketing discipline, same warning
// accounting. Future readers should treat the two functions
// as sibling implementations of the same godlike/07
// residue-accounting pattern.
func scanScriptDocsRouteFile(path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// 1 MiB line-buffer cap mirrors the percheck_player_client
	// scanner — accommodates large generated files (e.g. a
	// future API gateway that inlines a JSON schema as a Go
	// string) without silently truncating a line that contains
	// the literal.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentCount := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if !strings.Contains(line, scriptDocsRouteLiteral) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		// Bucket 1: full-line `//`-prefixed comment (descriptive
		// prose that mentions the route, not a real
		// re-declaration). Logged as warning per godlike/07
		// residue accounting, NOT surfaced as a violation.
		//
		// godlike/07 NO-FAKE-AVAILABILITY: the comment-bucket
		// is conservative — a line that starts with whitespace
		// then `//` is also a comment; we use the trimmed
		// prefix to catch both. A line that is BOTH a
		// comment AND contains code (e.g.
		// `// see /api/script-docs/generate` is a pure
		// comment; `const x = "/api/script-docs/generate" // foo`
		// is a code line with a trailing comment) — the
		// trimmed-prefix check is FALSE for the trailing-
		// comment case, so the line is correctly bucketed
		// as a production violation. This matches
		// scanPlayerClientFile's behavior.
		if strings.HasPrefix(trimmed, "//") {
			commentCount++
			continue
		}
		// Bucket 2: production code containing the literal.
		// This is the hard-fail class — emit a violation.
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        lineNo,
			Rule:        "percheck_script_docs_route",
			Severity:    string(report.SeverityError),
			MatchedRule: "script_docs_route_canonical_gate",
			Note:        scriptDocsScanNote,
		})
	}

	// WARN accounting (godlike/07 no-fake-availability residue
	// accounting): comment-only hits are logged so future drift
	// is visible in CI output every run. They do NOT
	// contribute to the hard-fail set. Mirrors
	// scanPlayerClientFile's warning discipline.
	if commentCount > 0 {
		r.Warnings = append(r.Warnings, "Check 63 (script-docs route): "+
			strconv.Itoa(commentCount)+
			" comment-only reference(s) in "+relPath+
			" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}
