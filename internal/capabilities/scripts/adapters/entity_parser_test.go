// Package scripts — entity_parser_test.go exercises ParseEntities
// across all 3 parse paths (canonical, Italian-keyed, malformed).
//
// PR 7 (June 2026): typed Persons/Places/Concepts slots are
// populated from the postgen LLM JSON. The interpreter path is
// lenient: canonical shape first, Italian-keyed fallback, raw blob
// preserved in every path.
package adapters

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Canonical English-keyed shape ───────────────────────────────────────

// TestParseEntities_CanonicalFlatStringShape asserts the canonical
// English-keyed JSON with flat-string array elements populates
// the typed slots.
func TestParseEntities_CanonicalFlatStringShape(t *testing.T) {
	t.Parallel()
	input := `{"persons": ["Albert Einstein", "Marie Curie"], "places": ["Paris", "Bern"], "concepts": ["relativity"]}`
	got := ParseEntities(input)

	require.NotNil(t, got)
	assert.Equal(t, input, got.Raw, "Raw preserves the original JSON verbatim")

	require.Len(t, got.Persons, 2)
	assert.Equal(t, "Albert Einstein", got.Persons[0].Value)
	assert.Equal(t, "Marie Curie", got.Persons[1].Value)
	assert.Equal(t, float32(0), got.Persons[0].Score)

	require.Len(t, got.Places, 2)
	assert.Equal(t, "Paris", got.Places[0].Value)
	assert.Equal(t, "Bern", got.Places[1].Value)

	require.Len(t, got.Concepts, 1)
	assert.Equal(t, "relativity", got.Concepts[0].Value)
}

// TestParseEntities_CanonicalObjectElementShape asserts the
// canonical JSON with object-shaped elements ({"value":"..."})
// also parses into the typed slots.
func TestParseEntities_CanonicalObjectElementShape(t *testing.T) {
	t.Parallel()
	input := `{"persons": [{"value":"Albert Einstein","score":0.95}, {"name":"Marie Curie","score":0.88}]}`
	got := ParseEntities(input)

	require.NotNil(t, got)
	require.Len(t, got.Persons, 2)
	assert.Equal(t, "Albert Einstein", got.Persons[0].Value)
	assert.InDelta(t, float32(0.95), got.Persons[0].Score, 0.001)
	assert.Equal(t, "Marie Curie", got.Persons[1].Value)
	assert.InDelta(t, float32(0.88), got.Persons[1].Score, 0.001)
	assert.Equal(t, input, got.Raw)
}

// TestParseEntities_CanonicalMissingKeysLeavesNil asserts that
// missing top-level canonical keys leave the corresponding slot
// nil (not an empty slice).
func TestParseEntities_CanonicalMissingKeysLeavesNil(t *testing.T) {
	t.Parallel()
	input := `{"persons": ["A"]}`
	got := ParseEntities(input)

	require.NotNil(t, got)
	require.Len(t, got.Persons, 1)
	assert.Nil(t, got.Places, "Places must be nil when 'places' key is absent")
	assert.Nil(t, got.Concepts, "Concepts must be nil when 'concepts' key is absent")
}

// ── Italian-keyed fallback ──────────────────────────────────────────────

// TestParseEntities_ItalianShape_ObjectElements asserts that the
// Italian-keyed JSON with object-shaped elements
// (`nomi_speciali: [{"value":"X","score":0.9}]`) also parses
// into the typed Persons slot. This pins the cross-shape
// behaviour: the lenient flexEntity.UnmarshalJSON bridge applies
// to BOTH canonical and Italian-keyed envelopes.
func TestParseEntities_ItalianShape_ObjectElements(t *testing.T) {
	t.Parallel()
	input := `{
		"nomi_speciali": [
			{"value": "Vesuvio", "score": 0.95},
			{"name": "Pompei", "score": 0.88}
		],
		"parole_importanti": [{"text": "pomice", "score": 0.7}]
	}`
	got := ParseEntities(input)
	require.NotNil(t, got)

	require.Len(t, got.Persons, 2)
	assert.Equal(t, "Vesuvio", got.Persons[0].Value)
	assert.InDelta(t, float32(0.95), got.Persons[0].Score, 0.001)
	assert.Equal(t, "Pompei", got.Persons[1].Value)
	assert.InDelta(t, float32(0.88), got.Persons[1].Score, 0.001)

	require.Len(t, got.Concepts, 1)
	assert.Equal(t, "pomice", got.Concepts[0].Value)
	assert.InDelta(t, float32(0.7), got.Concepts[0].Score, 0.001)
	assert.Equal(t, input, got.Raw)
}

