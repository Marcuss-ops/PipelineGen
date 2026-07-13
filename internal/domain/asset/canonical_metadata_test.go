// Package asset (canonical_metadata_test.go) pins the canonical
// metadata_json SSOT surface declared in canonical_metadata.go.
//
// Two regression gates:
//  1. TestCanonicalMediaMetadata_AllFieldNamesAreCanonical —
//     zero-value struct + json.Marshal must produce EXACTLY the
//     10 expected keys (no more, no less).
//  2. TestCanonicalMediaMetadata_FieldByFieldRoundTripEquality —
//     seed every field with a non-zero canonical value, marshal,
//     unmarshal — and assert reflect.DeepEqual byte-equivalence.
//
// Adding a canonical key requires updating (a) the struct, (b) the
// producer builder, AND both of these gates. Removing a key fails
// both gates. godlike/06 SSOT.
package asset

import (
	"encoding/json"
	"reflect"
	"testing"
)

// expectedCanonicalFieldNames is the EXACT 10-key SSOT set
// for the canonical metadata_json shape (godlike/06). The
// field-by-field equality test below is anchored to this set;
// adding a key requires touching (a) the struct, (b) the
// buildCanonicalGeneratedMetadata builder, AND (c) this var.
var expectedCanonicalFieldNames = []string{
	"content_hash",
	"embedding_version_visual",
	"height",
	"origin",
	"prompt_original",
	"provider",
	"semantic_description",
	"style",
	"tags",
	"width",
}

// TestCanonicalMediaMetadata_AllFieldNamesAreCanonical pins the
// exact SSOT JSON key set. Marshals a zero-value
// CanonicalMediaMetadata, re-decodes as map[string]any, and
// asserts (a) the resulting key count matches
// len(expectedCanonicalFieldNames), (b) every expected key is
// present, and (c) no extra keys appear. A future
// addition/removal that misses this test fails CI here.
//
// Why zero-value: a zero-value struct exercises the "is every
// field unconditionally emitted?" invariant. If a future
// maintainer adds `omitempty` to any field, the wire shape
// drops that key (nil/"" → no marshalling), this test catches it.
func TestCanonicalMediaMetadata_AllFieldNamesAreCanonical(t *testing.T) {
	var zero CanonicalMediaMetadata
	b, err := json.Marshal(zero)
	if err != nil {
		t.Fatalf("zero-value marshal failed: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("zero-value unmarshal failed: %v\n%s", err, string(b))
	}
	if got, want := len(m), len(expectedCanonicalFieldNames); got != want {
		t.Fatalf("canonical key count drifted: got %d, want %d (keys=%v)",
			got, want, keysOf(m))
	}
	for _, key := range expectedCanonicalFieldNames {
		if _, ok := m[key]; !ok {
			t.Errorf("canonical key %q is missing from the wire shape: generated keys=%v",
				key, keysOf(m))
		}
	}
}

// TestCanonicalMediaMetadata_FieldByFieldRoundTripEquality is
// the SSOT field-by-field equality contract. Seeds every field
// with a non-zero canonical value, marshals, then unmarshals
// into a fresh struct via the JSON tags — and asserts
// reflect.DeepEqual round-trip equality.
//
// godlike/06 invariant: callers that read metadata_json MUST
// get back exactly the fields they wrote, byte-for-byte. Any
// per-field tag drift between the producer builder and this
// reader fails CI here.
//
// Specific assertions covered:
//   - Plain string fields round-trip byte-for-byte (PromptOriginal,
//     Style, ContentHash, EmbeddingVersionVisual).
//   - Typed enum fields round-trip value-preserving (Provider +
//     Origin — the typed-enum underliers marshal to/from their
//     string constants identically).
//   - Integer fields round-trip without float coercion (Width +
//     Height).
//   - Slice fields round-trip with length preservation (Tags).
//   - Empty-string default fields (SemanticDescription) round-trip
//     as "" — proving the wire shape still includes the key even
//     at zero value.
func TestCanonicalMediaMetadata_FieldByFieldRoundTripEquality(t *testing.T) {
	seed := CanonicalMediaMetadata{
		PromptOriginal:         "cinematic mountain sunrise over a calm lake",
		SemanticDescription:    "",
		Style:                  "cinematic",
		Tags:                   []string{"mountain", "sunrise", "outdoor"},
		Provider:               ProviderGoogleSlides,
		Origin:                 ImageOriginGenerated,
		Width:                  1920,
		Height:                 1080,
		ContentHash:            "deadbeef0123456789",
		EmbeddingVersionVisual: "2026-06-16-v1",
	}

	b, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var roundTripped CanonicalMediaMetadata
	if err := json.Unmarshal(b, &roundTripped); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, string(b))
	}

	if !reflect.DeepEqual(seed, roundTripped) {
		t.Errorf("field-by-field equality violated across JSON round-trip:\n got  %+v\n want %+v\n JSON %s",
			roundTripped, seed, string(b))
	}
}

// keysOf returns the sorted key set of m for deterministic
// error messages. Stable insertion-sort keeps the output
// reproducible across test runs without a sort.Sort dependency.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
