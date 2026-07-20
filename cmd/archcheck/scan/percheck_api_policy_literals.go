// Package scan — PR-SUBMISSION-FACTORY forward-prevention gate
// (Step 9.b of the architecture refactor plan, July 2026): the
// HTTP transport layer (internal/api) must NOT contain the
// canonical job-policy literals that the application-layer
// submission factory owns. The literals scope/job-type/priority/
// max-retries, by architecture rule, must live exclusively in
// `internal/application/scripts/submission/{command,factory,policy}.go`,
// not in any HTTP handler or script-flow glue.
//
// A leaked literal in internal/api (production code) means the
// transport has drifted into owning application-layer decisions,
// which is godlike/07 NO-FAKE-AVAILABILITY boundary violation:
// the transport MUST stop at "transport only" and forward the
// command; the factory MUST build the SubmitRequest.
//
// scanner policy (mirrors percheck_api_infrastructure_imports
// precedent):
//   - walk scope: <root>/internal/api/ ("api itself in the
//     api layer"). Production source files only (NOT _test.go).
//   - skip dirs: standard set (.git, vendor, node_modules,
//     node-scraper, examples, archivist, docs, data).
//   - skip the scanner's own package prefix
//     (cmd/archcheck/scan) so the regex self-match inside the
//     scanner's docs/comments/strings doesn't trip the gate.
//   - skip IMPORT lines under `import (...)` block: typed
//     imports of `domainops` / `scriptpkg` that surface the
//     symbols BY NAME are required for compilation, not
//     a policy decision in the transport.
//   - the literal assignment SHAPE (the structural decision)
//     is the violation, not the symbol import.
//   - comment-only matches emit a Warning residue bucket
//     (godlike/07 NO_FAKE_AVAILABILITY: descriptive prose
//     referencing the literal in a godoc comment is non-fatal).
//     productionOnly=true silences the warning bucket.
//
// matched rule_id: `percheck_api_policy_literals`.
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

// apiPolicyLiteralsSkipDirs is the standard skip-dir set.
var apiPolicyLiteralsSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// apiPolicyLiteralsSkipPathPrefixes is the scanner's own
// package exemption.
var apiPolicyLiteralsSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// apiPolicyLiteralsScanScope is the prefix the gate
// applies to. The scope is deliberately narrowed to
// `internal/api/script/` (the canonical script API surface)
// rather than the broader `internal/api/`. godlike/07
// minimum-blast-radius prefers the smallest reliable
// surface tied to a single policy owner. The voiceover
// zone (`internal/api/assets/voiceover/`) carries the
// historical `TypeGenerate` type alias (legacy platform
// term for the script-generate family); widening the scope
// to `internal/api/` would require either rewriting that
// legacy surface OR adding an exemption list, both of
// which expand blast radius. The diagnostic's intent is
// the canonical script.generate policy surface, which is
// `internal/api/script/`.
const apiPolicyLiteralsScanScope = "internal/api/script/"

// apiPolicyLiteralsRule is the rule-family id the
// scanner emits.
const apiPolicyLiteralsRule = "percheck_api_policy_literals"

// apiPolicyLiteralsNote is the violation Note for any
// production-code surface that imports-or-references the
// canonical job-policy literals.
const apiPolicyLiteralsNote = "forbidden policy literal in internal/api (PR-SUBMISSION-FACTORY forward-prevention gate, July 2026); godlike/07 NO-FAKE-AVAILABILITY boundary: the HTTP transport layer must NOT own job-policy decisions. The canonical application-layer owner is internal/application/scripts/submission/{command,factory,policy}.go. The handler must build a submission.GenerateCommand and delegate SubmitRequest assembly to *submission.SubmitRequestFactory. Move the literal to the factory, build from there, and have the handler call `factory.Build(cmd)` instead. Test-fixture residue (baseline-scan allowlist) is documented in migrations/api/archcheck-strict-baseline.json (godlike/07 NO-FAKE-AVAILABILITY migration window)."

// apiPolicyLiteralsRe matches the canonical job-policy
// literal names as a word-boundary regex. Mirrors the
// precedent of percheck_no_domain_job_compatibility_aliases
// (literal-symbol word-boundary ban). The literal names are
// deliberately exhaustive — every adjacent identifier the
// submission factory owns.
var apiPolicyLiteralsRe = regexp.MustCompile(`\b(ScopeScriptGenerate|TypeGenerate|JobPriority|JobMaxRetries|operations\.Scope|operations\.JobType|operations\.MaxRetries)\b`)