// TestParseEntities_ItalianShape_MapsToPersonsAndConcepts asserts
// the Italian-keyed prompts.yaml shape (nomi_speciali +
// parole_importanti) populates Persons and Concepts. The Italian
// schema has no dedicated place bucket — Places must remain nil.
func TestParseEntities_ItalianShape_MapsToPersonsAndConcepts(t *testing.T) {
	t.Parallel()
	input := `{"nomi_speciali": ["Vesuvio", "San Marzano", "Pompei"], "parole_importanti": ["mozzarella di bufala", "forno a legna"]}`
	got := ParseEntities(input)

	require.NotNil(t, got)
	assert.Equal(t, input, got.Raw)

	require.Len(t, got.Persons, 3)
	assert.Equal(t, "Vesuvio", got.Persons[0].Value)
	assert.Equal(t, "San Marzano", got.Persons[1].Value)
	assert.Equal(t, "Pompei", got.Persons[2].Value)

	require.Len(t, got.Concepts, 2)
	assert.Equal(t, "mozzarella di bufala", got.Concepts[0].Value)
	assert.Equal(t, "forno a legna", got.Concepts[1].Value)

	assert.Nil(t, got.Places, "Italian-keyed fallback has no place bucket; Places must be nil")
}

// TestParseEntities_ItalianShape_DropsOtherKeys asserts that the
// keys not modelled in EntityResult (frasi_importanti,
// entity_senza_testo, artlist_phrases) are silently dropped.
// They remain accessible via Raw.
func TestParseEntities_ItalianShape_DropsOtherKeys(t *testing.T) {
	t.Parallel()
	input := `{
		"frasi_importanti": ["a verbatim sentence"],
		"entity_senza_testo": {"VisualSubject": "some search"},
		"nomi_speciali": ["Einstein"],
		"parole_importanti": ["relativity"],
		"artlist_phrases": ["visual phrase 1", "visual phrase 2"]
	}`
	got := ParseEntities(input)

	require.NotNil(t, got)
	assert.Equal(t, input, got.Raw)
	assert.Equal(t, 1, len(got.Persons))
	assert.Equal(t, "Einstein", got.Persons[0].Value)
	assert.Equal(t, 1, len(got.Concepts))
	assert.Equal(t, "relativity", got.Concepts[0].Value)
	// Dropped keys must NOT appear in any typed slot.
	for _, e := range got.Persons {
		assert.NotEqual(t, "a verbatim sentence", e.Value)
	}
	for _, e := range got.Concepts {
		assert.NotEqual(t, "visual phrase 1", e.Value)
	}
}

// ── Edge cases ─────────────────────────────────────────────────────────

// TestParseEntities_EmptyInput asserts an empty rawJSON returns
// an EntityResult with Raw set and all typed slots nil. No
// allocation of typed slices.
func TestParseEntities_EmptyInput(t *testing.T) {
	t.Parallel()
	got := ParseEntities("")
	require.NotNil(t, got)
	assert.IsType(t, &scriptpkg.EntityResult{}, got)
	assert.Equal(t, "", got.Raw)
	assert.Nil(t, got.Persons)
	assert.Nil(t, got.Places)
	assert.Nil(t, got.Concepts)
}

// TestParseEntities_NullCanonicalKeyDoesNotRoute asserts that
// `persons: null` (or a null+other-keys mixture) does NOT route
// to the canonical path. The probe-based dispatcher rejects null
// values for the routing key.
func TestParseEntities_NullCanonicalKeyDoesNotRoute(t *testing.T) {
	t.Parallel()
	input := `{"persons": null, "places": ["Paris"]}`
	got := ParseEntities(input)
	require.NotNil(t, got)
	// persons:null is treated as absent; only `persons` is the
	// routing key. The JSON now lacks both routing keys (after
	// null-stripping) → both typed slots nil, Raw preserved.
	assert.Nil(t, got.Persons)
	assert.Nil(t, got.Places)
	assert.Equal(t, input, got.Raw)
}

