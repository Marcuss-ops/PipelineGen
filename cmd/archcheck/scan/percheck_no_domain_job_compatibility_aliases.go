// Package scan — percheck_no_domain_job_compatibility_aliases
// (PR-COMPATIBILITY-ALIASES-REMOVE-DOMAIN-JOB, July 2026).
//
// Job compatibility-alias ban (godlike/07): the legacy
// `internal/domain/job` package was deleted in this commit. The
// package carried ONLY back-compat type aliases (`Job`,
// `Status`, `Event`, `Filter`, `Store`) layered on top of the
// canonical `internal/kernel/job` surface. Per the repo SSOT,
// the kernel is the SOLE owner of job-mechanism types and the
// kernel does NOT depend on feature-specific job names
// (`scripts.JobGenerate`, `images.JobGenerate`,
// `voiceover.JobGenerate`, etc.) — those live in their
// proprietary owning packages. Re-adding the alias layer would
// silently reintroduce the dual-source-of-truth violation.
//
// This check scans `internal/`, `tests/`, `pkg/`, `cmd/` for
// production-code references to `internal/domain/job` (the
// deleted path) and reports them as a hard ERROR. Comment-only
// references are WARNed (silenced under productionOnly=true)
// for documentation purposes.
//
// Signature `(root, pol, r, productionOnly bool)` mirrors the
// family precedent: percheck_qdrant_index_import_ban,
// percheck_voiceover_alias_ban, percheck_root_override_ban.
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const (
	domainJobBannedPath = "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	domainJobErrorBucket = "domain_job_compatibility_aliases"
	domainJobWarnBucket  = "domain_job_compatibility_aliases_comment_only"
)

// domainJobImportRegex matches a Go import statement that
// references the deleted `internal/domain/job` path. It requires
// the path to be terminated by either a closing quote `"` or a
// path-join slash `/` (so legitimate partial-prefix matches do
// not false-positive on `internal/domain/jobsentinel` etc.).
var domainJobImportRegex = regexp.MustCompile(regexp.QuoteMeta(domainJobBannedPath) + `(/|")`)

// ScanNoDomainJobCompatibilityAliases enforces the post-alias-
// removal rule: nobody — production code, test code, or
// documentation — may import the deleted `internal/domain/job`
// path. Production-code hits are hard ERRORs; comment-only or
// doc-only hits are WARNed (silenced when productionOnly=true so
// operators can still ship resume-bookkeeping references).
func ScanNoDomainJobCompatibilityAliases(root string, _ *policy.Policy, r *report.Report, productionOnly bool) {
	scanned := 0
	for _, dir := range []string{"internal", "tests", "pkg", "cmd"} {
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
			// Per family precedent: the percheck's own scan dir is
			// exempt so the regex does not ban itself when it
			// embeds the literal banned path in a comment.
			if strings.Contains(path, string(filepath.Separator)+"cmd"+string(filepath.Separator)+"archcheck"+string(filepath.Separator)+"scan"+string(filepath.Separator)) {
				return nil
			}
			scanDomainJobFile(path, r, productionOnly)
			scanned++
			return nil
		})
	}
}

// scanDomainJobFile reads one Go file and reports any
// `internal/domain/job` reference it finds. Production-code
// hits (non-comment lines, e.g. import statements) are ERRORs;
// comment-only hits are WARNed. _test.go files are scanned with
// the same severity (test code may hold the deleted path as a
// import too — a test built against a deleted package cannot
// compile), but the WARN bucket is the canonical
// residue-accounting surface for leftover doc-comment mentions.
func scanDomainJobFile(path string, r *report.Report, productionOnly bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !domainJobImportRegex.MatchString(line) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if isGoFullCommentLine(trimmed) {
			if !productionOnly {
				r.AddInfraction(domainJobWarnBucket, path, line, "comment-only reference to deleted internal/domain/job (residue accounting: percheck_no_domain_job_compatibility_aliases)")
			}
			continue
		}
		r.AddInfraction(domainJobErrorBucket, path, line, "import of deleted internal/domain/job (re-add prohibited: PR-COMPATIBILITY-ALIASES-REMOVE-DOMAIN-JOB)")
	}
}

// isGoFullCommentLine reports whether the trimmed line is a
// full-line Go comment (`//…`). Block comments (`/*…*/`) are
// NOT considered full-line comments because they can carry
// import statements inside them (rare but legal in code-gen).
func isGoFullCommentLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "//")
}

// pkgFromDomainJobRel returns a package-relative display path
// for the percheck's error/warn labels (mirrors the family
// precedent pkgFromQdrantImportBanRel / pkgFromGenerationFacadeRel).
func pkgFromDomainJobRel(absPath string) string {
	rel := strings.TrimPrefix(absPath, "/")
	rel = strings.TrimPrefix(rel, "internal/")
	rel = strings.TrimPrefix(rel, "tests/")
	rel = strings.TrimPrefix(rel, "pkg/")
	rel = strings.TrimPrefix(rel, "cmd/")
	return rel
}
