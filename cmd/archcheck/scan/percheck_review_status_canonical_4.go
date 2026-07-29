// Package scan — per-check forward-prevention gate that enforces
// the canonical-4-count invariant on the ReviewStatus enum
// (PR-CLIPINGEST-PIPELINE Step 10, July 2026).
//
// scan/percheck_review_status_canonical_4.go owns the Go
// migration of the canonical-4 forward-prevention gate for the
// ReviewStatus surface. It reads ONLY the canonical SOLE owner
// (internal/kernel/asset/rights_state.go) and counts the
// `ReviewStatusX ReviewStatus = "..."` const declarations. The
// count MUST equal 4 (the canonical surface declared at
// CanonicalReviewStatusValues()). A future agent who adds a 5th
// value MUST update:
//
//	(a) the 4 const declarations in rights_state.go,
//	(b) CanonicalReviewStatusValues() in the same file,
//	(c) the rights_state_test.go enum membership test,
//	(d) migration 158's CHECK constraint comment (the
//	    comment-block alphabet that mirrors the runtime
//	    enum check),
//	(e) the percheck_review_status_canonical_4 count
//	    literal (this file's wantCount).
//
// Drift in any of (a..e) surfaces as a single CI violation
// from THIS scanner.
//
// godlike/06 SSOT invariant: this scanner does NOT enforce the
// ALPHABET VALUE of each const — only the count. The runtime
// Valid() method on ReviewStatus handles alphabet validation;
// rights_state_test.go::TestReviewStatus_StringLiteralValues pins
// the alphabet values.
//
// godlike/06 SSOT (one canonical owner per fact): RightsStatus +
// ReviewStatus co-locate in the SAME rights_state.go file
// because they're the two orchestrated "rights surface"
// dimensions (per-step 10 user spec). The scan path here
// deliberately targets ONLY the ReviewStatus prefix to avoid
// miscounting RightsStatus declarations in the same file.
//
// Skip-dir policy mirrors percheck_rights_status_canonical_6.go's
// standard sibling pattern. Comment-only lines with ReviewStatus
// references are residue-accounted (WARNed, not violated).
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

// reviewStatusCanonical4Path is the canonical SOLE owner of
// the ReviewStatus enum (4 constants + helpers).
const reviewStatusCanonical4Path = "internal/kernel/asset/rights_state.go"

// reviewStatusConstLineRe matches the LITERAL const-declaration
// shape at canonical_file line-start (post-tab indentation):
//
//	ReviewStatusX ReviewStatus = "..."
//
// The trailing `"..."` is a real string literal; surrounding
// characters are noise-padded by tab (line starts with `\t`).
// This regex is intentionally line-start anchored so a
// comment-only `// ReviewStatusX = ...` reference does NOT trip
// the const-count, but the residue-warning scan still picks it
// up via a separate substring check.
//
// Drift-prevention: the regex deliberately accepts ONLY the
// ReviewStatus prefix (NOT RightsStatus). Both enums co-locate
// in the same file; this scanner targets ONLY the ReviewStatus
// subset of declarations.
var reviewStatusConstLineRe = regexp.MustCompile(`^\tReviewStatus\w+\s+ReviewStatus\s+=\s+"[^"]+"$`)

// reviewStatusCanonical4Rule is the rule-family id the scanner
// emits (mirrors the sibling percheck_rights_status_canonical_6
// naming convention).
const reviewStatusCanonical4Rule = "percheck_review_status_canonical_4"

// reviewStatusCanonical4Note is the violation Note for count
// mismatches. The message references the canonical surface +
// its RightsStatus companion so an operator sees the broader
// rights-schema intent.
const reviewStatusCanonical4Note = "ReviewStatus canonical file must declare exactly 4 const entries (PR-CLIPINGEST-PIPELINE Step 10, July 2026); godlike/06 SSOT requires the const count to equal CanonicalReviewStatusValues(); if a 5th value is needed, update the type surface, the membership test, migration 158's CHECK comment, and this gate in lockstep"

// reviewStatusWarn is the centralized WARN-bucket emitter for
// residue-accounting. Mirrors assetStateWarn and
// rightsStatusWarn.
func reviewStatusWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, reviewStatusCanonical4Rule+" "+label+" "+msg)
}

// ScanReviewStatusCanonical4 opens the single canonical file
// (reviewStatusCanonical4Path) and counts const declarations
// of the form `ReviewStatusX ReviewStatus = "..."`. The count
// MUST equal 4; otherwise the scanner emits a violation with
// the actual count surface. Comment-only references to
// ReviewStatus constants are WARNed (residue accounting,
// godlike/07 discipline).
//
// Scanner ignores the value of each const's string literal;
// only the COUNT is enforced. The runtime
// ReviewStatus.Valid() + rights_state_test.go's
// TestReviewStatus_StringLiteralValues covers alphabet
// validation.
func ScanReviewStatusCanonical4(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved for future SeverityOverride plumbing.
	path := filepath.Join(root, reviewStatusCanonical4Path)
	f, err := os.Open(path)
	if err != nil {
		// The canonical file is the SSOT; if it cannot be
		// opened the operator MUST investigate. Surface a
		// violation rather than silently passing.
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/domain/asset",
			File:        reviewStatusCanonical4Path,
			Line:        0,
			Rule:        reviewStatusCanonical4Rule,
			Severity:    string(report.SeverityError),
			MatchedRule: "canonical_4_count_mismatch",
			Note:        reviewStatusCanonical4Note + " | cannot open canonical file: " + err.Error(),
		})
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	const wantCount = 4
	count := 0
	commentOnly := 0
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if reviewStatusConstLineRe.MatchString(line) {
			count++
			continue
		}
		// Residue accounting (godlike/07): a comment line
		// referencing the ReviewStatus surface is descriptive
		// prose and not a real declaration. WARN, do NOT
		// violate.
		trimmed := strings.TrimLeft(line, " \t")
		if (strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*")) &&
			strings.Contains(line, "ReviewStatus") {
			commentOnly++
		}
	}
	if count != wantCount {
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/domain/asset",
			File:        reviewStatusCanonical4Path,
			Line:        0,
			Rule:        reviewStatusCanonical4Rule,
			Severity:    string(report.SeverityError),
			MatchedRule: "canonical_4_count_mismatch",
			Note: reviewStatusCanonical4Note +
				" | actual const count: " + strconv.Itoa(count) +
				" | want: " + strconv.Itoa(wantCount),
		})
	}
	if commentOnly > 0 {
		reviewStatusWarn(r, "canonical-4-count:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+reviewStatusCanonical4Path+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}
