// Package scan — percheck_no_domain_job_compatibility_aliases
// (PR-COMPATIBILITY-ALIASES-REMOVE-DOMAIN-JOB, commit 8.1, July 2026).
//
// Job compatibility-alias ban (godlike/07): the legacy
// `internal/domain/job` package was deleted in commit 8. The
// package carried ONLY back-compat type aliases (`Job`,
// `Status`, `Event`, `Filter`, `Store`) layered on top of the
// canonical `internal/kernel/job` surface. Per the repo SSOT
// (godlike/06), the kernel is the SOLE owner of job-mechanism
// types and the kernel does NOT depend on feature-specific job
// names (`scripts.JobGenerate`, `images.JobGenerate`,
// `voiceover.JobGenerate`, etc.) — those live in their
// proprietary owning packages. Re-adding the alias layer would
// silently reintroduce the dual-source-of-truth violation.
//
// This check scans `internal/`, `tests/`, `pkg/`, `cmd/` for
// production-code references to `internal/domain/job` (the
// deleted path) and reports them as a hard ERROR via
// `r.Violations = append(...)`. Comment-only references are
// emitted to `r.Warnings` via the centralized
// `domainJobWarnBucket` helper (silenced under
// productionOnly=true).
//
// Signature `(root, pol, r, productionOnly bool)` mirrors the
// family precedent: percheck_qdrant_index_import_ban,
// percheck_voiceover_alias_ban, percheck_root_override_ban,
// percheck_no_generic_generation_facade.
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// domainJobRule is the canonical rule_id for this per-check.
// Mirrors family precedent (generationFacadeRule + others).
const domainJobRule = "percheck_no_domain_job_compatibility_aliases"

// domainJobWarnBucketID is the prefix tag for warn-bucket
// emissions. The mirror helper `domainJobWarnBucket` appends
// `<bucketID> <label> <msg>` to `r.Warnings`. Mirrors
// generationFacadeWarnBucket family precedent.
const domainJobWarnBucketID = "domain_job_compatibility_aliases_comment_only"

// domainJobErrorBucketID is the prefix tag for error-bucket
// emissions (embedded in MatchedRule).
const domainJobErrorBucketID = "domain_job_compatibility_aliases"

const (
	// domainJobBannedPath is the literal import path banned
	// (the previously-deleted alias-layer package).
	domainJobBannedPath = "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	// domainJobNote is the Note text attached to every ERROR
	// violation. References commit 8 + the banned-path rationale
	// so the operator sees the migration rationale inline (no
	// cross-file lookup needed).
	domainJobNote = "forbidden import of the deleted internal/domain/job alias layer (commit 8, PR-COMPATIBILITY-ALIASES-REMOVE-DOMAIN-JOB, July 2026). The package carried ONLY back-compat type aliases (Job/Status/Event/Filter/Store) shadowing the canonical internal/kernel/job surface. Per godlike/06 SSOT the kernel is the SOLE owner of job-mechanism types and does NOT depend on feature-specific job names (scripts.JobGenerate / images.JobGenerate / voiceover.JobGenerate / youtube.JobExtract / documents.JobGenerate / media.JobReindex — those live in their proprietary owning packages). Forward-prevention gate: percheck_no_domain_job_compatibility_aliases. The kernel surface is the canonical source-of-truth; the alias layer was deleted because it was a dual-source-of-truth violation. Re-introducing the layer would silently reintroduce the godlike/07 NO-FAKE-AVAILABILITY regression."
)

// domainJobImportRegex matches a Go import statement that
// references the deleted `internal/domain/job` path. It requires
// the path to be terminated by either a closing quote `"` or a
// path-join slash `/` (so legitimate partial-prefix matches do
// not false-positive on `internal/domain/jobsentinel` etc.).
var domainJobImportRegex = regexp.MustCompile(regexp.QuoteMeta(domainJobBannedPath) + `(/|")`)

// domainJobScanScopes is the prefix set the gate applies
// (production code + composition root + tests + leaf pkg +
// cmd/). The scanner's own directory (cmd/archcheck/scan/**)
// is exempt so the regex does not ban itself when it
// embeds the literal banned path in a comment / MatchedRule.
var domainJobScanScopes = []string{"internal", "tests", "pkg", "cmd"}

