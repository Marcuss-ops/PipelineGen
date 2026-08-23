package primitives

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestNewURL_WrapsString verifies the constructor preserves the
// underlying string identity. Note: lexical validation (scheme,
// host, well-formedness) is intentionally NOT performed here — it
// stays at the HTTP handler boundary where richer error mapping
// exists. The primitive is a pure view.
func TestNewURL_WrapsString(t *testing.T) {
	cases := []struct {
		in   string
		want URL
	}{
		{"https://example.com/path", URL("https://example.com/path")},
		{"", URL("")},
		{"http://localhost:8080", URL("http://localhost:8080")},
		{"not-a-url", URL("not-a-url")}, // primitive is pure: no validation
	}
	for _, tc := range cases {
		got := NewURL(tc.in)
		if got != tc.want {
			t.Errorf("NewURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestURL_IsEmpty exercises the boundary-friendly fail-closed hook.
// Used to short-circuit optional URLs (e.g. callback URLs that are
// not set).
func TestURL_IsEmpty(t *testing.T) {
	cases := []struct {
		in   URL
		want bool
	}{
		{NewURL(""), true},
		{NewURL("https://example.com"), false},
		{URL(""), true}, // zero value
		{URL("https://zero-value"), false},
	}
	for _, tc := range cases {
		if got := tc.in.IsEmpty(); got != tc.want {
			t.Errorf("(%q).IsEmpty() = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestURL_String_Roundtrips verifies the Stringer contract.
func TestURL_String_Roundtrips(t *testing.T) {
	in := "https://example.com/roundtrip?q=1"
	u := NewURL(in)
	if got := u.String(); got != in {
		t.Errorf("String() = %q, want %q", got, in)
	}
}

// TestURL_JSONMarshalRoundtrip hardens the wire-identity invariant
// declared in doc.go: a nominal `type URL string` MUST marshal to
// JSON as the bare string with zero overhead. Catches any future
// regression that adds MarshalJSON (breaking wire consumers).
func TestURL_JSONMarshalRoundtrip(t *testing.T) {
	cases := []URL{
		NewURL("https://example.com/path"),
		NewURL("http://localhost:8080"), // scheme variants
		NewURL("not-a-url"),             // primitive is pure: invalid still round-trips
	}
	for _, in := range cases {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("json.Marshal(%q) err: %v", in, err)
		}
		var out URL
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("json.Unmarshal(%s) err: %v", b, err)
		}
		if out != in {
			t.Errorf("round-trip drift: in=%q out=%q json=%s", in, out, b)
		}
		// Byte-identity: typed URL json MUST equal raw-string json.
		rawB, _ := json.Marshal(string(in))
		if !bytes.Equal(b, rawB) {
			t.Errorf("typed URL json %s should be byte-identical to raw string json %s (in=%q)", b, rawB, in)
		}
	}
}
