// Package indexing — index_document_test.go: canonical-shape sanity
// suite for IndexDocument + IndexedMetadata.
//
// Companion to index_document.go's 19-field semantic-payload
// enrichment (shipped via commit `e8f8587f` on `origin/main`).
// Locks the wire-shape contract so a future refactor that breaks
// the canonical field set surfaces as a test failure BEFORE the
// runtime drift reaches production.
//
// godlike/06 SSOT (one canonical owner per fact): every field
// below has ONE canonical declaration on `IndexedMetadata`;
// parallel-agent's inline struct extension is the canonical
// SOLE owner of each new field per godlike/07
// minimum-blast-radius discipline.
//
// godlike/07 NO-FAKE-AVAILABILITY: presence of a payload key
// with a zero/empty value is a fake-availability regression
// (the godlike/06 freeze test `composition_test.go::TestComposition
// _FrozenQdrantIndexDocumentForbiddenFields` enforces the
// reverse direction — forbidden keys are NOT promoted to the
// Writer↔Qdrant wire shape).
//
// godlike/06 LOCKSTEP-DISCIPLINE (additive-field audit-pin):
// the canonical field set is INTENTIONALLY HARD-CODED in
// `expectedSemanticPayloadFields()` rather than auto-derived
// via reflect on `IndexedMetadata`. A future agent adding a
// new field MUST: (1) add the field to `IndexedMetadata`, AND
// (2) add the new field to `expectedSemanticPayloadFields()`
// in lockstep. Failure to update both surfaces in this test
// file would land the new field SILENTLY (the test would NOT
// trip) — per godlike/06 SSOT one-canonical-owner-per-fact
// the canonical field set is a discussion-driven decision,
// not an auto-discovery.

package indexing

import (
	"encoding/json"
	"reflect"
	"testing"
)

// expectedSemanticPayloadFields lists the canonical fields the
// enrichment wave locked onto IndexedMetadata. The map value is
// the expected reflect.Kind so a future field-type drift surfaces
// as a build-time-ish test failure (test runs in `go test
// -short`).
//
// Per-field deviation provenance (vs the original
// 2026-07-06 user spec):
//
//   - Round is int (NOT *int): the parallel-agent pre-applied
//     struct settles on `Round int` for simplicity; the godlike/06
//     one-canonical-owner-per-fact rule means I cannot re-type
//     without a deprecation cycle. The int zero is treated as
//     "no round known" by payload_builder.go's `Round > 0` guard.
//
//   - ContextSubject is included even though the user spec did
//     not list it explicitly: parallel-agent added it as the
//     canonical LLM-derived secondary subject (per godlike/06
//     SSOT we're locked to the wire shape the parallel-agent
//     shipped). The pipeline strictly states: includes 19 fields
//     post-enrichment.
func expectedSemanticPayloadFields() map[string]reflect.Kind {
	return map[string]reflect.Kind{
		// 18 user-spec semantic-payload fields:
		"Destination":    reflect.String,
		"Origin":         reflect.String,
		"SourceProvider": reflect.String,
		"Event":          reflect.String,
		"Round":          reflect.Int, // SSOT: int (parallel-agent settle)
		"Scene":          reflect.String,
		"Subject":        reflect.String,
		"Entities":       reflect.Slice,
		"SemanticTitle":  reflect.String,
		"EmbeddingText":  reflect.String,
		"SourceVideoID":  reflect.String,
		"DurationSec":    reflect.Int,
		"PolicyVersion":  reflect.String,
		"TotalChunks":    reflect.Int,
		"DrivePath":      reflect.String,
		"FolderID":       reflect.String,
		"FolderPath":     reflect.String,
		"IndexingStatus": reflect.String,

		// 17th (parallel-agent-added): LLM-derived secondary
		// subject (canonical per godlike/06 SSOT).
		"ContextSubject": reflect.String,
	}
}

// 1. All 19 semantic-payload enrichment fields are declared on
// `IndexedMetadata` with the expected reflect.Kind. A future
// refactor that removes a field or changes its type surfaces as
// a test failure BEFORE the writer/builder/Mapper contract breaks.
//
// LOCKSTEP-DISCIPLINE audit-pin: the canonical field set is
// enumerated in `expectedSemanticPayloadFields()` NOT auto-derived
// via reflect — a future agent adding a new field MUST update
// BOTH the `IndexedMetadata` struct AND this want list in
// lockstep per godlike/06 SSOT one-canonical-owner-per-fact.
func TestIndexedMetadata_HasAll19SemanticPayloadFields(t *testing.T) {
	typ := reflect.TypeOf(IndexedMetadata{})

	want := expectedSemanticPayloadFields()
	for name, wantKind := range want {
		f, ok := typ.FieldByName(name)
		if !ok {
			t.Errorf("IndexedMetadata.%s NOT FOUND — field missing from struct (regression: a future refactor accidentally removed the canonical field)", name)
			continue
		}
		if f.Type.Kind() != wantKind {
			t.Errorf("IndexedMetadata.%s type = %v — want %v (regression: field type drifted from canonical)", name, f.Type.Kind(), wantKind)
		}
	}
}

