// Package entities — semantic_item.go defines the SemanticItem contract:
// the general, typed entry of a script's semantic index.
//
// The semantic index is the pipeline stage that sits between the script and
// the visual resolver:
//
//	SCRIPT → Semantic Extractor → SemanticItem index → Canonical Entity
//	Resolver → Visual Intent Resolver → ... → Renderer
//
// A SemanticItem is deliberately broader than an EntityOccurrence: it records
// every meaningful surface a scene mentions — entities (PERSON/ORGANIZATION/
// LOCATION/PRODUCT), measurable values (DATE/MONEY/NUMBER/PERCENTAGE/TIME),
// editorial artifacts (IMPORTANT_PHRASE/QUOTE/CLAIM/STATISTIC/RANKING/CHAPTER/
// TITLE/EVENT) and image-bound entities (IMAGE_ENTITY). It carries BOTH the
// verbatim rune span
// (start_char/end_char) and the integer-microsecond timing (start_us/end_us),
// so the WHAT (type/text) is always grounded in the WHEN — never text-length
// estimates.
//
// The extractor populates semantic_id/scene_id/type/text/normalized_text and
// both spans; the Canonical Entity Resolver fills canonical_entity_id. This
// package only owns the shape and its invariants — it does not decide which
// overlay or animation an item becomes (that is the Visual Intent Resolver's
// job, downstream).
package entities

import (
	"errors"
)

// SemanticType is the canonical semantic-index vocabulary. It is a superset of
// the entity vocabulary (PERSON/ORGANIZATION/LOCATION/PRODUCT): the index also
// records measurable values and editorial artifacts so the same index can drive
// overlays, motion graphics, sound effects and B-roll without any downstream
// stage re-deriving what was said.
type SemanticType string

const (
	// ── Entity types ────────────────────────────────────────────────
	SemanticPerson       SemanticType = "PERSON"
	SemanticOrganization SemanticType = "ORGANIZATION"
	SemanticLocation     SemanticType = "LOCATION"
	// SemanticProduct is a consumer/brand product name ("iPhone 17",
	// "Tesla Model 3"): an imageable, asset-bound entity that resolves a
	// product image popup.
	SemanticProduct SemanticType = "PRODUCT"

	// ── Measurable value types ──────────────────────────────────────
	SemanticDate       SemanticType = "DATE" // calendar day / point in time
	SemanticMoney      SemanticType = "MONEY"
	SemanticNumber     SemanticType = "NUMBER"
	SemanticPercentage SemanticType = "PERCENTAGE"
	// SemanticTime is a spoken duration ("tre ore", "20 minuti")
	// or a time-of-day: a value artifact that renders as a numeric
	// badge, never just a bare NUMBER or DATE.
	SemanticTime SemanticType = "TIME"

	// ── Editorial artifact types ────────────────────────────────────
	SemanticImportantPhrase SemanticType = "IMPORTANT_PHRASE"
	SemanticQuote           SemanticType = "QUOTE"
	SemanticClaim           SemanticType = "CLAIM"
	SemanticStatistic       SemanticType = "STATISTIC"
	SemanticRanking         SemanticType = "RANKING"
	SemanticTitle           SemanticType = "TITLE"
	SemanticEvent           SemanticType = "EVENT"
	// SemanticChapter is a section heading that separates the video's
	// chapters; it resolves to the full-frame chapter_card template.
	SemanticChapter SemanticType = "CHAPTER"

	// ── Image-bound entity ──────────────────────────────────────────
	SemanticImageEntity SemanticType = "IMAGE_ENTITY"
)

// SemanticItem is one grounded entry in a script's semantic index. It answers
// COSA mostrare (type/text) and QUANDO (start_us/end_us) for a single surface
// of a scene, together with A CHI appartiene (canonical_entity_id) once the
// Canonical Entity Resolver has linked it.
//
// All timing is integer microseconds, never floats, and all text offsets are
// Unicode-rune offsets (never UTF-8 byte offsets) — the same invariants the
// EntityOccurrence projection enforces, so downstream consumers never
// accumulate rounding errors or mis-anchor an overlay.
type SemanticItem struct {
	// SemanticID is the stable, scene-scoped item id (e.g.
	// "sem_scene03_person_01"). It is unique within a scene.
	SemanticID string `json:"semantic_id"`
	// SceneID is the id of the scene this item belongs to.
	SceneID string `json:"scene_id"`
	// Type is the canonical semantic type (SemanticPerson, SemanticMoney, ...).
	Type SemanticType `json:"type"`
	// Text is the verbatim surface text the extractor found ("Floyd
	// Mayweather", "more than 100 million dollars").
	Text string `json:"text"`
	// NormalizedText is the canonical normalized form used for dedup and
	// matching (for names: NormalizeName(Text) → "floyd mayweather").
	NormalizedText string `json:"normalized_text"`
	// StartChar/EndChar are the Unicode-rune offsets of Text's first verbatim
	// occurrence in the scene text.
	StartChar int `json:"start_char"`
	EndChar   int `json:"end_char"`
	// StartUS/EndUS are the integer-microsecond span during which the item is
	// spoken/active on the scene's own timeline.
	StartUS int64 `json:"start_us"`
	EndUS   int64 `json:"end_us"`
	// Confidence is the extractor's confidence in [0,1].
	Confidence float64 `json:"confidence"`
	// CanonicalEntityID is the resolved canonical identity (e.g.
	// "person:floyd-mayweather-jr"). It is empty until the Canonical Entity
	// Resolver links the item; it is omitted from the JSON when unset.
	CanonicalEntityID string `json:"canonical_entity_id,omitempty"`
}

// ErrInvalidSemanticItem is returned when a SemanticItem violates its index
// invariants (empty identity, inverted spans, out-of-range confidence).
var ErrInvalidSemanticItem = errors.New("invalid semantic item")

// Validate enforces the semantic-index invariants: non-empty identity, a
// canonical type and text, valid rune and microsecond spans, and a confidence
// within [0,1]. A consumer can never trust an item whose WHAT is not grounded
// in a monotonic WHEN.
