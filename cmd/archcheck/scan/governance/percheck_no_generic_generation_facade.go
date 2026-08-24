// Package scan — percheck_no_generic_generation_facade.go
// (PR-GENERATION-FACADE-REMOVE, commit 7, July 2026).
//
// Forward-prevention per-check that BANS the import of the
// deleted generic generation facade packages from ANY production
// .go file OUTSIDE the canonical exempt set:
//
//  1. cmd/archcheck/scan/** — the scanner's own package references
//     the banned paths in prose/disabled-rule context (this file
//     documents the ban; mirrors the family precedent from
//     percheck_qdrant_index_import_ban.go).
//
// Per the user directive (Italian, July 2026): "Elimina
// internal/application/generation, internal/domain/generation, il
// modulo dal registry, la rotta /generations, i tipi
// Definition/Registry/ScriptSource/BatchSource/GenerationDescriptor,
// il generic handler e il generic job mapping." The application-zone
// facade (`internal/application/generation/`) + the domain-zone facade
// (`internal/domain/generation/`) were both git-rm'd in commit 7;
// the canonical proprietary APIs (book/lesson/script/batch) did
// NOT exist on disk (`internal/api/content/` is a doc-only shell
// per the conf-inventory report), so the facade removal is
// acceptable (per user spec: "Restano le API canoniche
// proprietarie per book, lesson, script, batch se esistono e
// sono reali").
//
// Banned import paths (the canonical banned literals):
//   - "github.com/Marcuss-ops/PipelineGen/internal/capabilities/generation"
//   - "github.com/Marcuss-ops/PipelineGen/internal/domain/generation"
//
// Scan scope is `internal/**` + `cmd/**` (external CLI surfaces
// may reference the facade from operator tooling — production
// Go code under one of these trees is the target). The
// composition root at `internal/app/` is included because the
// pre-commit-7 days had `internal/app/registry_public_modules.go`
// import the facade — the gate catches any regression.
//
// _test.go files are exempt (regression-guard surface legitimately
// needs fixture import setups). A file that declares
// `// GENERATION_FACADE_SCOPE: <reason>` in its header godoc is
// also exempt: the marker documents an EXPLICIT allowlist for
// edge cases. Today NO file uses this marker; the allowlist
// exists for future drift.
//
// Comment-only references to the banned import paths are
// residue-accounted (godlike/07) — descriptive prose is non-fatal
// but emits a WARN so operator dashboards can spot drift.
//
// Matched rule_id: `percheck_no_generic_generation_facade`.
package governance

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

// generationFacadeSkipDirs mirrors the standard sibling scanning
// policy from percheck_qdrant_index_import_ban.go +
// percheck_mediatransformer_no_infra_fields.go.
var generationFacadeSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
	"scripts":      true,
}

// generationFacadeSkipPathPrefixes is the scanner-package-exemption
// set: cmd/archcheck/scan references the banned paths in prose
// (this file documents the ban + the file basenames of the
// deleted directories may appear in historical `git log` /
// `Migration log` dec comments).
var generationFacadeSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// generationFacadeScanScope is the prefix set the gate applies
// to. Both internal/** (production code) AND cmd/** (CLI tooling)
// are in scope because the pre-commit-7 days had the composition
// root (`internal/app/`) + operational tooling (`cmd/admin/**`)
// reference the facade from production code paths.
var generationFacadeScanScopes = []string{
	"internal/",
	"cmd/",
}

// generationFacadeBannedPathApp is the application-zone facade
// import path the gate detects. The regex anchors on this fully-
// qualified package path so an import like
// `"github.com/Marcuss-ops/PipelineGen/internal/capabilities/generation/response"`
// is matched at the literal line carrying the import statement.
const generationFacadeBannedPathApp = "github.com/Marcuss-ops/PipelineGen/internal/capabilities/generation"

// generationFacadeBannedPathDomain is the domain-zone facade
// import path the gate detects. Same shape as the application-
// zone ban above; covers the legacy `internal/domain/generation/`
// surface that carried the canonical StatusSucceeded / StatusFailed
// enum + Generator interface before commit 7.
const generationFacadeBannedPathDomain = "github.com/Marcuss-ops/PipelineGen/internal/domain/generation"

// generationFacadeAppRe matches the import line shape for the
// application-zone facade. The terminator `(/|")` eliminates
// false positives on strings like "...generation-services...".
var generationFacadeAppRe = regexp.MustCompile(`"` + regexp.QuoteMeta(generationFacadeBannedPathApp) + `(/|")`)

// generationFacadeDomainRe matches the import line shape for the
// domain-zone facade. Same terminator pattern.
var generationFacadeDomainRe = regexp.MustCompile(`"` + regexp.QuoteMeta(generationFacadeBannedPathDomain) + `(/|")`)

