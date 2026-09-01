// Package scripts — entity_parser.go is the lenient typed parser
// for the postgen LLM entity JSON. It's the bridge between the
// free-form LLM JSON string (currently stored as
// EntityResult.Raw) and the typed Persons/Places/Concepts slices
// that downstream dashboards read at runtime.
//
// PR 7 (June 2026): the entity boundary calls ParseEntities instead
// of stuffing the raw string into EntityResult.Raw. With
// this, result.Artifacts.Entities.Persons is populated without
// the consumer having to fall back to Raw.
//
// Why lenient: smaller Ollama models sometimes emit the canonical
// English-keyed shape (`persons`, `places`, `concepts`), but the
// current prompts.yaml (`entity_extraction`) still asks the model
// for the Italian-keyed shape (`nomi_speciali`, `parole_importanti`,
// etc.). The parser tries canonical first and falls back to the
// Italian-keyed map if canonical unmarshal fails. On any further
// failure the raw string is preserved verbatim on Raw so the
// consumer can still surface the LLM output for debugging.
//
// Why entity elements come in two shapes: even when the LLM emits
// the canonical schema flexEntity is sometimes an object
// (`{"value":"X","score":0.9}`) and sometimes a flat string
// (`"X"`). The flexEntity custom unmarshaler accepts both.
package adapters

import (
	"encoding/json"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ParseEntities parses the postgen LLM entity JSON into a typed
// *scriptpkg.EntityResult. ALWAYS preserves the raw input string
// in the Raw field, regardless of parse success.
//
// PR 7 (June 2026): dispatch is probe-based (top-level keyset
// inspection) rather than naive fallback. The naive
// canonical-then-Italian fallback masked the Italian shape
// because Go's encoding/json is case-insensitive on struct field
// matching → unmarshaling an Italian-shaped payload (`{"nomi_speciali": [...]}`)
// into the canonical struct silently SUCCEEDS with all three
// typed slots empty (no error, just an empty result) → the
// Italian fallback never fired inside the prompts.yaml
// production shape. Fix: probe keys first, then route.
//
// Return shape by parse path:
//
//   - Empty rawJSON:           Raw=rawJSON, Persons/Places/Concepts=nil
//   - Canonical shape detected: Persons/Places/Concepts populated
//     from {persons, places, concepts},
//     Raw=rawJSON
//   - Italian-keyed shape detected: Persons ← nomi_speciali,
//     Concepts ← parole_importanti,
//     Places  ← nil (Italian schema has no
//     dedicated place bucket),
//     Raw=rawJSON
//   - Malformed JSON or unknown shape: Raw=rawJSON, typed slots=nil
//
// Quality rules:
//
//   - Empty `Value` entries are dropped from the typed slots.
//   - Other keys in the Italian shape (frasi_importanti,
//     entity_senza_testo, artlist_phrases) intentionally have no
//     typed slot in EntityResult — they are not entity buckets.
//     They remain accessible via the Raw field.
//   - The probe keys are: `persons` for canonical,
//     `nomi_speciali` for Italian.
func ParseEntities(rawJSON string) *scriptpkg.EntityResult {
	out := &scriptpkg.EntityResult{Raw: rawJSON}
	trimmed := strings.TrimSpace(rawJSON)
	if trimmed == "" {
		return out
	}

	// Step 1: probe. Find out which shape the top-level JSON
	// carries by inspecting its keyset without committing to a
	// typed struct. Both keys ('persons' for canonical and
	// 'nomi_speciali' for Italian) are required for dispatch.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		// Malformed JSON (not a top-level object, or invalid
		// syntax). Both downstream paths would fail; leave only
		// Raw populated.
		return out
	}

	hasPersons := hasTopLevelKey(probe, "persons")
	hasNomiSpeciali := hasTopLevelKey(probe, "nomi_speciali")

	// Step 2: dispatch on probe results.
	switch {
	case hasPersons:
		// Canonical English-keyed shape:
		//   {"persons": [...], "places": [...], "concepts": [...]}
		var canonical struct {
			Persons  []flexEntity `json:"persons,omitempty"`
			Places   []flexEntity `json:"places,omitempty"`
			Concepts []flexEntity `json:"concepts,omitempty"`
		}
		if err := json.Unmarshal([]byte(trimmed), &canonical); err == nil {
			out.Persons = toEntities(canonical.Persons)
			out.Places = toEntities(canonical.Places)
			out.Concepts = toEntities(canonical.Concepts)
		}
		return out

	case hasNomiSpeciali:
		// Italian-keyed shape (current prompts.yaml
		// `entity_extraction` template):
		//   {"nomi_speciali": [...], "parole_importanti": [...], ...}
		//
		// nomi_speciali is heterogeneous — persons, places,
		// organizations, works. It is mapped to the Persons slot
		// (the canonical heterogeneous-named-entity slot). The
		// Italian schema has no dedicated place bucket.
		var italian struct {
			NomiSpeciali     []flexEntity `json:"nomi_speciali,omitempty"`
			ParoleImportanti []flexEntity `json:"parole_importanti,omitempty"`
		}
		if err := json.Unmarshal([]byte(trimmed), &italian); err == nil {
			out.Persons = toEntities(italian.NomiSpeciali)
			out.Concepts = toEntities(italian.ParoleImportanti)
		}
		return out
	}

	// Step 3: neither shape detected. Top-level is an object
	// (probe succeeded) but contains neither persons nor
	// nomi_speciali. Both untyped-shape parse attempts would
	// produce all-nil typed slots anyway. Leave only Raw
	// populated.
	return out
}