// TestParseEntities_WhitespaceOnlyInput pins the
// TrimSpace-before-unmarshal behaviour.
func TestParseEntities_WhitespaceOnlyInput(t *testing.T) {
	t.Parallel()
	got := ParseEntities("   \n\t  ")
	require.NotNil(t, got)
	assert.Equal(t, "   \n\t  ", got.Raw)
	assert.Nil(t, got.Persons)
}

// TestParseEntities_MalformedJSONLeavesRawAlone asserts that a
// malformed JSON (neither canonical nor Italian-keyed shape) is
// preserved on Raw with all typed slots nil. Failure is
// failure-soft — the caller can recover the LLM blob via Raw.
func TestParseEntities_MalformedJSONLeavesRawAlone(t *testing.T) {
	t.Parallel()
	const raw = `{"this is not valid JSON:`
	got := ParseEntities(raw)
	require.NotNil(t, got)
	assert.Equal(t, raw, got.Raw)
	assert.Nil(t, got.Persons)
	assert.Nil(t, got.Places)
	assert.Nil(t, got.Concepts)
}

// TestParseEntities_PreservesRawOnSuccess asserts Raw is
// populated even when parsing succeeds (used by persisted rows
// for debug visibility).
func TestParseEntities_PreservesRawOnSuccess(t *testing.T) {
	t.Parallel()
	const raw = `{"persons":["A"]}`
	got := ParseEntities(raw)
	require.NotNil(t, got)
	assert.Equal(t, raw, got.Raw)
	assert.Equal(t, "A", got.Persons[0].Value)
}

// TestParseEntities_DropsEmptyValueEntries asserts that elements
// with empty (or whitespace-only) Value are dropped from the
// typed slots.
func TestParseEntities_DropsEmptyValueEntries(t *testing.T) {
	t.Parallel()
	input := `{"persons": ["Einstein", "", "  ", "Curie"]}`
	got := ParseEntities(input)
	require.NotNil(t, got)
	require.Len(t, got.Persons, 2)
	assert.Equal(t, "Einstein", got.Persons[0].Value)
	assert.Equal(t, "Curie", got.Persons[1].Value)
}

// TestParseEntities_UnknownElementTypeSkipped asserts that JSON
// elements of neither string nor object shape (numbers, arrays,
// booleans) are silently skipped without polluting the slot.
func TestParseEntities_UnknownElementTypeSkipped(t *testing.T) {
	t.Parallel()
	input := `{"persons": ["Einstein", 42, true, null, {"value":"Curie"}]}`
	got := ParseEntities(input)
	require.NotNil(t, got)
	require.Len(t, got.Persons, 2)
	assert.Equal(t, "Einstein", got.Persons[0].Value)
	assert.Equal(t, "Curie", got.Persons[1].Value)
}

// TestParseEntities_FromProcessorIntegration asserts that
// ParseEntities plays nicely with the entity-extractor output:
// the input mirrors what the entity_extraction prompt in
// prompts.yaml actually asks for end-to-end.
func TestParseEntities_FromProcessorIntegration(t *testing.T) {
	t.Parallel()
	// Simulates a real LLM-emitted entitiesJSON dump from the
	// Italian-keyed prompt.
	input := strings.TrimSpace(`
		{
		  "frasi_importanti": ["Una frase evocativa."],
		  "entity_senza_testo": {"VisualSubject": "Vesuvio eruzione"},
		  "nomi_speciali": ["Vesuvio", "Pompei", "Plinio il Vecchio"],
		  "parole_importanti": ["pomice", "lapilli"],
		  "artlist_phrases": ["Vesuvio erupting", "Roman ruins excavation"]
		}
	`)
	got := ParseEntities(input)
	require.NotNil(t, got)

	require.Len(t, got.Persons, 3)
	assert.Equal(t, "Vesuvio", got.Persons[0].Value)
	assert.Equal(t, "Pompei", got.Persons[1].Value)
	assert.Equal(t, "Plinio il Vecchio", got.Persons[2].Value)

	require.Len(t, got.Concepts, 2)
	assert.Equal(t, "pomice", got.Concepts[0].Value)
	assert.Equal(t, "lapilli", got.Concepts[1].Value)

	// Raw must be the verbatim input (no whitespace transformation).
	assert.Equal(t, input, got.Raw)
}
