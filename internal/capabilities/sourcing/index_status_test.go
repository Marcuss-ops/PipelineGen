package sourcing

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSourcingIndexStatus_StableValues pins the wire string values
// against the canonical 4-state lifecycle. If anyone re-types a
// constant's wire string in production, this test fails immediately.
func TestSourcingIndexStatus_StableValues(t *testing.T) {
	cases := []struct {
		status SourcingIndexStatus
		want   string
	}{
		{SourcingIndexStatusPending, "pending"},
		{SourcingIndexStatusSkipped, "skipped"},
		{SourcingIndexStatusCompleted, "completed"},
		{SourcingIndexStatusFailed, "failed"},
	}
	for _, tc := range cases {
		if tc.status.String() != tc.want {
			t.Errorf("String() = %q, want %q", tc.status.String(), tc.want)
		}
		if !tc.status.IsValid() {
			t.Errorf("%q should be valid", tc.status)
		}
		if err := tc.status.Validate(); err != nil {
			t.Errorf("Validate(%q) returned %v, want nil", tc.status, err)
		}
	}
}

// TestSourcingIndexStatus_InvalidRejected verifies all non-canonical
// bytes return valid=false and Validate() returns a non-nil error.
// Includes the legacy "enqueued"/"not_configured" strings + empty +
// uppercase variants + close-miss spellings.
func TestSourcingIndexStatus_InvalidRejected(t *testing.T) {
	invalid := []SourcingIndexStatus{
		"",
		"enqueued",
		"not_configured",
		"unknown",
		"PENDING",
		"Completed",
		"completed ",
		"complete", // close miss
		"fail",
	}
	for _, s := range invalid {
		if s.IsValid() {
			t.Errorf("%q should be invalid", s)
		}
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%q) returned nil, want error", s)
		}
	}
}

// TestSourcingIndexStatus_JSONRoundTrip verifies Marshal+Unmarshal
// symmetry across all 4 canonical values. The wire shape is
// byte-identical: each round-trip emits the same bytes → parses back
// to the same typed enum.
func TestSourcingIndexStatus_JSONRoundTrip(t *testing.T) {
	type payload struct {
		IndexingStatus SourcingIndexStatus `json:"indexing_status"`
	}
	for _, status := range CanonicalSourcingIndexStatusValues {
		t.Run(status.String(), func(t *testing.T) {
			enc, err := json.Marshal(payload{IndexingStatus: status})
			if err != nil {
				t.Fatalf("marshal %q: %v", status, err)
			}
			var dec payload
			if err := json.Unmarshal(enc, &dec); err != nil {
				t.Fatalf("unmarshal %q: %v", status, err)
			}
			if dec.IndexingStatus != status {
				t.Fatalf("round trip = %q, want %q", dec.IndexingStatus, status)
			}
		})
	}
}

