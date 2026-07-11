// Package main — CheckSymbolReferences.
//
// symbol_refs.go is the artifact-resolution half of the archcheck gate
// added by action P0-5 slice 4/4 of the cleanup plan (June 2026). The
// rest of scripts/archcheck/ governs CODEBASE-shape regressions
// (database/sql in api/application/domain, interface{} growth,
// dependency setters, ...). This file governs a different axis:
// TOKEN-LEVEL STALE REFERENCES in the architecture documentation
// files (architecture/*.yaml, plus the
// issues file added in slice 2/4).
//
// How it works:
//
//  1. Walk ONLY the two yamlDir files named in the slice 4/4 spec
//     (action P0-5 of cleanup plan, June 2026):
//
//     yamlDir/current.yaml  — active migration / wave tracker
//     yamlDir/issues.yaml   — open technical-issues tracker
//
//     Other yaml surfaces (policy.yaml, deprecations.yaml,
//     ownership.generated.yaml, ownership/<split>.yaml,
//     migrations/*.yaml, archive/**/*.yaml) are deliberately EXCLUDED:
//     each is owned by different tooling (cmd/archcheck policy rules,
//     architecture-aggregate generator, migration inventory composer,
//     snapshot audit) and their stale-reference drift is a different
//     bug class that symbol_refs.go should not chase.
//
//  2. From each scanned file, emit ONLY the leaf scalars whose
//     ancestor chain (tracked via an indentation-aware stack) includes
//     one of the canonical ScopedParentKeys (`linked_issues` and
//     `blocker`). Leaves under `exit_gate`, `description`, `title`,
//     `status`, etc. — which often contain English prose that freely
//     mentions `internal/...` paths for documentation — are NOT
//     emitted, eliminating false-positive violations.
//
//  3. Skip full-line comments (lines starting with `#` after
//     trim). AGENTS.md zero-legacy rule.
//
//  4. Find sub-tokens whose FIRST 3+ bytes match one of the
//     canonical Go-path prefixes (`internal/`, `pkg/`, `cmd/`),
//     extending greedily through path chars (a-z, 0-9, _, -, .,
//     /), then trim trailing punctuation. Tokens are deduped per
//     leaf scalar.
//
//  5. Route each token to one of four validators and emit one
//     Finding per missing reference.
//
// Routing (mirrors the AGENTS.md allowed zones and the existing
// scripts/archcheck/main.go focus gate):
//
//	internal/<...>/*.go   os.Stat the path
//	internal/<pkg>      go list github.com/Marcuss-ops/PipelineGen/<token>
//	pkg/<x>            go list ./<token> (+ os.Stat for *.go tail)
//	cmd/<binary>       os.Stat cmd/<binary>/main.go (+ os.Stat for *.go tail)
//
// False-positive guards:
//   - Full-line comments (`#` lines) are skipped.
//   - Tokens that don't start with `internal/`/`pkg/`/`cmd/` AND
//     don't end with `.go` are NOT considered paths; left alone.
//
// Wired into scripts/ci-architectural-checks.sh as Check N
// (post-Check-1, pre-final-summary). Run via:
//
//	go run ./scripts/archcheck --symbol-refs
//
// Output is written to stderr (one structured line per finding, plus
// a trailing `symbol_refs: N unresolved references` summary). Exit
// code is 1 when any finding is present (CI gate convention).
package main

import (
	"fmt"
	"os"
	"sort"
)

// modulePath is the PipelineGen Go module name. Hardcoded rather
// than read from go.mod because the archcheck package is already
// ~10 files and adding a go.mod parser would push the stdlib-only
// dependency story over budget. If anyone renames the module, they
// will need to flip this constant and re-run --symbol-refs locally
// to confirm zero findings before pushing.
const modulePath = "github.com/Marcuss-ops/PipelineGen"

// Finding is one unresolved architecture-reference. File is
// repo-relative. Line is 1-indexed. Token is the raw path token as
// extracted from the YAML scalar (no normalisation — what the
// authored YAML looks like, byte-for-byte). Kind is one of the
// four canonical validators (`internal_go_file`, `internal_pkg`,
// `pkg_go_file`, `pkg_pkg`, `cmd_go_file`, `cmd_binary`). Reason is
// a short operator-readable explanation of WHY the token didn't
// resolve (missing file / go list error / missing main.go).
type Finding struct {
	File   string
	Line   int
	Token  string
	Kind   string
	Reason string
}

// Findings is a sortable slice of Finding. Sorted for deterministic
// CI output (CI dashboards alphabetise by (file, line) so the same
// tree yields the same diff across CI runs).
type Findings []Finding