// domainJobScannerExemptPath is the substring matched against
// any candidate file path indicating the percheck should skip
// (its own scanner package mirrors percheck_qdrant_index_import_ban
// + percheck_no_generic_generation_facade family precedent).
const domainJobScannerExemptPath = "/cmd/archcheck/scan/"

// domainJobWarnBucket is the centralized residue-emitter. Each
// call appends one line to `r.Warnings` in the format
// `<bucketID> <label> <msg>`. Mirrors generationFacadeWarnBucket.
func domainJobWarnBucket(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, domainJobWarnBucketID+" "+label+" "+msg)
}

// ScanNoDomainJobCompatibilityAliases enforces the post-alias-
// removal rule: nobody — production code, test code, or
// documentation — may import the deleted `internal/domain/job`
// path. Production-code hits are HARD ERRORs (appended to
// `r.Violations`); comment-only or doc-only hits are WARNed
// (silenced when productionOnly=true so operators can still ship
// resume-bookkeeping references silently).
func ScanNoDomainJobCompatibilityAliases(root string, _ *policy.Policy, r *report.Report, productionOnly bool) {
	for _, dir := range domainJobScanScopes {
		absDir := filepath.Join(root, dir)
		if _, err := os.Stat(absDir); err != nil {
			continue
		}
		_ = filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.Contains(path, domainJobScannerExemptPath) {
				return nil
			}
			scanDomainJobFile(path, r, productionOnly)
			return nil
		})
	}
}

// scanDomainJobFile reads one Go file and reports any
// `internal/domain/job` reference it finds. Production-code
// hits (non-comment lines, e.g. import statements) are HARD
// ERRORs (`r.Violations = append(...)`); comment-only hits are
// accumulated per file and emitted as a single `r.Warnings`
// entry (silenced under productionOnly).
func scanDomainJobFile(path string, r *report.Report, productionOnly bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	relPath := pkgFromDomainJobRel(path)
	scanner := bufio.NewScanner(f)
	commentOnly := 0
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if !domainJobImportRegex.MatchString(line) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if isGoFullCommentLine(trimmed) {
			commentOnly++
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     relPath,
			File:        relPath,
			Line:        lineNo,
			Rule:        domainJobRule,
			Severity:    string(report.SeverityError),
			MatchedRule: domainJobErrorBucketID + ":production_import_attempt",
			Note:        domainJobNote + " | snippet: " + truncateDomainJobHit(line),
		})
	}
	if commentOnly > 0 && !productionOnly {
		domainJobWarnBucket(r, "domain-job-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// isGoFullCommentLine reports whether the trimmed line is a
// full-line Go comment (`//…`). Block comments (`/*…*/`) and
// leading `*` continuation lines are NOT considered full-line
// comments because they can carry import statements inside them
// (rare but legal in code-gen — that pattern is treated as a
// production-code hit, which is the safe interpretation).
func isGoFullCommentLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "//")
}

// pkgFromDomainJobRel returns a package-relative display path
// for the percheck's error/warn labels. Trims the canonical
// prefix options (`internal/`, `tests/`, `pkg/`, `cmd/`). The
// return value can be empty when the path matches an
// unexpected prefix (rare; family-precedent fallback).
func pkgFromDomainJobRel(absPath string) string {
	rel := strings.TrimPrefix(absPath, "/")
	for _, prefix := range []string{"internal/", "tests/", "pkg/", "cmd/"} {
		rel = strings.TrimPrefix(rel, prefix)
		if rel != absPath {
			// TrimPrefix succeeded — return the trimmed result.
			return rel
		}
	}
	return rel
}

// truncateDomainJobHit returns a stable-line-length capped
// version of the matched import line for inline-emission in
// the Violation.Note field (mirrors truncateGenerationFacade
// family precedent). The cap keeps the JSON report concise.
func truncateDomainJobHit(line string) string {
	const maxLen = 160
	if len(line) <= maxLen {
		return line
	}
	return line[:maxLen-3] + "..."
}
