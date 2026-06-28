package main

import (
	"encoding/json"
	"io"
	"os"
	"testing"
)

// TestReportSchema_Focused asserts that the focused-mode Report
// produced by runFocusedChecks() encodes to JSON whose top-level
// fields and types match the golden file in testdata/report_schema.json.
//
// The test does NOT compare field values (those are dynamic — counts
// change as the codebase evolves). It compares the WIRE SHAPE only:
// which keys exist and what JSON type each one has. The rationale is
// stability for downstream consumers (CI dashboards, jq pipelines,
// alerts): shape changes are breaking and must be intentional, while
// value drift is expected and is what the ratchet baseline is for.
func TestReportSchema_Focused(t *testing.T) {
	report := runFocusedChecks()
	if report.Mode != "focused" {
		t.Errorf("Mode = %q, want %q", report.Mode, "focused")
	}
	assertSchemaConformance(t, &report)
}

// TestReportSchema_Ratchet asserts the ratchet-mode Report
// (runRatchetChecks) matches the same golden schema. This is the
// harder gate (allowlist + baseline + database/sql regressions + ...
// — see architecture/current.yaml Wave 14-18 for the ratchet
// definition) and the test pins its wire format too.
func TestReportSchema_Ratchet(t *testing.T) {
	report := runRatchetChecks()
	if report.Mode != "ratchet" {
		t.Errorf("Mode = %q, want %q", report.Mode, "ratchet")
	}
	assertSchemaConformance(t, &report)
}

// TestEncodeReport_WireFormat exercises the EncodeReport helper
// directly on a hand-crafted Report and asserts the output is valid
// JSON containing every required field. This pins the encoder
// independent of the run*Checks functions — useful when refactoring
// check_*.go files in the future PRs of FASE 1.B.
func TestEncodeReport_WireFormat(t *testing.T) {
	r := &Report{
		Passed:     false,
		Mode:       "focused",
		Commit:     "test-fixture",
		Checks:     map[string]int{"stub_check": 0},
		Violations: []string{"stub violation"},
	}
	out := captureEncodeReport(t, r)
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("EncodeReport produced invalid JSON: %v\noutput: %s", err, out)
	}
	for _, key := range []string{"passed", "mode", "commit", "checks", "violations"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("encoded report missing required field %q", key)
		}
	}
	if decoded["mode"] != "focused" {
		t.Errorf("encoded mode = %v, want %q", decoded["mode"], "focused")
	}
}

// assertSchemaConformance loads testdata/report_schema.json and
// verifies every schema field is present in the encoded report with
// the right JSON type. Extra fields in the actual report are allowed
// (forward-compat) but missing required fields or type drift fails
// the test. Fields listed in the schema's `_optional` array may be
// absent from the encoded JSON (they carry `json:\"...,omitempty\"`)
// and the test skips the missing-key check for them.
func assertSchemaConformance(t *testing.T, r *Report) {
	t.Helper()
	out := captureEncodeReport(t, r)

	var actual map[string]any
	if err := json.Unmarshal(out, &actual); err != nil {
		t.Fatalf("EncodeReport produced invalid JSON: %v\noutput: %s", err, out)
	}

	schema, optional := loadSchema(t)
	for key, wantType := range schema {
		got, ok := actual[key]
		if !ok {
			if optional[key] {
				// omitempty field legitimately absent; skip.
				continue
			}
			t.Errorf("encoded report missing field %q (schema expects type %q)", key, wantType)
			continue
		}
		if !jsonTypeMatches(got, wantType) {
			t.Errorf("encoded report field %q has type %T, want %q (per testdata/report_schema.json)", key, got, wantType)
		}
	}
}

// captureEncodeReport runs EncodeReport(r) and returns the bytes
// written to stdout, restoring the original os.Stdout afterwards.
// Implemented with os.Pipe so the test exercises the same code path
// as the real binary (no shadowing of the encoder).
//
// Concurrency: the encode goroutine writes to `pw` (via os.Stdout);
// a second goroutine waits for the encode to finish, then closes
// `pw` so the main goroutine's io.ReadAll can drain and return.
// The two goroutines (encode + close-after-encode) are both required
// because:
//   - if we close `pw` first, the encoder may race against the
//     close and lose bytes (EPIPE / truncated pipe write).
//   - if we don't close `pw`, io.ReadAll blocks forever waiting
//     for EOF.
//
// The pattern is the standard "wait-then-close + concurrent drain"
// idiom for os.Pipe captures in Go tests.
func captureEncodeReport(t *testing.T, r *Report) []byte {
	t.Helper()
	oldStdout := os.Stdout
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = pw

	encodeDone := make(chan struct{})
	go func() {
		defer close(encodeDone)
		_ = EncodeReport(r)
	}()
	// Wait for the encode to complete, then close the writer so
	// io.ReadAll can return. Run in a separate goroutine so a
	// full pipe buffer (rare, but possible for large reports)
	// doesn't deadlock the encode against the close.
	go func() {
		<-encodeDone
		pw.Close()
	}()

	out, err := io.ReadAll(pr)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	pr.Close()
	os.Stdout = oldStdout
	return out
}

// loadSchema reads the golden file and decodes it. The schema is
// `field-name -> type-name` where type-name is one of
// `bool|string|object|array|int`. The leading `_doc` key is
// informational and stripped before returning to the caller. The
// `_optional` array (also stripped from the schema map) is returned
// as a second value so the caller can permit its keys to be absent
// from the encoded JSON.
func loadSchema(t *testing.T) (map[string]string, map[string]bool) {
	t.Helper()
	data, err := os.ReadFile("testdata/report_schema.json")
	if err != nil {
		t.Fatalf("read testdata/report_schema.json: %v (the golden file is required for snapshot tests)", err)
	}
	// Decode into a flexible shape: the schema is a JSON object with
	// string values, plus an `_optional` array of strings. Use
	// `map[string]json.RawMessage` so we can peek at `_optional`
	// without committing to a fixed shape for future additions.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse testdata/report_schema.json: %v", err)
	}
	schema := make(map[string]string, len(raw))
	for key, val := range raw {
		if key == "_doc" || key == "_optional" {
			continue
		}
		var typeName string
		if err := json.Unmarshal(val, &typeName); err != nil {
			t.Fatalf("parse testdata/report_schema.json: field %q has non-string value: %v", key, err)
		}
		schema[key] = typeName
	}
	var optionalList []string
	if rawOpt, ok := raw["_optional"]; ok {
		if err := json.Unmarshal(rawOpt, &optionalList); err != nil {
			t.Fatalf("parse testdata/report_schema.json: _optional must be a JSON array of strings: %v", err)
		}
	}
	optional := make(map[string]bool, len(optionalList))
	for _, k := range optionalList {
		optional[k] = true
	}
	return schema, optional
}

// jsonTypeMatches returns true when the runtime value v matches the
// schema type name. JSON numbers decode as float64 by default in
// encoding/json; we accept a value as `int` when its float form is
// an integer (no fractional component). The schema's `object` maps
// to a JSON object; `array` to a JSON array.
func jsonTypeMatches(v any, want string) bool {
	switch want {
	case "bool":
		_, ok := v.(bool)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "int":
		f, ok := v.(float64)
		return ok && f == float64(int(f))
	default:
		return false
	}
}
