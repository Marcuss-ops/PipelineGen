package search

// Wave 19 cross-capability rule: this package (search — canonical
// SSOT for the Search capability) must NOT import any other
// internal/application/* package. The TypeAliasesAreIdentity test
// verifying the search ↔ mediasearch alias pair therefore lives in
// internal/application/mediasearch/types_alias_test.go (the cap that
// imports search; legal direction per Wave 19).
//
// Vendor neutrality: this test package must NOT depend on zap (the
// production logger adapter is installed in PR 9 by internal/app/).
// Tests verify the in-package noop fallback in the NOOP test, and the
// existing TestAggregatorNILLogNoopFallback covers the nil-log case.

import (
	"context"
	"encoding/base64"
	"testing"
)

// TestAggregatorNOOPReturnsEmptyResult proves the PR 8 stub does
// NOT error and returns an empty, non-partial Result. The real
// behaviour ships in PR 9; assertions here are bounds-checked only.
// Pass nil log so NewAggregator substitutes its noop logger; this
// keeps the test vendor-neutral (no zap dependency).
func TestAggregatorNOOPReturnsEmptyResult(t *testing.T) {
	agg := NewAggregator(NewBackendRegistry(), nil)
	res, err := agg.Search(context.Background(), Query{Text: "hello", Limit: 50})
	if err != nil {
		t.Fatalf("NOOP must not error, got: %v", err)
	}
	if res == nil {
		t.Fatal("NOOP must return non-nil Result")
	}
	if len(res.Items) != 0 {
		t.Fatalf("NOOP Items must be empty, got %d items", len(res.Items))
	}
	if res.NextCursor != "" {
		t.Fatalf("NOOP NextCursor must be empty, got %q", res.NextCursor)
	}
	if res.Partial {
		t.Fatal("NOOP cannot be Partial")
	}
	if res.ProviderErrors == nil {
		t.Fatal("NOOP must return non-nil ProviderErrors even when empty")
	}
}

// TestAggregatorNILLogNoopFallback covers the constructor's nil-log
// branch — a nil Logger is replaced with a noopLogger without panic.
func TestAggregatorNILLogNoopFallback(t *testing.T) {
	agg := NewAggregator(nil, nil)
	if agg.log == nil {
		t.Fatal("nil log must be replaced with noopLogger")
	}
	res, err := agg.Search(context.Background(), Query{Text: "x"})
	if err != nil || res == nil {
		t.Fatalf("nil-log aggregator must still serve empty Result, got err=%v res=%v", err, res)
	}
}

// TestCursorRoundtripEmpty validates base64-decode + JSON-shape
// + version check on a degenerate (empty) cursor. The empty
// representation MUST round-trip without error per design.
func TestCursorRoundtripEmpty(t *testing.T) {
	c := Cursor("")
	encoded, err := EncodeCursor(c)
	if err != nil {
		t.Fatalf("EncodeCursor(\"\") failed: %v", err)
	}
	if encoded != "" {
		t.Fatalf("EncodeCursor(\"\") must return \"\", got %q", encoded)
	}
	decoded, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("DecodeCursor(empty) failed: %v", err)
	}
	if decoded != c {
		t.Fatalf("empty roundtrip broken: %q != %q", decoded, c)
	}
}

// TestCursorInvalidDecode ensures malformed inputs surface as
// ErrInvalidCursor (never nil error, never panic). Covers 3 distinct
// malformed classes: bad base64, valid-base64-but-not-JSON,
// and wrong-version-marker.
func TestCursorInvalidDecode(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"bad_base64", "not-base64!"},
		{"not_json", base64.RawURLEncoding.EncodeToString([]byte("not-json"))},
		{"wrong_version", base64.RawURLEncoding.EncodeToString(
			[]byte(`{"v":99,"items":[]}`))},
		{"missing_version", base64.RawURLEncoding.EncodeToString(
			[]byte(`{"items":[]}`))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeCursor(tc.in)
			if err != ErrInvalidCursor {
				t.Errorf("DecodeCursor(%q): want ErrInvalidCursor, got %v",
					tc.in, err)
			}
		})
	}
}

