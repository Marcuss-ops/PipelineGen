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
// LOCATION), measurable values (DATE/MONEY/NUMBER/PERCENTAGE), editorial
// artifacts (IMPORTANT_PHRASE/QUOTE/CLAIM/STATISTIC/RANKING/TITLE/EVENT) and
// image-bound entities (IMAGE_ENTITY). It carries BOTH the verbatim rune span
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
	"fmt"
	"strings"
)

// SemanticType is the canonical semantic-index vocabulary. It is a superset of
// the entity vocabulary (PERSON/ORGANIZATION/LOCATION): the index also records
// measurable values and editorial artifacts so the same index can drive
// overlays, motion graphics, sound effects and B-roll without any downstream
// stage re-deriving what was said.
type SemanticType string

const (
	// ── Entity types ────────────────────────────────────────────────
	SemanticPerson       SemanticType = "PERSON"
	SemanticOrganization SemanticType = "ORGANIZATION"
	SemanticLocation     SemanticType = "LOCATION"

	// ── Measurable value types ──────────────────────────────────────
	SemanticDate       SemanticType = "DATE"
	SemanticMoney      SemanticType = "MONEY"
	SemanticNumber     SemanticType = "NUMBER"
	SemanticPercentage SemanticType = "PERCENTAGE"

	// ── Editorial artifact types ────────────────────────────────────
	SemanticImportantPhrase SemanticType = "IMPORTANT_PHRASE"
	SemanticQuote           SemanticType = "QUOTE"
	SemanticClaim           SemanticType = "CLAIM"
	SemanticStatistic       SemanticType = "STATISTIC"
	SemanticRanking         SemanticType = "RANKING"
	SemanticTitle           SemanticType = "TITLE"
	SemanticEvent           SemanticType = "EVENT"

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
func (i SemanticItem) Validate() error {
	if strings.TrimSpace(i.SemanticID) == "" {
		return fmt.Errorf("%w: empty semantic_id", ErrInvalidSemanticItem)
	}
	if strings.TrimSpace(i.SceneID) == "" {
		return fmt.Errorf("%w: %q empty scene_id", ErrInvalidSemanticItem, i.SemanticID)
	}
	if strings.TrimSpace(string(i.Type)) == "" {
		return fmt.Errorf("%w: %q empty type", ErrInvalidSemanticItem, i.SemanticID)
	}
	if strings.TrimSpace(i.Text) == "" {
		return fmt.Errorf("%w: %q empty text", ErrInvalidSemanticItem, i.SemanticID)
	}
	if strings.TrimSpace(i.NormalizedText) == "" {
		return fmt.Errorf("%w: %q empty normalized_text", ErrInvalidSemanticItem, i.SemanticID)
	}
	if i.StartChar < 0 || i.EndChar <= i.StartChar {
		return fmt.Errorf("%w: %q invalid char span [%d,%d)", ErrInvalidSemanticItem, i.SemanticID, i.StartChar, i.EndChar)
	}
	if i.StartUS < 0 || i.EndUS <= i.StartUS {
		return fmt.Errorf("%w: %q invalid microsecond span [%d,%d)", ErrInvalidSemanticItem, i.SemanticID, i.StartUS, i.EndUS)
	}
	if i.Confidence < 0 || i.Confidence > 1 {
		return fmt.Errorf("%w: %q confidence %f out of range", ErrInvalidSemanticItem, i.SemanticID, i.Confidence)
	}
	return nil
}