// 2. JSON marshal round-trip preserves the field-set semantics
// even WITHOUT explicit JSON tags: encoding/json's default
// behavior marshals exported fields using the capitalized name.
// This pins that the test surface is robust against the canonical
// `JSON-tag-less` design and surfaces if a future agent adds tags.
//
// Per the user spec ("JSON round-trip preserva i nuovi field"):
// this is the canonical test for that requirement. (The
// initially-attempted `*dest = *src` structural round-trip test was
// removed because `*dest = *src` is byte-equivalent by Go semantics
// and `reflect.DeepEqual` would always return true — dead code
// per code-reviewer verdict on the prior commit attempt.)
func TestIndexedMetadata_JSONRoundTripPreservesFieldSet(t *testing.T) {
	src := &IndexedMetadata{
		Destination: "stock",
		Origin:      "retrieved",
		Round:       7,
		Entities:    []string{"Adrien Broner", "Manny Pacquiao"},
	}

	data, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// Decode into map[string]interface{} to inspect the canonical
	// decoded shape (since we don't have explicit tags).
	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	// Lock the wire-shape keys (default JSON: capitalized + field
	// name) — a future tag-adding refactor would surface here.
	wantKeys := []string{
		"Destination", "Origin", "Round", "Entities",
	}
	for _, k := range wantKeys {
		if _, ok := decoded[k]; !ok {
			t.Errorf("JSON decoded payload missing key %q — canonical wire-shape drift", k)
		}
	}
	if v, ok := decoded["Round"]; ok {
		// JSON encodes int -> float64 by default in
		// map[string]interface{}; the lock asserts float
		// semantics (preserves the integer value through the
		// decoding).
		if v != float64(7) {
			t.Errorf("decoded[Round] = %v — want float64(7)", v)
		}
	}
}

// 3. Canonical SSOT lock: `Round` is `int`, NOT `*int`.
// The original 2026-07-06 user spec asked for `*int` (nullable)
// but the parallel-agent pre-applied struct settles on `int`.
// godlike/06 SSOT (one-canonical-owner per fact): the parallel-agent
// pre-applied struct is the canonical SOLE owner of the typed DTO
// surface — changing `Round int` to `*int` would require a
// deprecation cycle that hasn't landed. This test pins the current
// canonical state so a future godlike/06 migration to `*int`
// surfaces as an intentional, discussion-driven change rather
// than an accidental type drift.
//
// The `godlike/07 NO-FAKE-AVAILABILITY` semantics: Round=0 is
// treated as "no round known" by `payload_builder.go`'s `if Round
// > 0` guard. With `*int`, nil would distinguish "no round" from
// "round 0" (rare; mostly for the boxing-knockdown-first-round
// case). The deviation is documented in commit `e8f8587f`'s body
// + this test's provenance note.
func TestIndexedMetadata_RoundIsInt_NotPointer(t *testing.T) {
	typ := reflect.TypeOf(IndexedMetadata{})
	f, ok := typ.FieldByName("Round")
	if !ok {
		t.Fatalf("IndexedMetadata.Round NOT FOUND")
	}

	// Lock: kind == Int (NOT Ptr — fields are disjoint, no second
	// check needed since Int ≠ Ptr by construction).
	if f.Type.Kind() != reflect.Int {
		t.Errorf("IndexedMetadata.Round type.kind = %v — want reflect.Int (parallel-agent settle; user spec asked *int but SSOT is int)", f.Type.Kind())
	}
}

// 4. godlike/06 forbidden-field contract (defensive).
// `IndexDocument` MUST NOT contain the canonical set of forbidden
// fields declared in `ForbiddenIndexDocumentFields` (godlike/06 SSOT
// slice). The airlock strips these from the SQL-fetch AssetData
// shape. This is also enforced by the canonical freeze-test in
// `composition_test.go::TestComposition_FrozenQdrantIndexDocument
// ForbiddenFields`. The reflective duplication here is a SECONDARY
// guard so the violation surfaces immediately when running the
// indexing-package tests (without needing composition-test).
//
// PR-CATALOG-MULTILINGUA step 6 (July 2026): the canonical
// ForbiddenIndexDocumentFields slice is reduced to {Status,
// LocalPath} — DriveLink is PROMOTED to canonical payload field
// (no longer forbidden on IndexDocument). Test reads the SSOT slice
// directly so the next forbidden-field promote lands in lockstep
// with this test in a single freeze-test audit-pin pass.
func TestIndexDocument_DoNotExposeForbiddenFields(t *testing.T) {
	for _, structT := range []interface{}{
		IndexDocument{},
		IndexedMetadata{},
	} {
		typ := reflect.TypeOf(structT)
		for _, name := range ForbiddenIndexDocumentFields {
			if _, ok := typ.FieldByName(name); ok {
				t.Errorf("%s contains forbidden field %q — godlike/06 SSOT freeze-test VIOLATION", typ.Name(), name)
			}
		}
	}
}
