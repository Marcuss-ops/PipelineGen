package primitives

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestNewJobID_WrapsString verifies the constructor preserves the
// underlying string identity (the nominal wrapper is a pure view;
// no copy/mutation occurs).
func TestNewJobID_WrapsString(t *testing.T) {
	cases := []struct {
		in   string
		want JobID
	}{
		{"job-abc-123", JobID("job-abc-123")},
		{"", JobID("")},
		{"job-with-dashes_and_underscores.dots", JobID("job-with-dashes_and_underscores.dots")},
	}
	for _, tc := range cases {
		got := NewJobID(tc.in)
		if got != tc.want {
			t.Errorf("NewJobID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestJobID_IsEmpty exercises the boundary-friendly fail-closed hook.
// Both empty and zero-value constructs (no NewJobID at all) must
// report IsEmpty == true.
func TestJobID_IsEmpty(t *testing.T) {
	cases := []struct {
		in   JobID
		want bool
	}{
		{NewJobID(""), true},
		{NewJobID("job-1"), false},
		{JobID(""), true}, // zero value, never wrapped
		{JobID("job-zero-value"), false},
	}
	for _, tc := range cases {
		if got := tc.in.IsEmpty(); got != tc.want {
			t.Errorf("(%q).IsEmpty() = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestJobID_String_Roundtrips verifies the fmt contract (Stringer) is
// faithful — String() returns the underlying string with no decoration.
func TestJobID_String_Roundtrips(t *testing.T) {
	in := "job-roundtrip-1"
	id := NewJobID(in)
	if got := id.String(); got != in {
		t.Errorf("String() = %q, want %q", got, in)
	}
}

// TestJobID_JSONMarshalRoundtrip hardens the wire-identity invariant
// declared in doc.go: a nominal `type JobID string` MUST marshal to
// JSON as the bare string with zero overhead (no MarshalJSON method
// required). The round-trip + byte-identity assertion catches a
// future regression that accidentally adds a custom MarshalJSON.
func TestJobID_JSONMarshalRoundtrip(t *testing.T) {
	in := NewJobID("job-rt-1")
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal err: %v", err)
	}
	var out JobID
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("json.Unmarshal err: %v", err)
	}
	if out != in {
		t.Errorf("round-trip drift: in=%q out=%q json=%s", in, out, b)
	}
	// Byte-identity: typed JobID json MUST equal raw-string json.
	rawB, _ := json.Marshal("job-rt-1")
	if !bytes.Equal(b, rawB) {
		t.Errorf("typed JobID json %s should be byte-identical to raw string json %s", b, rawB)
	}
}
