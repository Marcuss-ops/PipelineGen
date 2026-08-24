// Package scan — Check 62 SCRIPT-FLOW-SPLIT inline-middleware-in-
// feature-routing-file ban.
//
// scan/percheck_inline_middleware.go owns the Go migration of
// scripts/ci-architectural-checks.sh::Check 62. The canonical
// SCRIPT-FLOW-SPLIT contract bans inline middleware signatures
// (RequireAdminToken / extractHeaderToken / EnableAuth /
// AdminTokenProvider) in internal/api/<feature>/ files that exceed
// 300 LoC — any such file is an extraction candidate, with the
// signatures belonging in internal/api/<feature>/middleware_auth.go
// per AGENTS.md Pattern 5 + godlike/06 SSOT (orchestrator owns the
// contract types + the 4-element auth cluster lives in a single
// per-feature middleware file).
//
// Phase 2 of PR-ARCHCHECK-GO-MIGRATION-PHASE-2 (deadline
// 2026-08-15) ships this scanner alongside the original shell
// check (Check 62 = this gate), which is RETAINED as a
// transitional baseline per godlike/08 §"Zero-baseline rule".
//
// *_test.go files and any file matching the canonical leaf-name
// `middleware_auth.go` are EXCLUDED from the scope:
//
//   - _test.go exclusion mirrors Check 54's _test.go exclusion: tests
//     may freely reference the 4 signatures as part of mock-driving
//     the AdminTokenProvider port contract.
//   - middleware_auth.go exclusion is the canonical allowlist: by
//     convention, every feature that wires auth MUST place all 4
//     signatures in <feature>/middleware_auth.go. The exclusion
//     prevents the gate from firing on the canonical location.
//
// Cross-references:
//   - architecture/current.yaml#SCRIPT-FLOW-SPLIT: the wave-tracker
//     entry that registers this closure.
//   - architecture/current.yaml#PR-ARCHCHECK-GO-MIGRATION-PHASE-2: the
//     Phase-2 forward-prevention umbrella wave-tracker.
//   - internal/api/script/middleware_auth.go: the canonical SOLE
//     owner of the 4-element auth cluster (per AGENTS.md Pattern 5).
package governance

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// inlineMiddlewareRootRelPath is the repo-relative path that
// scanner walks. Every .go file under this prefix (excluding
// _test.go + files matching middlewareAuthLeafName) is scanned.
const inlineMiddlewareRootRelPath = "internal/api"

// middlewareAuthLeafName is the canonical leaf-name that exempts a
// file from the gate. Per AGENTS.md Pattern 5 + SCRIPT-FLOW-SPLIT
// precedent, every feature's 4-element auth cluster lives in
// <feature>/middleware_auth.go; the exemption prevents the gate
// from firing on the canonical home.
const middlewareAuthLeafName = "middleware_auth.go"

// inlineMiddlewareMaxLines is the canonical LoC threshold for the
// extraction candidate classifier. Mirrors the shell Check 62
// `threshold=300` constant. Files strictly greater than this size
// AND carrying any of the 4 signatures are surfaced as Violations.
const inlineMiddlewareMaxLines = 300

// inlineMiddlewareSignatures is the closed set of 4 forbidden
// identifiers. A file carrying ANY of these substrings (after
// exclusion filters) is inspected for size; if size > threshold,
// a Violation is emitted. The order matches the user's verbatim
// spec: the canonical alpha-order of the 4-element auth cluster.
var inlineMiddlewareSignatures = []string{
	"RequireAdminToken",
	"extractHeaderToken",
	"EnableAuth",
	"AdminTokenProvider",
}