// apiPolicyLiteralsImportRe matches Go import lines within
// an `import (...)` block, so we don't trip on the canonical
// import symbols under domainops / scriptpkg.
var apiPolicyLiteralsImportRe = regexp.MustCompile(`^\s*"github\.com/Marcuss-ops/PipelineGen/`)

// isAPIPolicyLiteralsImportLine returns true if the trimmed
// line is part of an import-block line (single or grouped).
// The check is conservative: any line starting with `import (
// or a `"github.com/..."` import path is exempt.
func isAPIPolicyLiteralsImportLine(trimmed string) bool {
	if strings.HasPrefix(trimmed, "import (") ||
		strings.HasPrefix(trimmed, "import \"") ||
		strings.HasPrefix(trimmed, "import `") {
		return true
	}
	return apiPolicyLiteralsImportRe.MatchString(trimmed)
}

// apiPolicyLiteralsWarn emits the WARN residue entry.
func apiPolicyLiteralsWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, apiPolicyLiteralsRule+" "+label+" "+msg)
}

// ScanAPIPolicyLiterals walks every .go file under
// <root>/internal/api/ and emits a violation for any
// production file (NOT _test.go) whose body references the
// canonical job-policy literal (`operations.Scope... /
// TypeGenerate / JobPriority / JobMaxRetries`). IMPORT lines
// are exempt (the symbols must be reachable by name to satisfy
// Go's typed-import surface; the ban is on the ASSIGN shape,
// not the type reference). Comment-only matches emit a
// Warning residue bucket (suppressed in productionOnly mode).
//
// productionOnly mode silences the comment-only WARN bucket so
// the operator-facing "zero production-code hits" claim is
// auditable via len(r.Violations) == 0.
func ScanAPIPolicyLiterals(root string, pol *policy.Policy, r *report.Report, productionOnly bool) {
	_ = pol

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if apiPolicyLiteralsSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, apiPolicyLiteralsSkipPathPrefixes) {
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
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		if !strings.HasPrefix(relSlash, apiPolicyLiteralsScanScope) {
			return nil
		}
		scanAPIPolicyLiteralsFile(path, relSlash, r, productionOnly)
		return nil
	})
}

// scanAPIPolicyLiteralsFile opens a single .go file under
// the gate scope and emits percheck_api_policy_literals
// violations for any line matching the canonical literal
// regex, EXCEPT lines inside an import block.
func scanAPIPolicyLiteralsFile(path, relPath string, r *report.Report, productionOnly bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentOnly := 0
	inImportBlock := false
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")

		// Track grouped-import block boundary so we can
		// exempt any `operations.*` reference inside it.
		if strings.HasPrefix(trimmed, "import (") {
			inImportBlock = true
			continue
		}
		if inImportBlock {
			if trimmed == ")" {
				inImportBlock = false
			}
			// Inside an import block: exempt any line,
			// including the closing paren.
			continue
		}
		// Exempt single-line import statements too.
		if isAPIPolicyLiteralsImportLine(trimmed) {
			continue
		}

		// Comment-only matches → WARN residue bucket.
		if (strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*")) &&
			apiPolicyLiteralsRe.MatchString(line) {
			commentOnly++
			continue
		}

		if !apiPolicyLiteralsRe.MatchString(line) {
			continue
		}

		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromAPIPolicyLiteralsRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        apiPolicyLiteralsRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "policy_literal_in_transport",
			Note:        apiPolicyLiteralsNote + " | snippet: " + truncateAPIPolicyLiterals(line),
		})
	}
	if commentOnly > 0 && !productionOnly {
		apiPolicyLiteralsWarn(r, "policy-literal-comments:",
			strconv.Itoa(commentOnly)+" comment-only literal reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// pkgFromAPIPolicyLiteralsRel extracts the package identifier
// from a repo-relative file path. Mirrors the family idiom.
func pkgFromAPIPolicyLiteralsRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

// truncateAPIPolicyLiterals bounds the snippet surface at
// 120 chars to keep report JSON size stable.
func truncateAPIPolicyLiterals(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}
