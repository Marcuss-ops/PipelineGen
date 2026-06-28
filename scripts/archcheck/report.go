// Package main — archcheck report types.
//
// report.go owns the JSON output contract for scripts/archcheck. The
// Report struct is the single source of truth for fields surfaced to
// dashboards and jq pipelines; check_*.go files (in the future
// checks/ subpackage) accumulate into a Report (via runner.go) and
// EncodeReport renders the final JSON.
//
// Keeping the struct + encoder in their own file (rather than main.go)
// lets the snapshot test in snapshot_test.go exercise the wire format
// without importing main() (which calls os.Exit) and gives a stable
// home for future shape additions (graphs, diff summaries, ...) that
// would otherwise balloon main.go.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Report is the JSON contract for scripts/archcheck consumers.
//
// The schema is intentionally minimal (`Checks map[string]int` +
// `Violations []string`) so dashboards and `jq` pipelines reading the
// report are stable. The Wave 19 PR2-1 edge-graph emission
// (`application_to_infrastructure_edges`, `cross_capability_import_edges`)
// is tracked separately under architecture/current.yaml and not yet
// wired in this struct — the field was prototyped and reverted; a
// follow-up PR will reintroduce it under `report.graphs` after the
// ratchet allowlist for those edges is committed.
type Report struct {
	Passed            bool           `json:"passed"`
	FocusedGatePassed bool           `json:"focused_gate_passed,omitempty"`
	Mode              string         `json:"mode"`
	Commit            string         `json:"commit"`
	LegacyBudget      int            `json:"legacy_budget,omitempty"`
	Checks            map[string]int `json:"checks"`
	Violations        []string       `json:"violations"`
}

// EncodeReport writes r to stdout as pretty-printed JSON and returns
// any encoding error. The caller is responsible for the post-encode
// exit-code policy (typically `os.Exit(1)` when `r.Passed == false`).
//
// The function is a thin wrapper around `json.Encoder` so the wire
// format (indent, trailing newline, key ordering) stays in one place.
// snapshot_test.go asserts the produced bytes match the schema in
// testdata/report_schema.json; any future change to the JSON shape
// MUST be reflected in the golden file in the same PR.
func EncodeReport(r *Report) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("archcheck: encode report: %w", err)
	}
	return nil
}