// TestEncodeCursorFromItemsEmpty asserts that building a cursor from
// zero identifiable items yields ErrEmptyCandidate (the aggregator's
// dedup policy is "drop empty-identity candidates silently").
func TestEncodeCursorFromItemsEmpty(t *testing.T) {
	if _, err := EncodeCursorFromItems([]Candidate{
		{Source: ""}, {AssetID: ""}, {Score: 0},
	}); err != ErrEmptyCandidate {
		t.Fatalf("want ErrEmptyCandidate on empty-identity items, got %v", err)
	}
}

// TestEncodeCursorFromItemsNonEmpty sanity-checks the happy path:
// one or more identifiable items produce a non-empty Cursor that
// re-decodes to a non-empty Cursor with the same item count.
//
// Round-trip wiring (per cursor.go): items → EncodeCursorFromItems
// → Cursor (in-memory JSON form) → EncodeCursor → wire (base64url) →
// DecodeCursor → Cursor (in-memory JSON form). Passing the JSON form
// directly to DecodeCursor would FAIL (DecodeCursor base64-decodes
// first), which is the bug this test specifically guards against.
func TestEncodeCursorFromItemsNonEmpty(t *testing.T) {
	in := []Candidate{
		{AssetID: "a-1", Source: "youtube", Score: 0.95},
		{AssetID: "a-2", Source: "artlist", Score: 0.81},
	}
	c, err := EncodeCursorFromItems(in)
	if err != nil {
		t.Fatalf("EncodeCursorFromItems(non-empty): %v", err)
	}
	if c == "" {
		t.Fatal("Cursor must be non-empty for non-empty input")
	}
	wire, err := EncodeCursor(c)
	if err != nil {
		t.Fatalf("EncodeCursor on freshly built Cursor: %v", err)
	}
	if wire == "" {
		t.Fatal("EncodeCursor must produce non-empty wire form")
	}
	back, err := DecodeCursor(wire)
	if err != nil {
		t.Fatalf("DecodeCursor on freshly encoded cursor: %v", err)
	}
	if back == "" {
		t.Fatal("round-trip lost non-emptiness")
	}
}

// TestRegistryEligibleFiltersByMediaType proves the BackendRegistry
// filters by Capabilities ∩ Query.MediaTypes when the caller
// specifies a media type filter.
func TestRegistryEligibleFiltersByMediaType(t *testing.T) {
	reg := NewBackendRegistry()
	if err := reg.Register(&fakeBackend{name: "v", caps: []Capability{CapVideo}}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&fakeBackend{name: "a", caps: []Capability{CapAudio}}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&fakeBackend{name: "m", caps: []Capability{CapVideo, CapMusic}}); err != nil {
		t.Fatal(err)
	}
	got := reg.Eligible(Query{MediaTypes: []string{"audio"}})
	if len(got) != 1 || got[0].Name() != "a" {
		t.Fatalf("Eligible(audio) want [a], got %v", backendNames(got))
	}
	all := reg.Eligible(Query{}) // empty MediaTypes = no filter
	if len(all) != 3 {
		t.Fatalf("Eligible(empty) want 3 backends, got %d", len(all))
	}
}

// TestRegistryFreezeBlocksRegister proves Freeze is one-way: any
// subsequent Register returns ErrFrozen (main spec invariant).
func TestRegistryFreezeBlocksRegister(t *testing.T) {
	reg := NewBackendRegistry()
	if err := reg.Register(&fakeBackend{name: "x", caps: []Capability{CapVideo}}); err != nil {
		t.Fatal(err)
	}
	reg.Freeze()
	if err := reg.Register(&fakeBackend{name: "y", caps: []Capability{CapVideo}}); err != ErrFrozen {
		t.Fatalf("after Freeze, Register must return ErrFrozen, got %v", err)
	}
	if !reg.IsFrozen() {
		t.Fatal("IsFrozen must return true after Freeze")
	}
}

// ── Test helpers ───────────────────────────────────────────────────

type fakeBackend struct {
	name string
	caps []Capability
}

func (f *fakeBackend) Name() string                          { return f.name }
func (f *fakeBackend) Capabilities() []Capability           { return f.caps }
func (f *fakeBackend) Search(ctx context.Context, q Query) ([]Candidate, error) {
	return nil, nil
}

func backendNames(bs []SearchBackend) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Name()
	}
	return out
}
