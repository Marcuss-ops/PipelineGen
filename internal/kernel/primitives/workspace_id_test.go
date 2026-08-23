package primitives

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestNewWorkspaceID_WrapsString verifies the constructor preserves
// the underlying string identity and accepts the reserved "default"
// sentinel without error (the middleware uses IsEmpty || "default"
// as the strict predicate — see middleware_workspace_scope.go).
func TestNewWorkspaceID_WrapsString(t *testing.T) {
	cases := []struct {
		in   string
		want WorkspaceID
	}{
		{"ws-prod-1", WorkspaceID("ws-prod-1")},
		{"", WorkspaceID("")},
		{"default", WorkspaceID("default")}, // reserved sentinel
		{"ws-with.dots-and-dashes_underscores", WorkspaceID("ws-with.dots-and-dashes_underscores")},
	}
	for _, tc := range cases {
		got := NewWorkspaceID(tc.in)
		if got != tc.want {
			t.Errorf("NewWorkspaceID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestWorkspaceID_IsEmpty exercises the boundary-friendly fail-closed
// hook. Note: "default" must report IsEmpty == false because
// distinguishing the reserved sentinel from unset is part of the
// middleware contract — see TestWorkspaceID_DefaultIsNotEmpty below.
func TestWorkspaceID_IsEmpty(t *testing.T) {
	cases := []struct {
		in   WorkspaceID
		want bool
	}{
		{NewWorkspaceID(""), true},
		{NewWorkspaceID("ws-1"), false},
		{WorkspaceID(""), true}, // zero value
		{WorkspaceID("ws-zero"), false},
	}
	for _, tc := range cases {
		if got := tc.in.IsEmpty(); got != tc.want {
			t.Errorf("(%q).IsEmpty() = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestWorkspaceID_DefaultIsNotEmpty pins the reserved-sentinel
// contract: "default" is treated as a valid (non-empty) workspace
// from the primitive's standpoint. The middleware bundles the
// strict predicate (IsEmpty || == "default") at the boundary, where
// the HTTP context can choose to route it to the singleton unscoped
// workspace or fail with 400.
func TestWorkspaceID_DefaultIsNotEmpty(t *testing.T) {
	d := NewWorkspaceID("default")
	if d.IsEmpty() {
		t.Errorf("WorkspaceID(\"default\").IsEmpty() = true, want false (default is a reserved sentinel, not the zero value)")
	}
}

// TestWorkspaceID_String_Roundtrips verifies the Stringer contract.
func TestWorkspaceID_String_Roundtrips(t *testing.T) {
	in := "ws-roundtrip-1"
	id := NewWorkspaceID(in)
	if got := id.String(); got != in {
		t.Errorf("String() = %q, want %q", got, in)
	}
}

// TestWorkspaceID_JSONMarshalRoundtrip hardens the wire-identity
// invariant declared in doc.go: a nominal `type WorkspaceID string`
// MUST marshal to JSON as the bare string with zero overhead. The
// reserved "default" sentinel must round-trip cleanly because the
// middleware relies on it to be a JSON-string value at the wire
// (clients send "default" verbatim from the multi-tenancy header
// contract).
func TestWorkspaceID_JSONMarshalRoundtrip(t *testing.T) {
	cases := []WorkspaceID{
		NewWorkspaceID("ws-rt-1"),
		NewWorkspaceID("default"),
		NewWorkspaceID(""), // zero-value edge case
	}
	for _, in := range cases {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("json.Marshal(%q) err: %v", in, err)
		}
		var out WorkspaceID
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("json.Unmarshal(%s) err: %v", b, err)
		}
		if out != in {
			t.Errorf("round-trip drift: in=%q out=%q json=%s", in, out, b)
		}
		// Byte-identity: typed WorkspaceID json MUST equal raw-string json.
		rawB, _ := json.Marshal(string(in))
		if !bytes.Equal(b, rawB) {
			t.Errorf("typed WorkspaceID json %s should be byte-identical to raw string json %s (in=%q)", b, rawB, in)
		}
	}
}
