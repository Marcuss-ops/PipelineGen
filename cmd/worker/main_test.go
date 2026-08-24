// Tests for cmd/worker::workerruntime.ParseAndValidateCaps — Phase 3 of the W1 specification.
//
// These tests pin the contract between the VELOX_WORKER_CAPABILITIES env var
// and the registered RemoteWorker handler set. Together with
// internal/capabilities/jobs/worker/registry_test.go they cover the W1 exit gate:
// the parse must REFUSE rather than silently emit empty/unknown capabilities.
//
// We use `package main` so the tests can touch workerruntime.ParseAndValidateCaps directly
// (no public re-export needed for an internal CLI helper).
package main

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/app/workerruntime"
)

// ── Capability derivation contract ───────────────────────────────────────

func TestParseAndValidateCaps_EmptyEnvReturnsRegisteredSet(t *testing.T) {
	// Creator Blocco 1.2: empty env now fails closed — operators must
	// set VELOX_WORKER_PROFILE or VELOX_WORKER_CAPABILITIES explicitly.
	registered := []string{"a", "b", "c"}
	_, err := workerruntime.ParseAndValidateCaps("", registered)
	if err == nil {
		t.Fatal("expected error for empty VELOX_WORKER_CAPABILITIES (Creator Blocco 1.2 fail-closed)")
	}
	if !strings.Contains(err.Error(), "refusing to start") {
		t.Fatalf("expected 'refusing to start' in error, got: %v", err)
	}
}

func TestParseAndValidateCaps_ValidSubset(t *testing.T) {
	registered := []string{"a", "b", "c"}
	caps, err := workerruntime.ParseAndValidateCaps(`{"job_types":["a","c"]}`, registered)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalUnordered(caps.JobTypes, []string{"a", "c"}) {
		t.Fatalf("want [a c], got %v", caps.JobTypes)
	}
}

func TestParseAndValidateCaps_UnknownTypeRejected(t *testing.T) {
	registered := []string{"a", "b"}
	_, err := workerruntime.ParseAndValidateCaps(`{"job_types":["a","z"]}`, registered)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	if !strings.Contains(err.Error(), "z") {
		t.Fatalf("error should mention offending type 'z', got: %v", err)
	}
}

func TestParseAndValidateCaps_MalformedJSONRejected(t *testing.T) {
	registered := []string{"a"}
	_, err := workerruntime.ParseAndValidateCaps(`{not valid json`, registered)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("error should mention JSON, got: %v", err)
	}
}

func TestParseAndValidateCaps_EmptyArrayRejected(t *testing.T) {
	registered := []string{"a"}
	if _, err := workerruntime.ParseAndValidateCaps(`{"job_types":[]}`, registered); err == nil {
		t.Fatal("expected error for empty job_types array (must fail closed)")
	}
}

func TestParseAndValidateCaps_DuplicateValuesNormalized(t *testing.T) {
	registered := []string{"a", "b"}
	caps, err := workerruntime.ParseAndValidateCaps(`{"job_types":["a","a","b","b","a"]}`, registered)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(caps.JobTypes) != 2 {
		t.Fatalf("expected 2 unique types after dedup, got %v", caps.JobTypes)
	}
}

func TestParseAndValidateCaps_WhitespaceEntriesNormalized(t *testing.T) {
	registered := []string{"a"}
	caps, err := workerruntime.ParseAndValidateCaps(`{"job_types":["  a  "],"junk":""}`, registered)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalUnordered(caps.JobTypes, []string{"a"}) {
		t.Fatalf("want [a], got %v", caps.JobTypes)
	}
}

func TestParseAndValidateCaps_OnlyWhitespaceEmptySetRejected(t *testing.T) {
	// Spec: "final set sorted and non-empty" — when dedup drops every entry
	// (all whitespace/empty), the resolved set is empty and must fail closed.
	registered := []string{"a"}
	_, err := workerruntime.ParseAndValidateCaps(`{"job_types":["   ", "  ", "\t"]}`, registered)
	if err == nil {
		t.Fatal("expected error when dedup drops all entries (empty resolved set)")
	}
}

func TestParseAndValidateCaps_FinalSetIsSorted(t *testing.T) {
	// Spec: "final set sorted and non-empty". The implementation must
	// canonicalize order before returning so a regression where the parser
	// stops calling sort.Strings surfaces here as a deterministic-shape
	// assertion failure rather than a silent log-formatting drift.
	registered := []string{"a", "b", "c", "d", "e"}
	caps, err := workerruntime.ParseAndValidateCaps(`{"job_types":["e","c","a","d","b"]}`, registered)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a", "b", "c", "d", "e"}
	if !equalStrings(caps.JobTypes, want) {
		t.Fatalf("sorted: want %v, got %v", want, caps.JobTypes)
	}
}

// ── Doctor subcommand dispatch contract ─────────────────────────────────

func TestIsDoctorSubcommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{name: "positional doctor with flags", args: []string{"doctor", "--json"}, want: true},
		{name: "bare positional doctor", args: []string{"doctor"}, want: true},
		{name: "documented --mode=doctor form", args: []string{"--mode=doctor", "--json"}, want: true},
		{name: "config flag is not a subcommand", args: []string{"--config", "config.yaml"}, want: false},
		{name: "no args", args: nil, want: false},
		{name: "doctor as second arg is not a subcommand", args: []string{"--json", "doctor"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDoctorSubcommand(tc.args); got != tc.want {
				t.Fatalf("isDoctorSubcommand(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[string]int{}
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
		if counts[s] < 0 {
			return false
		}
	}
	return true
}