// ScanInlineMiddleware walks every non-test .go file under
// <root>/internal/api/, counts each file's LoC, and emits an
// `error`-severity Violation for every file that BOTH (a)
// exceeds 300 LoC and (b) contains any of the 4 forbidden
// inline-middleware signatures.
//
// The `middleware_auth.go` leaf-name is the ONLY exemption: scripts
// under `internal/api/<feature>/` matching this leaf carry the
// canonical auth cluster and are NOT flagged, regardless of LoC.
//
// Skipped directories (mirrors ScanTypeRedeclarations
// skipDirs): the walker is scoped to internal/api/ subtrees
// only — node_modules / vendor / .git / etc. are not under
// the walker scope so they are bypassed by construction.
// *_test.go files are excluded per AGENTS.md Pattern 8 +
// SCRIPT-FLOW-SPLIT precedent (tests may mock-drive
// AdminTokenProvider freely).
//
// The scan adds violations to r.Violations in arbitrary order;
// the runner's post-process pass applies a stable sort before
// emitting the report JSON. The single-shot emission pattern per
// file matches the canonical idiomatic style used by
// percheck_typeredecl.go (no internal sort needed inside the
// scanner itself).
func ScanInlineMiddleware(root string, pol *policy.Policy, r *report.Report) {
	scanRoot := filepath.Join(root, inlineMiddlewareRootRelPath)

	// Defensive: missing internal/api/ dir is a tree-shape error
	// — other scans surface it; the inline-middleware scanner
	// simply no-ops on missing input.
	_ = filepath.WalkDir(scanRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Exclusion 1 — test files.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Exclusion 2 — canonical middleware_auth.go leaf-name.
		if filepath.Base(path) == middlewareAuthLeafName {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		relSlash := filepath.ToSlash(rel)
		scanInlineMiddlewareFile(path, relSlash, r)
		return nil
	})
}

// scanInlineMiddlewareFile reads a single .go file line-by-line,
// counts lines (mirrors `wc -l <file>`), and runs a single-pass
// substring scan for each of the 4 forbidden signatures. If BOTH
// (a) line count > threshold AND (b) any signature is found, a
// Violation is emitted.
//
// The single-pass substring scan intentionally does NOT use
// ripgrep / go/parser / AST machinery: the 4 signatures are
// distinctive Go identifiers and the gate's contract is
// "line-count AND substring-match", not "syntax-correct usage".
// This mirrors the shell Check 62's `wc -l + rg` semantic
// exactly (godlike/06 SSOT shell-equivalence).
func scanInlineMiddlewareFile(path, relPath string, r *report.Report) {
	lineCount, sigPresent := readInlineMiddlewareFile(path)
	// Compound gate: BOTH conditions must hold for a Violation.
	if lineCount <= inlineMiddlewareMaxLines {
		return
	}
	if !sigPresent {
		return
	}
	// Compute the canonical feature-dir (parent of relPath) for
	// the violation note's extraction target. Mirrors the shell
	// gate's `dirname $f` + `/middleware_auth.go` concat.
	featureDir := filepath.ToSlash(filepath.Dir(relPath))
	r.Violations = append(r.Violations, report.Violation{
		File:        relPath,
		Rule:        "percheck_inline_middleware",
		Severity:    string(report.SeverityError),
		MatchedRule: "inline_middleware_in_oversized_feature_route",
		ActualLines: lineCount,
		MaxLines:    inlineMiddlewareMaxLines,
		Note: "inline middleware in feature routing file " + relPath + " " +
			strconv.Itoa(lineCount) + " LoC exceeds " +
			strconv.Itoa(inlineMiddlewareMaxLines) +
			"; extract to " + featureDir + "/middleware_auth.go per AGENTS.md Pattern 5 + SCRIPT-FLOW-SPLIT precedent",
	})
}

// readInlineMiddlewareFile reads path and returns the
// (lineCount, sigPresent) tuple. sigPresent is true iff ANY of the
// 4 canonical signatures appears on any line (the violation
// contract triggers on "at least one" — the specific cluster is
// caller-irrelevant since remediation involves extracting all 4).
//
// readInlineMiddlewareFile uses bufio.Scanner with the canonical
// 1MB-buffer idiom (percheck_monitor.go) so very long lines
// (auto-generated code) do not truncate. The returned lineCount
// mirrors `wc -l <file>` semantics: the per-line increment is the
// number of newline-terminated blocks. Both flags strip
// trailing-\n semantics so scanner line-count == wc -l count.
func readInlineMiddlewareFile(path string) (lineCount int, sigPresent bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lineCount++
		line := sc.Text()
		for _, sig := range inlineMiddlewareSignatures {
			if strings.Contains(line, sig) {
				sigPresent = true
				// Do not break early — counting lineCount is
				// cheap and lets the violation report carry
				// the full file size in the ActualLines field.
			}
		}
	}
	return lineCount, sigPresent
}
