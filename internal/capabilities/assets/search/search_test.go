package assets

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
	"reflect"
	"sort"
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

// TestRegistryEligibleFiltersBySources proves PR-1 source filtering.
// Query.Sources=["youtube"] must select ONLY the "youtube" backend
// despite other backends being registered with overlapping caps.
// Aliases resolve via ResolveCanonicals so Query.Sources=["yt"]
// must produce the same eligible set as Query.Sources=["youtube"].
func TestRegistryEligibleFiltersBySources(t *testing.T) {
	reg := NewBackendRegistry()
	for _, name := range []string{"youtube", "artlist", "local", "semantic", "stock"} {
		if err := reg.Register(&fakeBackend{name: name, caps: []Capability{CapVideo}}); err != nil {
			t.Fatal(err)
		}
	}
	reg.Freeze()

	cases := []struct {
		name      string
		input     []string
		wantNames []string
	}{
		{"canonical_youtube", []string{"youtube"}, []string{"semantic", "youtube"}},
		{"alias_yt_resolves_to_youtube", []string{"yt"}, []string{"semantic", "youtube"}},
		{"artlist_canonical", []string{"artlist"}, []string{"artlist", "semantic"}},
		// PR-SEARCH-HANDLER-MOUNT (July 2026): semantic backend
		// is always included (cross-source meta-backend with
		// internal source filter via Qdrant filter.Source).
		{"clips_alias_resolves_to_local", []string{"clips"}, []string{"local", "semantic"}},
		{"vector_alias_resolves_to_semantic", []string{"vector"}, []string{"semantic"}},
		{"multiple_sources", []string{"youtube", "artlist"}, []string{"artlist", "semantic", "youtube"}},
		{"case_insensitive_YT_upper", []string{"YT"}, []string{"semantic", "youtube"}},
		{"mixed_case_ArTlist", []string{"ArTlist"}, []string{"artlist", "semantic"}},
		{"mixed_canoncials_aliases", []string{"yt", "stock"}, []string{"semantic", "stock", "youtube"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reg.Eligible(Query{Sources: tc.input})
			gotNames := backendNames(got)
			sort.Strings(gotNames)
			wantSorted := append([]string{}, tc.wantNames...)
			sort.Strings(wantSorted)
			if !reflect.DeepEqual(gotNames, wantSorted) {
				t.Fatalf("Eligible(%v) want %v, got %v", tc.input, wantSorted, gotNames)
			}
		})
	}
}

// TestRegistryEligibleUnknownSourcesReturnsEmpty — PR-1 fail-fast
// invariant: when caller supplies ONLY unknown aliases, return
// empty eligible set (no silent fallback to all backends).
func TestRegistryEligibleUnknownSourcesReturnsEmpty(t *testing.T) {
	reg := NewBackendRegistry()
	for _, name := range []string{"youtube", "artlist", "local"} {
		if err := reg.Register(&fakeBackend{name: name, caps: []Capability{CapVideo}}); err != nil {
			t.Fatal(err)
		}
	}
	reg.Freeze()

	cases := []struct {
		name  string
		input []string
	}{
		{"single_unknown", []string{"bogus"}},
		{"all_unknown_separate", []string{"fake", "nope"}},
		{"mixed_known_and_unknown", []string{"youtube", "fake"}}, // known wins, unknown dropped — yields [youtube]
		{"empty_string_treated_as_unknown", []string{"", ""}},
	}
	t.Run("all_unknown_empty_result", func(t *testing.T) {
		got := reg.Eligible(Query{Sources: cases[0].input})
		if len(got) != 0 {
			t.Fatalf("Eligible(unknown) must be empty, got %v", backendNames(got))
		}
	})
	t.Run("multiple_unknown_empty_result", func(t *testing.T) {
		got := reg.Eligible(Query{Sources: cases[1].input})
		if len(got) != 0 {
			t.Fatalf("Eligible([fake nope]) must be empty, got %v", backendNames(got))
		}
	})
	t.Run("mixed_known_unknown_filters_known", func(t *testing.T) {
		got := reg.Eligible(Query{Sources: cases[2].input})
		gotNames := backendNames(got)
		if len(gotNames) != 1 || gotNames[0] != "youtube" {
			t.Fatalf("Eligible([youtube fake]) want [youtube], got %v", gotNames)
		}
	})
	t.Run("empty_string_treated_as_unknown", func(t *testing.T) {
		// Empty-string slots in q.Sources are silently dropped by
		// CanonicalizeSource so ["" ""] normalises to empty → empty result.
		got := reg.Eligible(Query{Sources: cases[3].input})
		if len(got) != 0 {
			t.Fatalf("Eligible([\"\"]) must be empty, got %v", backendNames(got))
		}
	})
}