// TestSourcingIndexStatus_UnmarshalReject verifies UnmarshalJSON
// returns a non-nil error for legacy/unsupported bytes — no silent
// defaulting. Per PR-STATUS-ENFORCE-TYPED, the production wire
// boundary is fail-closed on bytes not in the canonical 4-state set.
func TestSourcingIndexStatus_UnmarshalReject(t *testing.T) {
	type payload struct {
		IndexingStatus SourcingIndexStatus `json:"indexing_status"`
	}
	for _, raw := range []string{
		`{"indexing_status":"enqueued"}`,
		`{"indexing_status":"not_configured"}`,
		`{"indexing_status":""}`,
		`{"indexing_status":"unknown"}`,
		`{"indexing_status":"COMPLETED"}`,
		`{"indexing_status":"complete "}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var dec payload
			if err := json.Unmarshal([]byte(raw), &dec); err == nil {
				t.Errorf("unmarshal %q returned nil error; want rejection", raw)
			}
		})
	}
}

// TestSourcingIndexStatus_Parse verifies the canonical bytes-to-enum
// helper. Validates Parse + rejects invalid bytes with non-nil error.
func TestSourcingIndexStatus_Parse(t *testing.T) {
	for _, status := range CanonicalSourcingIndexStatusValues {
		got, err := ParseSourcingIndexStatusFromBytes([]byte(status))
		if err != nil {
			t.Errorf("Parse(%q) returned error: %v", status, err)
		}
		if got != status {
			t.Errorf("Parse(%q) = %q, want %q", status, got, status)
		}
	}
	for _, raw := range []string{"enqueued", "unknown", "", "fail"} {
		_, err := ParseSourcingIndexStatusFromBytes([]byte(raw))
		if err == nil {
			t.Errorf("Parse(%q) returned nil error; want rejection", raw)
		}
	}
}

// TestSourcingIndexStatus_MarshalReject ensures Marshal of invalid
// bytes fails at the call site, not silently emits "" or default.
func TestSourcingIndexStatus_MarshalReject(t *testing.T) {
	type payload struct {
		S SourcingIndexStatus `json:"s"`
	}
	invalid := []SourcingIndexStatus{
		"",
		"enqueued",
		"not_configured",
		"PENDING",
	}
	for _, s := range invalid {
		p := payload{S: s}
		enc, err := json.Marshal(p)
		if err == nil {
			t.Errorf("Marshal(%q) succeeded with %s; want error", s, enc)
		}
	}
}

// TestSourcingIndexStatus_JSOBServedContent verifies the rendered JSON
// contains the exact wire string for each value (forward-compat for any
// future operator log scraper that reads the wire field directly).
func TestSourcingIndexStatus_JSOBServedContent(t *testing.T) {
	type payload struct {
		S SourcingIndexStatus `json:"s"`
	}
	cases := []struct {
		in   SourcingIndexStatus
		want string
	}{
		{SourcingIndexStatusPending, `"s":"pending"`},
		{SourcingIndexStatusSkipped, `"s":"skipped"`},
		{SourcingIndexStatusCompleted, `"s":"completed"`},
		{SourcingIndexStatusFailed, `"s":"failed"`},
	}
	for _, tc := range cases {
		enc, err := json.Marshal(payload{S: tc.in})
		if err != nil {
			t.Fatalf("marshal %q: %v", tc.in, err)
		}
		if !strings.Contains(string(enc), tc.want) {
			t.Errorf("JSON %q missing %q", string(enc), tc.want)
		}
	}
}

// TestSourcingIndexStatus_CanonicalEnumerationPinned verifies the
// CanonicalSourcingIndexStatusValues slice contains EXACTLY the 4
// canonical values in canonical order — drift here is a godlike/06
// SSOT violation.
func TestSourcingIndexStatus_CanonicalEnumerationPinned(t *testing.T) {
	want := []SourcingIndexStatus{
		SourcingIndexStatusPending,
		SourcingIndexStatusSkipped,
		SourcingIndexStatusCompleted,
		SourcingIndexStatusFailed,
	}
	if len(CanonicalSourcingIndexStatusValues) != len(want) {
		t.Fatalf("len(CanonicalSourcingIndexStatusValues) = %d, want %d",
			len(CanonicalSourcingIndexStatusValues), len(want))
	}
	for i, s := range want {
		if CanonicalSourcingIndexStatusValues[i] != s {
			t.Errorf("CanonicalSourcingIndexStatusValues[%d] = %q, want %q",
				i, CanonicalSourcingIndexStatusValues[i], s)
		}
	}
}

// Compile-time assertions — drift detection for any future rename of
// Marshal/Unmarshal/String methods. Verifies that SourcingIndexStatus
// satisfies the canonical Go stringer + json Marshaler + Unmarshaler
// interfaces at compile time.
var (
	_ json.Marshaler   = SourcingIndexStatus("")
	_ json.Unmarshaler = (*SourcingIndexStatus)(nil)
)