// generationFacadeRule is the rule-family id the scanner emits.
const generationFacadeRule = "percheck_no_generic_generation_facade"

// generationFacadeNote is the violation Note for any
// re-introduction of either banned import path. The message
// references the canonical replacement surfaces (the proprietary
// books/lessons/scripts packages per their canonical pipelines)
// so the operator sees the migration path inline.
const generationFacadeNote = "forbidden import of the retired generic generation facade (commit 7, PR-GENERATION-FACADE-REMOVE, July 2026). The application-zone surface ('github.com/Marcuss-ops/PipelineGen/internal/capabilities/generation') + the domain-zone surface ('github.com/Marcuss-ops/PipelineGen/internal/domain/generation') were git-rm'd in commit 7 because zero production callers remained. The canonical proprietary APIs (book/lesson/script/batch) — if they exist in a future commit — live at the per-domain packages (internal/application/books/, internal/application/lessons/, internal/application/scripts/) and consume the runtime-driver interfaces directly without an interposed 'generation' facade. Per godlike/06 SSOT, the per-domain packages own their handler wiring; re-introducing the generic facade creates a godlike/07 NO-FAKE-AVAILABILITY regression. The forward-prevention gate is percheck_no_generic_generation_facade"

// generationFacadeWarnBucket is the centralized residue-emitter.
// Mirrors qdrantImportBanWarnBucket + indexedStateWriterSSOTWarnBucket.
func generationFacadeWarnBucket(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, generationFacadeRule+" "+label+" "+msg)
}

// ScanNoGenericGenerationFacade walks every .go file under
// <root>/internal/** + <root>/cmd/** and emits a violation for
// any production file (NOT _test.go) that imports either of the
// deleted facade import paths. The scanner package
// (cmd/archcheck/scan/**) is exempt. Comment-only references to
// the banned import paths are residue-accounted as WARN
// (godlike/07).
//
// productionOnly=true (PR-P12-PERCHECK-BASELINE-ZERO, July 2026,
// deadline 2026-08-15): silences the comment-only WARN bucket
// so the operator-facing "zero production-code hits" claim is
// auditable via len(r.Violations) == 0. Mirrors the family
// precedent from percheck_voiceover_alias_ban.go +
// percheck_root_override.go + percheck_providers_searchaggregator_ban.go.
func ScanNoGenericGenerationFacade(root string, pol *policy.Policy, r *report.Report, productionOnly bool) {
	_ = pol // reserved for future SeverityOverride plumbing.

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if generationFacadeSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, generationFacadeSkipPathPrefixes) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		// Test files are exempt — regression-guard surface
		// legitimately needs fixture import setups.
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		// Out-of-scope: only scan internal/** + cmd/** files.
		inScope := false
		for _, scope := range generationFacadeScanScopes {
			if strings.HasPrefix(relSlash, scope) {
				inScope = true
				break
			}
		}
		if !inScope {
			return nil
		}
		scanGenerationFacadeFile(path, relSlash, r, productionOnly)
		return nil
	})
}

// scanGenerationFacadeFile opens a single .go file and emits
// percheck_no_generic_generation_facade violations for any line
// whose content matches either generationFacadeAppRe or
// generationFacadeDomainRe. Comment-only references are residue-
// accounted as WARN (godlike/07 discipline). Silenced under
// productionOnly=true (Configuration detail documented on
// ScanNoGenericGenerationFacade).
func scanGenerationFacadeFile(path, relPath string, r *report.Report, productionOnly bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		// Residue accounting (godlike/07): comment-only
		// references to the banned import paths are descriptive
		// prose, not real imports. WARN, do NOT violate.
		// Silenced under productionOnly=true so the
		// "zero production-code hits" claim is auditable.
		if (strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*")) &&
			(strings.Contains(line, generationFacadeBannedPathApp) ||
				strings.Contains(line, generationFacadeBannedPathDomain)) {
			commentOnly++
			continue
		}
		matchedSurface := ""
		switch {
		case generationFacadeAppRe.MatchString(line):
			matchedSurface = "application-zone"
		case generationFacadeDomainRe.MatchString(line):
			matchedSurface = "domain-zone"
		default:
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromGenerationFacadeRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        generationFacadeRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "generic_generation_facade_import_attempt:" + matchedSurface,
			Note:        generationFacadeNote + " | snippet: " + truncateGenerationFacade(line),
		})
	}
	if commentOnly > 0 && !productionOnly {
		generationFacadeWarnBucket(r, "generic-generation-facade-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// pkgFromGenerationFacadeRel extracts the package identifier
// from a repo-relative file path. Mirrors pkgFromQdrantImportBanRel.
func pkgFromGenerationFacadeRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

// truncateGenerationFacade bounds the snippet surface at 120 chars
// to keep report JSON size stable. Mirrors truncateQdrantImportBan.
func truncateGenerationFacade(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}
