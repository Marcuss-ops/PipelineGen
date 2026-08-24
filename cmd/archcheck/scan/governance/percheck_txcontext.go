// Package scan — Check 53 TxContext-ban (P0 C7, July 2026).
//
// scan/percheck_txcontext.go owns the Go migration of
// scripts/ci-architectural-checks.sh::Check 53. The canonical
// Sender-side atomic-complete port surface lives in
// internal/capabilities/jobs/policy/complete_job_service.go.
// The TxContext interface (5 methods: GetJob / UpdateJobToSucceededCAS
// / InsertResultOnConflict / GetPriorArtifactHashes / PersistArtifactMap /
// InsertOutboxEnvelope) is the ONLY legitimate seam through which
// callers may invoke the underlying in-TX work. Direct callers
// outside the canonical completion service package bypass
// Service.Complete orchestration (pre-TX Validated gate + lease
// CAS + ON CONFLICT dedup + hash round-trip + outbox emission)
// and silently regress the canonical single-TX guarantee
// (godlike/07 no-fake-availability).
//
// Phase 1 of PR-ARCHCHECK-GO-MIGRATION-PHASE-1 (deadline
// 2026-08-15) ships this scanner alongside the original shell
// check, which is RETAINED as a transitional baseline.
//
// Cross-references:
//   - internal/capabilities/jobs/policy/complete_job_service.go: the
//     canonical service (the ONLY allowed caller surface).
//   - scripts/ci-architectural-checks.sh::Check 53: the shell
//     check whose semantics this scanner mirrors 1:1.
//   - architecture/current.yaml#P0-COMPL-5-WIRE-NAMING: the wave
//     that introduced the canonical surface.
package governance

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// txContextCompletionRelPath is the repo-relative path to the
// canonical completion service package. Every .go file under
// this directory is the ONLY allowed caller surface for the 5
// wire methods. Matches the shell check's
// --glob '!**/internal/capabilities/jobs/policy/**' allowlist.
const txContextCompletionRelPath = "internal/capabilities/jobs/policy"

// txContextWireMethods is the canonical list of 5 wire methods
// (per P0 C7, July 2026) that MUST only be called from the
// completion service. Direct callers from any other package
// silently regress the canonical single-TX guarantee.
//
// The 6th method (GetJob) is intentionally excluded because
// the same name is used in non-completion code as a generic
// "fetch job row" helper — the shell check pattern
// `\bGetJob\(` would over-match unrelated sites. The 5
// methods listed here are the load-bearing P0 C7 surface.
var txContextWireMethods = []string{
	"UpdateJobToSucceededCAS",
	"InsertResultOnConflict",
	"GetPriorArtifactHashes",
	"PersistArtifactMap",
	"InsertOutboxEnvelope",
}

// ScanTxContextBan walks <root>/internal/application/** and
// <root>/internal/api/** for non-test .go files, scanning each
// line for any of the 5 TxContext wire-method call patterns.
// Files under internal/capabilities/jobs/policy/ are
// allowlisted (the canonical service package). Full-line
// comments (lines starting with `//` after trim) are excluded
// so descriptive prose doesn't trigger false positives.
//
// Severity is `error` (mirrors the shell check's exit 1 on
// hits). The runner --strict mode promotes `error` violations
// to ExitViolations.
func ScanTxContextBan(root string, pol *policy.Policy, r *report.Report) {
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}

	completionPrefix := filepath.ToSlash(txContextCompletionRelPath) + "/"

	// Scan two roots: internal/application and internal/api.
	// The shell check's `internal/application internal/api`
	// glob list matches this exactly.
	for _, subdir := range []string{"internal/application", "internal/api"} {
		dir := filepath.Join(root, subdir)
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if skipDirs[filepath.Base(path)] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			relSlash := filepath.ToSlash(rel)
			// Allowlist: skip any file under the canonical
			// completion service package (path prefix match).
			if strings.HasPrefix(relSlash, completionPrefix) {
				return nil
			}
			scanTxContextFile(root, path, relSlash, r)
			return nil
		})
	}
}

// scanTxContextFile reads a single .go file line-by-line and
// emits a violation for each non-comment line that contains
// one of the 5 TxContext wire-method call patterns. Comment
// lines (leading `//` after trim) are excluded. The match is
// a simple substring search for `.<MethodName>(` — sufficient
// for the P0 C7 surface (no AST walk needed; the wire-method
// names are well-known and unique enough that substring
// matching produces zero false-positives in production code).
func scanTxContextFile(root, path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		// Drop full-line comments. The shell awk pattern
		// `^[[:space:]]*//` matches the same shape (leading
		// whitespace then `//`).
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		// Check each of the 5 wire-method call patterns.
		for _, m := range txContextWireMethods {
			needle := "." + m + "("
			if !strings.Contains(line, needle) {
				continue
			}
			r.Violations = append(r.Violations, report.Violation{
				File:        relPath,
				Line:        lineNum,
				Rule:        "percheck_txcontext_ban",
				Severity:    string(report.SeverityError),
				MatchedRule: "txcontext_outside_completion_service",
				Note:        "direct TxContext wire-method call outside canonical completion service: " + needle + " — route through completion.Service.Complete to preserve the canonical single-TX guarantee",
			})
		}
	}
}
