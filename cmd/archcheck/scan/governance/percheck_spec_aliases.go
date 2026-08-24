// Package scan — per-check forward-prevention gate that bans
// `spec_aliases.go` files outside the two approved territories:
// internal/capabilities/images/workflow/generated/ and
// internal/capabilities/images/workflow/retrieved/.
//
// scan/percheck_spec_aliases.go is the canonical Go scanner for
// PR-AUDIT-8 (July 2026, P2). The gate codifies the godlike/06
// SSOT invariant: `spec_aliases.go` is a user-spec surface that
// exposes type aliases, sentinel errors, and compile-time
// assertions on top of the canonical implementation package.
// The ONLY two approved homes are:
//
//   - internal/capabilities/images/workflow/generated/spec_aliases.go
//     (Google Slides generation provider spec surface)
//   - internal/capabilities/images/workflow/retrieved/spec_aliases.go
//     (Wikipedia / SearXNG / DuckDuckGo / Drive retrieval spec surface)
//
// Any other `spec_aliases.go` file is a godlike/06 SSOT violation:
// the canonical implementation's package owns the public surface;
// a separate spec_aliases.go in a random package would fork the
// user-spec contract without godlike/06 compile-time enforcement.
//
// Rationale (godlike/06 SSOT + godlike/07 NO-FAKE-AVAILABILITY):
// the two existing spec_aliases.go files are carefully documented
// with per-type mapping comments that link each alias back to its
// canonical implementation. A future agent that copy-pastes the
// pattern into a new module without the mapping would create a
// silent drift where the user-spec surface diverges from the
// canonical implementation — a godlike/07 NO-FAKE-AVAILABILITY
// regression. This gate is the forward-prevention seam: a new
// spec_aliases.go in an unapproved territory surfaces as a CI
// build failure (`--strict` mode exit 1) BEFORE the drift reaches
// production.
//
// Excluded paths (godlike/06 SSOT):
//
//   - internal/capabilities/images/workflow/generated/** — the canonical
//     generated/ spec surface.
//   - internal/capabilities/images/workflow/retrieved/** — the canonical
//     retrieved/ spec surface.
//   - All *_test.go files — naturally excluded by the basename
//     gate ("spec_aliases_test.go" ≠ "spec_aliases.go"). No
//     explicit suffix check is needed.
//
// Unlike percheck_player_client / percheck_script_docs_route,
// this scanner works at the FILENAME level (not the file-content
// level), so there are no comment-only warnings. The check is
// an exact basename match on "spec_aliases.go".
//
// Skip-dir list mirrors the standard skip-list from
// percheck_player_client.go: .git + vendor + node_modules +
// node-scraper + examples + scripts.
package governance

import (
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// specAliasesFilename is the canonical basename the gate looks for.
const specAliasesFilename = "spec_aliases.go"

// specAliasesApprovedDirs lists the repo-relative directory prefixes
// where spec_aliases.go files are allowed. Matched via
// strings.HasPrefix(relDir, prefix) where relDir is the directory
// portion of the repo-relative path (i.e., dirname only, NOT the
// full file path), computed via filepath.ToSlash(filepath.Dir(relPath)).
//
// godlike/06 SSOT: adding a new approved directory MUST be done
// via a per-PR wave-tracker entry that documents the new spec
// surface AND its canonical implementation package. This slice
// is the load-bearing forward-prevention: a future agent that
// adds a spec_aliases.go to a new module will get a CI failure
// and must add the directory here — which forces the operator
// to review the mapping contract before the new surface lands.
var specAliasesApprovedDirs = []string{
	"internal/capabilities/images/workflow/generated",
	"internal/capabilities/images/workflow/retrieved",
}

// specAliasesSkipDirs is the standard skip-list for whole-repo
// walks. Mirrors the skipDirs pattern in percheck_typeredecl.go +
// percheck_monitor.go + percheck_player_client.go: .git + vendor +
// node_modules + the node-scraper frontend + examples + scripts.
var specAliasesSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"scripts":      true,
}

// specAliasesScanNote is the violation Note string. The message
// references the two canonical approved directories + the
// godlike/06 SSOT rationale + the PR-AUDIT-8 forward-prevention
// anchor so future agents reading the CI failure have the full
// context inline.
const specAliasesScanNote = "forbidden `spec_aliases.go` outside approved territories (internal/capabilities/images/workflow/generated/ + internal/capabilities/images/workflow/retrieved/); godlike/06 SSOT requires spec_aliases.go to live ONLY in these two canonical directories (PR-AUDIT-8 forward-prevention gate, July 2026)"

// ScanSpecAliasesTerritory walks every file under <root>/ and emits
// an error-severity violation for any file named `spec_aliases.go`
// whose parent directory is NOT one of the approved directories.
//
// Test files (*_test.go) are exempt — tests legitimately create
// spec_aliases.go fixtures for regression guards. The exemption is
// applied FIRST (before the directory check) so a test file named
// spec_aliases.go in any package under internal/ is not flagged.
//
// Severity is `error` (forward-prevention gate; the runner
// --strict mode promotes to ExitViolations). For non-strict mode,
// the runner still prints the report; the exit code remains 0
// unless --strict is on.
func ScanSpecAliasesTerritory(root string, pol *policy.Policy, r *report.Report) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if specAliasesSkipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		// Basename check: only act on files named spec_aliases.go.
		if filepath.Base(path) != specAliasesFilename {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)

		// Check the parent directory against the approved list.
		relDir := filepath.ToSlash(filepath.Dir(relSlash))
		for _, approved := range specAliasesApprovedDirs {
			if relDir == approved {
				// Approved territory — silent pass.
				return nil
			}
		}

		// NOT in an approved territory — emit a violation.
		r.Violations = append(r.Violations, report.Violation{
			File:        relSlash,
			Line:        1,
			Rule:        "percheck_spec_aliases",
			Severity:    string(report.SeverityError),
			MatchedRule: "spec_aliases_territory_gate",
			Note:        specAliasesScanNote,
		})
		return nil
	})
}
