// Package operational — voiceover_report.go
//
// Report writer + assertion recorder of the voiceover smoke harness:
// the report types (VoiceoverReport / AssertionRecord / DBProbeRecord)
// and the Assert*/RecordJobID/WriteReport methods on VoiceoverHarness.
// Extracted from voiceover_harness.go on 2026-08-07 to satisfy the
// archcheck-strict 600-line cap
// (architecture/policy.yaml#max_lines_per_file_strict).
package operational

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// VoiceoverReport is the JSON-serialised forensic artefact for one smoke run.
//
// Shape is intentionally self-contained: the report + the bash smoke log
// together provide full offline-forensic coverage without re-running the
// test. JobIDs is keyed by role (e.g. "parent", "child_it_it") so a
// dashboard can correlate the canonical parent/child pair across runs.
type VoiceoverReport struct {
	StartedAt  string                   `json:"started_at"`
	FinishedAt string                   `json:"finished_at"`
	FASE       string                   `json:"fase"`
	Outcome    string                   `json:"outcome"` // "pass" | "fail" | "skip"
	JobIDs     map[string]string        `json:"job_ids"`
	Assertions []AssertionRecord        `json:"assertions"`
	DBProbes   map[string]DBProbeRecord `json:"db_probes"`
}

// AssertionRecord captures one Assert() call's expected/actual/outcome.
type AssertionRecord struct {
	Label    string `json:"label"`
	Status   string `json:"status"` // "pass" | "fail" | "skip"
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Note     string `json:"note,omitempty"`
}

// DBProbeRecord captures one Probe* call's query + row count + first row.
type DBProbeRecord struct {
	Query  string `json:"query"`
	Rows   int    `json:"rows"`
	Sample string `json:"sample,omitempty"`
}

// Sentinel parsed by WriteReport and surfaced as VoiceoverReport.Outcome
// when at least one DB probe returned ErrSqliteBinaryMissing. Operators
// can grep the report for "probe_unavailable" to identify which FASE
// had the missing binary without re-running.

// ── Report writer + assertion recorder ──────────────────────────────────

// Assert records an assertion outcome into the report and fails the
// test if expected != actual. The first failed Assert in a test
// short-circuits the rest of the test (t.Fatal semantics).
func (h *VoiceoverHarness) Assert(label, expected, actual string) {
	h.t.Helper()
	status := "pass"
	if expected != actual {
		status = "fail"
	}

	h.reportMu.Lock()
	h.report.Assertions = append(h.report.Assertions, AssertionRecord{
		Label:    label,
		Status:   status,
		Expected: expected,
		Actual:   actual,
	})
	h.reportMu.Unlock()

	if status == "fail" {
		h.t.Fatalf("voiceover harness: %s — expected [%s], got [%s]", label, expected, actual)
	}
}

// Assertf is the printf-style variant of Assert for verbose expected/actual
// pairs (e.g. JSON body snippets).
func (h *VoiceoverHarness) Assertf(label, expected, actualFormat string, actualArgs ...any) {
	h.Assert(label, expected, fmt.Sprintf(actualFormat, actualArgs...))
}

// AssertHTTPStatus is a convenience wrapper for `Assert(label, "<expected>",
// strconv.Itoa(code))`. Returns the code unchanged so callers can chain.
func (h *VoiceoverHarness) AssertHTTPStatus(label string, expectedStatuses []string, code int) int {
	h.t.Helper()
	actual := fmt.Sprintf("%d", code)
	expected := strings.Join(expectedStatuses, "|")
	h.Assert(label, expected, actual)
	return code
}

// RecordJobID stores a jobID under a role key (e.g. "parent", "child_it_it")
// in the report. Idempotent: re-recording under the same key OVERWRITES
// (the latest value wins, which matches the natural "this is the final
// value after retry" semantics).
func (h *VoiceoverHarness) RecordJobID(role, jobID string) {
	h.reportMu.Lock()
	defer h.reportMu.Unlock()
	h.report.JobIDs[role] = jobID
}

// WriteReport finalises the report (stamps FinishedAt + Outcome) and
// JSON-marshals it to disk. Always returns nil error from a deferred
// call so a failed test still produces a report for forensics.
func (h *VoiceoverHarness) WriteReport() error {
	h.reportMu.Lock()
	defer h.reportMu.Unlock()

	h.report.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	h.report.Outcome = "pass"
	for _, a := range h.report.Assertions {
		if a.Status == "fail" {
			h.report.Outcome = "fail"
			break
		}
	}

	data, err := json.MarshalIndent(h.report, "", "  ")
	if err != nil {
		return fmt.Errorf("voiceover harness: marshal report: %w", err)
	}

	dir := filepath.Dir(h.reportPath)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(h.reportPath, data, 0o600); err != nil {
		return fmt.Errorf("voiceover harness: write report %s: %w", h.reportPath, err)
	}
	return nil
}

// recordDBProbe is the internal Append hook used by the 4 Probe*
// methods (voiceover_probes.go). Locked by reportMu.
func (h *VoiceoverHarness) recordDBProbe(label, query string, rows []string) {
	h.reportMu.Lock()
	defer h.reportMu.Unlock()
	sample := ""
	if len(rows) > 0 {
		sample = rows[0]
		if len(sample) > 200 {
			sample = sample[:200] + "..."
		}
	}
	h.report.DBProbes[label] = DBProbeRecord{
		Query:  query,
		Rows:   len(rows),
		Sample: sample,
	}
}