// goPathPrefixes are the lexical prefixes that mark a YAML scalar
// token as a Go-path reference. They mirror AGENTS.md's allowed
// zones. Anything else is left alone (no false-positive "this
// looks like a path" matches). Order is signficant: tokens are
// scanned for each prefix in turn; the first hit wins.
var goPathPrefixes = []string{"internal/", "pkg/", "cmd/"}

// ScopedParentKeys are the YAML parent keys whose descendant leaf
// scalars MUST be checked for Go-path references per slice 4/4
// (action P0-5 of cleanup plan, June 2026). Other parent keys
// (`status`, `owner`, `deadline`, `exit_gate`, `exit_signal`,
// `title`, `description`, ...) are OFF-LIMITS for this check — they
// describe qualitative behaviour or narrative prose and freely
// mention `internal/...` tokens in plain English, which would
// generate false-positive violations.
//
// `linked_issues` and `blocker` are the two canonical cross-
// reference fields in the architecture current.yaml / issues.yaml
// schema: every entry under them points at a ticket whose
// `owner_capability` annotation SHOULD resolve to a real codebase
// location. Earlier P0-5 slices (1-3) surfaced these fields and
// slimmed the schema; slice 4/4 promotes resolution of their values
// to a hard CI gate.
var ScopedParentKeys = map[string]bool{
	"linked_issues": true,
	"blocker":       true,
}

// CheckSymbolReferences walks yamlDir for *.yaml files
// (top-level + yamlDir/archive/ recursively) and returns one
// Finding per Go-like token that does not resolve on the file
// system or via `go list`. The function is the canonical
// entry-point used by scripts/ci-architectural-checks.sh Check N.
//
// Returns nil Findings + nil error when the YAML tree is clean.
// Returns Findings != nil + nil error when some refs resolve and
// some don't (the caller should treat the non-empty Findings as
// the failure signal). Returns Findings = nil + non-nil error when
// one or more files could not be scanned at all (read error); the
// error message names the file path for the operator's
// convenience.
//
// The returned Findings is sorted by (File, Line, Token).
func CheckSymbolReferences(yamlDir string) (Findings, error) {
	files, err := collectYAMLFiles(yamlDir)
	if err != nil {
		return nil, err
	}

	out := Findings{}
	for _, f := range files {
		leaves, scanErr := scanYAMLScopedLeafScalars(f, ScopedParentKeys)
		if scanErr != nil {
			return out, fmt.Errorf("scan %s: %w", f, scanErr)
		}
		for _, leaf := range leaves {
			tokens := extractGoPathTokens(leaf.value)
			for _, tok := range tokens {
				if reason := validateToken(tok); reason != "" {
					out = append(out, Finding{
						File:   f,
						Line:   leaf.line,
						Token:  tok,
						Kind:   classifyToken(tok),
						Reason: reason,
					})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Token < out[j].Token
	})
	return out, nil
}

// ── Helpers (extracted to symbol_refs_parse.go and symbol_refs_graph.go) ──
//
// YAML AST:   scannedScalar, keyFrame, collectYAMLFiles, scanYAMLLeafScalars,
//             isPathChar, trimTrailingPunctuation, scanYAMLScopedLeafScalars,
//             leafScanCompile regex vars → symbol_refs_parse.go
// Import graph: extractGoPathTokens, classifyToken, validateToken,
//             runGoList → symbol_refs_graph.go

// runSymbolRefsChecks is the archcheck dispatcher entry-point
// invoked by main.go when cfg.Mode == ModeSymbolRefs. It runs
// CheckSymbolReferences against the canonical yamlDir
// (`architecture/`, in cwd-relative terms — CI runs from
// REPO_ROOT), prints each finding as one structured line to
// stderr, then prints the trailing summary line. Exits 1 when
// findings is non-empty (CI gate convention). Exits 0 otherwise.
//
// Called from scripts/ci-architectural-checks.sh Check N as:
//
//	go run ./scripts/archcheck --symbol-refs
func runSymbolRefsChecks() int {
	yamlDir := "architecture"
	findings, err := CheckSymbolReferences(yamlDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "symbol_refs: scan error: %v\n", err)
		return 2
	}
	if len(findings) == 0 {
		return 0
	}
	for _, f := range findings {
		fmt.Fprintf(os.Stderr,
			"symbol_refs: %s:%d  kind=%s token=%s  %s\n",
			f.File, f.Line, f.Kind, f.Token, f.Reason)
	}
	fmt.Fprintf(os.Stderr, "symbol_refs: %d unresolved references\n", len(findings))
	return 1
}
