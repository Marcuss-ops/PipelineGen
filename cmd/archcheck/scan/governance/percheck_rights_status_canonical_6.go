// Package scan — per-check forward-prevention gate that enforces
// the canonical-6-count invariant on the RightsStatus enum
// (PR-CLIPINGEST-PIPELINE Step 10, July 2026).
//
// scan/percheck_rights_status_canonical_6.go owns the Go
// migration of the canonical-6 forward-prevention gate for the
// new RightsStatus surface. It reads ONLY the canonical SOLE
// owner (internal/kernel/asset/rights_state.go) and counts the
// `RightsStatusX RightsStatus = "..."` const declarations. The
// count MUST equal 6 (the canonical surface declared at
// CanonicalRightsStatusValues()). A future agent who adds a
// 7th value MUST update:
//
//	(a) the 6 const declarations in rights_state.go,
//	(b) CanonicalRightsStatusValues() in the same file,
//	(c) the rights_state_test.go enum membership test,
//	(d) migration 158's CHECK constraint comment (the
//	    comment-block alphabet that mirrors the runtime
//	    enum check),
//	(e) the percheck_rights_status_canonical_6 count
//	    literal (this file's wantCount).
//
// Drift in any of (a..e) surfaces as a single CI violation
// from THIS scanner; the comment-based godlike/07 residue-
// accounting warns them as well.
//
// godlike/06 SSOT invariant: this scanner does NOT enforce the
// ALPHABET VALUE of each const — only the count. The shadow-
// enum scanner pattern (mirrors percheck_asset_state_no_shadow_enum)
// would catch duplicate declarations outside the canonical file;
// this scanner focuses on the COUNT invariant alone, leaving
// alphabet validation to the runtime Valid() method and the
// rights_state_test.go TestRightsStatus_StringLiteralValues test.
//
// Skip-dir policy mirrors percheck_asset_state_canonical_14.go's
// standard sibling pattern. Comment-only lines with RightsStatus
// references are residue-accounted (WARNed, not violated),
// godlike/07 discipline.
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

// rightsStatusCanonical6Path is the canonical SOLE owner of
// the RightsStatus enum (6 constants + helpers).
const rightsStatusCanonical6Path = "internal/kernel/asset/rights_state.go"

// rightsStatusConstLineRe matches the LITERAL const-declaration
// shape at canonical_file line-start (post-tab indentation):
//
//	RightsStatusX RightsStatus = "..."
//
// The trailing `"..."` is a real string literal; surrounding
// characters are noise-padded by tab (line starts with `\t`).
// This regex is intentionally line-start anchored so a
// comment-only `// RightsStatusX = ...` reference does NOT trip
// the const-count, but the residue-warning scan still picks it
// up via a separate substring check.
//
// NOTE: the regex intentionally accepts BOTH ` RightsStatus`
// (e.g. RightsStatusOwned) AND any ` RightsStatus_other_type`
// pattern (for future flexibility) — but for Step 10 the only
// declared values are RightsStatusX, so drift in prefix naming
// surfaces as a count mismatch.
var rightsStatusConstLineRe = regexp.MustCompile(`^\tRightsStatus\w+\s+RightsStatus\s+=\s+"[^"]+"$`)

// rightsStatusCanonical6Rule is the rule-family id the scanner
// emits (mirrors percheck_asset_state_canonical_14.go's RuleID
// naming convention).
const rightsStatusCanonical6Rule = "percheck_rights_status_canonical_6"

// rightsStatusCanonical6Note is the violation Note for count
// mismatches. The message references the canonical surface +
// the forward-prevention guard at percheck_review_status_canonical_4
// so an operator sees both the RightsStatus count constraint AND
// its ReviewStatus companion gate.
const rightsStatusCanonical6Note = "RightsStatus canonical file must declare exactly 6 const entries (PR-CLIPINGEST-PIPELINE Step 10, July 2026); godlike/06 SSOT requires the const count to equal CanonicalRightsStatusValues(); if a 7th value is needed, update the type surface, the membership test, migration 158's CHECK comment, and this gate in lockstep"

// rightsStatusWarn is the centralized WARN-bucket emitter for
// residue-accounting. Mirrors assetStateWarn.
func rightsStatusWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, rightsStatusCanonical6Rule+" "+label+" "+msg)
}

// ScanRightsStatusCanonical6 opens the single canonical file
// (rightsStatusCanonical6Path) and counts const declarations
// of the form `RightsStatusX RightsStatus = "..."`. The count
// MUST equal 6; otherwise the scanner emits a violation with the
// actual count surface. Comment-only references to RightsStatus
// constants are WARNed (residue accounting, godlike/07 discipline).
//
// Scanner ignores the value of each const's string literal;
// only the COUNT is enforced. The shadow-enum scanner pattern
// (NOT YET SHIPPED for RightsStatus — see percheck_asset_state_no_shadow_enum
// for the pattern) catches duplicate declarations outside the
// canonical file; the alphabet value drift is captured at
// runtime by rights_state_test.go's TestRightsStatus_StringLiteralValues.
func ScanRightsStatusCanonical6(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved for future SeverityOverride plumbing.
	path := filepath.Join(root, rightsStatusCanonical6Path)
	f, err := os.Open(path)
	if err != nil {
		// The canonical file is the SSOT; if it cannot be
		// opened the operator MUST investigate. Surface a
		// violation rather than silently passing.
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/kernel/asset",
			File:        rightsStatusCanonical6Path,
			Line:        0,
			Rule:        rightsStatusCanonical6Rule,
			Severity:    string(report.SeverityError),
			MatchedRule: "canonical_6_count_mismatch",
			Note:        rightsStatusCanonical6Note + " | cannot open canonical file: " + err.Error(),
		})
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	const wantCount = 6
	count := 0
	commentOnly := 0
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if rightsStatusConstLineRe.MatchString(line) {
			count++
			continue
		}
		// Residue accounting (godlike/07): a comment line
		// referencing the RightsStatus surface is descriptive
		// prose and not a real declaration. WARN, do NOT
		// violate.
		trimmed := strings.TrimLeft(line, " \t")
		if (strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*")) &&
			strings.Contains(line, "RightsStatus") {
			commentOnly++
		}
	}
	if count != wantCount {
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/kernel/asset",
			File:        rightsStatusCanonical6Path,
			Line:        0,
			Rule:        rightsStatusCanonical6Rule,
			Severity:    string(report.SeverityError),
			MatchedRule: "canonical_6_count_mismatch",
			Note: rightsStatusCanonical6Note +
				" | actual const count: " + strconv.Itoa(count) +
				" | want: " + strconv.Itoa(wantCount),
		})
	}
	if commentOnly > 0 {
		rightsStatusWarn(r, "canonical-6-count:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+rightsStatusCanonical6Path+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}
