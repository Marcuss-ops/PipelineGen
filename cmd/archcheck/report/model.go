// Package report — archcheck JSON report model.
//
// report/model.go owns the on-wire shape of the cmd/archcheck report.
// Three types are emitted by the Phase 0 scan functions (Report,
// Violation, Summary); three are forward-looking placeholders for the
// PR4 runner (Finding, Severity, Suggestion).
//
// Package boundary: `package report` (separate from `package main` of
// cmd/archcheck) so the report subdirectory holds a focused concern:
// "what does the on-wire JSON look like in Go?" — independent of how
// the scan functions produce it. The boundary lets the snapshot test
// (cmd/archcheck/runner_test.go) import the model without dragging
// the scan logic into the test binary.
//
// Import graph (no cycles):
//
//	main     → policy, report
//	report   → policy       (Report.Policy is *policy.Policy)
//	policy   → (nothing internal)  (Rule.Check returns []any)
//
// JSON contract: the field tags on Report, Violation, and Summary
// ARE the public API. Downstream CI dashboards and jq pipelines
// depend on every key. Adding a field is safe (consumers that don't
// know about it ignore it). Renaming or removing a field is a
// breaking change that requires a major-version bump on the
// report's `mode` label (currently "target-tree-dry-run").
package report

import "github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"

// Report is the JSON document printed on stdout.
type Report struct {
	Passed        bool            `json:"passed"`
	Mode          string          `json:"mode"`
	PolicyPath    string          `json:"policy_path"`
	Root          string          `json:"scan_root"`
	Phase         string          `json:"phase"`
	Policy        *policy.Policy  `json:"policy_snapshot"`
	Summary       Summary         `json:"summary"`
	Violations    []Violation     `json:"violations"`
	Grandfathered []string        `json:"grandfathered_known"`
	// Warnings is the per-run audit-pin residue accounting surface
	// (godlike/07 no-fake-availability). Comment-only hits +
	// ARCH-ALLOWLIST marker sites in per-check checks (e.g. Check 54
	// monitor-infra-import ban) are logged here so future drift is
	// visible in CI output every run, without contributing to the
	// hard-fail Violations set. The field is forward-compatible
	// (existing JSON consumers ignore unknown fields) so Phase 0
	// reports without a Warnings entry remain byte-stable in their
	// other fields. PR-ARCHCHECK-GO-MIGRATION-PHASE-1 (July 2026)
	// is the first PR to populate the field.
	Warnings []string `json:"warnings,omitempty"`
}

// Violation is the JSON shape emitted per rule violation.
type Violation struct {
	Package      string `json:"package,omitempty"`
	Directory    string `json:"directory,omitempty"`
	File         string `json:"file,omitempty"`
	Line         int    `json:"line,omitempty"`
	ActualCount  int    `json:"actual_count,omitempty"`
	AllowedCount int    `json:"allowed_count,omitempty"`
	ActualLines  int    `json:"actual_lines,omitempty"`
	MaxLines     int    `json:"max_lines,omitempty"`
	MatchedRule  string `json:"matched_rule,omitempty"`
	Rule         string `json:"rule"`
	Severity     string `json:"severity"`
	Note         string `json:"note,omitempty"`
}

// Summary groups violation counts by rule id and severity.
type Summary struct {
	TotalViolations int            `json:"total_violations"`
	ByReason        map[string]int `json:"by_reason"`
	BySeverity      map[string]int `json:"by_severity"`
}

// Finding is a forward-looking alias for Violation. Phase 0 emits
// Violation structs; the Finding alias is the future-facing rename
// for Phase 1+ once the violation family is extended with rule-id
// and suggestion references. Keeping Finding as a type alias (not
// a distinct struct) means existing code that constructs Violation{}
// keeps working unchanged once the rename lands; the JSON wire
// shape is identical.
//
// Per the FASE 1.C PR1 spec, report/model.go declares both names —
// Violation for the Phase 0 contract, Finding as the Phase 1+ name.
// The snapshot test in runner_test.go only checks the JSON output,
// which uses "violations" as the array key (matching the existing
// Report field), so neither rename nor Finding can change the wire
// shape.
type Finding = Violation

// Severity is the canonical severity enum. Phase 0 scan functions
// emit "info" or "warn" as plain strings in the Violation.Severity
// field; the enum is forward-compatible — Phase 1+ code can use
// the constants below (SeverityInfo, SeverityWarn, SeverityError)
// and the underlying string values match what the wire format
// already expects. Existing Phase 0 code that does
// `Severity: "warn"` keeps working unchanged; the const is purely
// a typed-label convenience for new code.
//
// The enum is declared as a string (not int) so the JSON
// representation is human-readable ("info" / "warn" / "error")
// rather than numeric (0 / 1 / 2) — matching what downstream
// dashboards already parse.
type Severity string

const (
	// SeverityInfo is the lowest severity: hints and advisories
	// (e.g. kernel_split_hint). `--strict` does NOT promote
	// info-level violations to os.Exit(1) — they're always report-
	// only, even in Phase N.
	SeverityInfo Severity = "info"
	// SeverityWarn is the default severity for rule families that
	// the user-contract says should be report-only until the
	// operator promotes them via `--strict`. Most Phase 0 scan
	// functions emit this severity.
	SeverityWarn Severity = "warn"
	// SeverityError is reserved for Phase 1+ hard-gate rule
	// families. Phase 0 does not emit this severity; declaring
	// the const now lets PR4 (runner.go) reference it without a
	// churn cycle.
	SeverityError Severity = "error"
)

// Suggestion is a forward-looking struct for auto-fix hints.
// Phase 0 scan functions don't emit suggestions; the field is
// reserved for Phase 1+ when the runner will attach per-rule
// remediation text (e.g. "split this constructor into
// New<Small> + New<Large> with a config struct", or "move
// module_jobs_test.go references to module_media.go::BuildJobsBundle").
//
// The struct is intentionally small (3 string fields) so the JSON
// shape stays trivial and easy for downstream tooling to consume.
type Suggestion struct {
	// Rule is the canonical rule family id the suggestion
	// applies to (matches Violation.MatchedRule).
	Rule string
	// Message is the human-readable remediation text. May be
	// multi-line; the JSON encoder preserves newlines as \n
	// escape sequences.
	Message string
	// AutoFix is an optional machine-readable hint for tooling
	// that can apply the fix automatically (e.g. a sed
	// expression, a "goimports" invocation, a "gofmt -w"
	// command). Empty when no auto-fix is available.
	AutoFix string
}