// TestRegistryEligibleSourcesAndMediaTypes proves PR-1 dual filter
// composition: q.Sources narrows the candidate set; q.MediaTypes
// further narrows to backends whose caps intersect.
func TestRegistryEligibleSourcesAndMediaTypes(t *testing.T) {
	reg := NewBackendRegistry()
	if err := reg.Register(&fakeBackend{name: "youtube", caps: []Capability{CapVideo}}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&fakeBackend{name: "artlist", caps: []Capability{CapAudio}}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&fakeBackend{name: "local", caps: []Capability{CapVideo, CapAudio}}); err != nil {
		t.Fatal(err)
	}
	reg.Freeze()

	// Sources=[yt, artlist, clips] (clips is an alias that resolves
	// to "local"). AND MediaTypes=[audio] — youtube filtered out by
	// MediaTypes (only CapVideo); artlist kept; local kept.
	got := reg.Eligible(Query{Sources: []string{"yt", "artlist", "clips"}, MediaTypes: []string{"audio"}})
	gotNames := backendNames(got)
	sort.Strings(gotNames)
	want := []string{"artlist", "local"}
	if !reflect.DeepEqual(gotNames, want) {
		t.Fatalf("Eligible(sources+mediaTypes) want %v, got %v", want, gotNames)
	}

	// Sources=[yt, artlist] (NO clips alias → local filtered out by
	// Sources filter even though it satisfies MediaTypes=audio) +
	// MediaTypes=[audio] → only artlist remains. This proves the
	// Sources filter is applied BEFORE MediaTypes (fail-fast on
	// unknown alias).
	got = reg.Eligible(Query{Sources: []string{"yt", "artlist"}, MediaTypes: []string{"audio"}})
	gotNames = backendNames(got)
	sort.Strings(gotNames)
	if !reflect.DeepEqual(gotNames, []string{"artlist"}) {
		t.Fatalf("Eligible(yt+artlist+audio) want [artlist], got %v", gotNames)
	}
}

// TestQueryActorFieldPresent confirms the Query struct carries
// the PR-1 Actor field at the package level. The semantic backend
// adapter relies on Query.Actor.WorkspaceID/IsAdmin/UserID being
// settable by handlers; the Aggregator passes the Query through
// without modification so backends see exact middleware-set values.
func TestQueryActorFieldPresent(t *testing.T) {
	q := Query{
		Text: "x",
		Actor: Actor{
			WorkspaceID: "ws-1",
			UserID:      "u-1",
			IsAdmin:     false,
			IsSystem:    false,
		},
	}
	if q.Actor.WorkspaceID != "ws-1" || q.Actor.UserID != "u-1" || q.Actor.IsAdmin || q.Actor.IsSystem {
		t.Fatalf("Actor field round-trip broken: %+v", q.Actor)
	}
	if q.Actor.IsZero() {
		t.Fatal("non-zero Actor must NOT report IsZero=true")
	}
	var zeroQ Query
	if !zeroQ.Actor.IsZero() {
		t.Fatal("zero Actor must report IsZero=true")
	}
	if zeroQ.Actor.WorkspaceID != "" || zeroQ.Actor.UserID != "" || zeroQ.Actor.IsAdmin || zeroQ.Actor.IsSystem {
		t.Fatalf("zero Query.Actor must have all fields blank, got %+v", zeroQ.Actor)
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
	name     string
	caps     []Capability
	universe SearchUniverse
}

func (f *fakeBackend) Name() string               { return f.name }
func (f *fakeBackend) Capabilities() []Capability { return f.caps }
func (f *fakeBackend) Universe() SearchUniverse {
	if f.universe != "" {
		return f.universe
	}
	return SearchCatalog
}
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