// hasTopLevelKey reports whether a JSON object has a non-null
// top-level key. It treats `null` values as absent so that
// "persons": null does not route to the canonical parser.
func hasTopLevelKey(probe map[string]json.RawMessage, key string) bool {
	v, ok := probe[key]
	if !ok {
		return false
	}
	if len(v) == 0 {
		return false
	}
	// Treat JSON null as absent.
	if string(v) == "null" {
		return false
	}
	return true
}

// flexEntity accepts JSON elements in either a flat-string or
// {value, score}/{name, score}/{text, score} object form. The
// custom UnmarshalJSON is the lenient bridge — both shapes are
// observed from smaller Ollama models in production.
//
// Field-mapping priority for object shape:
//  1. `value` (canonical)
//  2. `name`  (legacy / alternative render)
//  3. `text`  (alternative render)
//
// Score is read from `score` when present (any numeric value;
// non-numeric values are silently ignored). The Score field is
// surfaced on Entity.Score but not consumed by the current
// document renderer — it is reserved for future dashboards / quality
// pulls (e.g. confidence-filtered entity lists).
type flexEntity struct {
	Value string
	Score float32
}

// UnmarshalJSON implements the lenient entity-element parse.
//
//	"Albert Einstein"                   → flexEntity{Value: "Albert Einstein"}
//	{"value":"Albert Einstein"}         → flexEntity{Value: "Albert Einstein"}
//	{"name":"Albert Einstein"}          → flexEntity{Value: "Albert Einstein"}
//	{"score":0.9,"value":"Einstein"}    → flexEntity{Value: "Einstein", Score: 0.9}
//	null / empty / non-string-or-object → skip (zero value)
func (f *flexEntity) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" {
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		f.Value = strings.TrimSpace(str)
		return nil
	}
	if s[0] == '{' {
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		if v, ok := raw["value"].(string); ok {
			f.Value = strings.TrimSpace(v)
		} else if v, ok := raw["name"].(string); ok {
			f.Value = strings.TrimSpace(v)
		} else if v, ok := raw["text"].(string); ok {
			f.Value = strings.TrimSpace(v)
		}
		if s, ok := raw["score"].(float64); ok {
			f.Score = float32(s)
		}
		return nil
	}
	// Unknown JSON type (number, array, etc.) — skip.
	return nil
}

// toEntities coerces a []flexEntity to a []scriptpkg.Entity.
// Empty Value entries are dropped (clean trim-whitespace check).
// nil input is preserved as nil — `Persons = nil` is the
// canonical contract for "key not present in source JSON" so
// callers can distinguish between "{places: []}" (empty list —
// caller wrote the key with no values) and "places absent" (the
// JSON had no `places` key at all). The distinction shows up in
// document renderer, which skips sub-headers when both Persons and
// Places are nil.
func toEntities(in []flexEntity) []scriptpkg.Entity {
	if in == nil {
		return nil
	}
	out := make([]scriptpkg.Entity, 0, len(in))
	for _, e := range in {
		if strings.TrimSpace(e.Value) == "" {
			continue
		}
		out = append(out, scriptpkg.Entity{Value: e.Value, Score: e.Score})
	}
	return out
}
