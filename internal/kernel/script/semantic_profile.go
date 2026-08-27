// Package script — semantic_profile.go is the single canonical owner of
// the per-segment semantic understanding contract (SegmentSemanticProfile).
//
// Segment understanding produces exactly one profile per segment; every
// media provider (YouTube, Artlist, images, existing stock) consumes it to
// build its own queries. The profile is the canonical point where the
// deterministic NLP entities and the small-LLM semantics merge before
// provider fanout. The architectural rule it encodes:
//
//	NLP identifies. Small LLM understands. Query builders translate.
//	VidRush chooses. The canonical asset pipeline persists.
package script

import "strings"

// TermKind classifies the semantic function of a term extracted from a
// segment. The kind keeps ENTITY ≠ KEYWORD ≠ VISUAL QUERY strictly
// separated: terms with different kinds may combine, but never merge.
type TermKind string

const (
	// TermKindSubject is what the segment is about (e.g. "tractor").
	TermKindSubject TermKind = "subject"
	// TermKindContext is the setting/background of the segment
	// (e.g. "agriculture", "farm", "field").
	TermKindContext TermKind = "context"
	// TermKindVisual is something you want to SEE on screen
	// (e.g. "vintage tractor in a field").
	TermKindVisual TermKind = "visual"
	// TermKindTemporal is a time reference (e.g. "1892",
	// "early 20th century").
	TermKindTemporal TermKind = "temporal"
	// TermKindAction is an activity or process (e.g. "plowing",
	// "harvesting").
	TermKindAction TermKind = "action"
	// TermKindTechnology is a machine or technique (e.g. "steam engine",
	// "gasoline engine").
	TermKindTechnology TermKind = "technology"
)

// WeightedKeyword is one keyword or visual concept with its confidence.
// Confidence expresses how strongly the term is grounded in the segment
// text (0..1); it is not a provider relevance score.
type WeightedKeyword struct {
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence,omitempty"`
}

// SemanticTerm is a typed term with a functional kind and score. It is the
// scalable evolution of parallel keyword arrays: one typed term stream
// classified by TermKind instead of four ad-hoc arrays
// (subject/context/visual/temporal/action/technology).
type SemanticTerm struct {
	Value string   `json:"value"`
	Kind  TermKind `json:"kind"`
	Score float64  `json:"score,omitempty"`
}

// RetrievalIntent groups the per-provider retrieval queries derived
// deterministically from one segment profile. The small LLM produces
// understanding; the query builders produce these provider-specific
// queries. YouTube leans on entities+keywords, Artlist on visual terms,
// images on entity-first phrasing.
type RetrievalIntent struct {
	// YouTube holds search queries for the YouTube provider
	// (e.g. "John Froelich first gasoline tractor 1892").
	YouTube []string `json:"youtube,omitempty"`
	// Artlist holds visual-first queries for the Artlist provider
	// (e.g. "vintage tractor farm field").
	Artlist []string `json:"artlist,omitempty"`
	// Images holds entity-first queries for the internet/generated
	// image providers (e.g. "John Froelich 1892 gasoline tractor").
	Images []string `json:"images,omitempty"`
}

// SegmentSemanticProfile is the canonical per-segment semantic
// understanding contract. It is the single point where deterministic NLP
// entities and small-LLM semantics merge before provider fanout.
//
// Cacheability: SegmentID + TextHash + UnderstandingModelVersion +
// PromptVersion form the profile fingerprint. When the text does not
// change and the understanding stack did not change, the profile must be
// reused without recomputation (warm path straight to retrieval).
type SegmentSemanticProfile struct {
	// SegmentID is the stable segment identifier within the current
	// VidRush plan (matches CanonicalSegment.ID).
	SegmentID string `json:"segment_id"`
	// TextHash is the canonical text hash of the segment (matches
	// CanonicalSegment.TextHash). When the paragraph changes, the hash
	// changes and the cached profile is invalidated.
	TextHash string `json:"text_hash"`

	// UnderstandingModelVersion identifies the small-LLM model that
	// produced the semantic fields. Part of the profile fingerprint.
	UnderstandingModelVersion string `json:"understanding_model_version,omitempty"`
	// PromptVersion identifies the segment-understanding prompt. Part
	// of the profile fingerprint.
	PromptVersion string `json:"prompt_version,omitempty"`

	// Topic is the single-sentence topic of the segment
	// (e.g. "origine dei primi trattori").
	Topic string `json:"topic,omitempty"`
	// Subtopics are the secondary themes of the segment
	// (e.g. "macchine agricole", "motori a vapore").
	Subtopics []string `json:"subtopics,omitempty"`

	// Keywords are the segment concepts, weighted by how strongly they
	// are grounded in the text (e.g. "agricoltura", "animali da tiro").
	Keywords []WeightedKeyword `json:"keywords,omitempty"`
	// VisualTerms are the visual concepts — things you want to see on
	// screen (e.g. "horse drawn farming", "steam tractor").
	VisualTerms []WeightedKeyword `json:"visual_terms,omitempty"`
	// Terms is the scalable typed term stream (subject/context/visual/
	// temporal/action/technology). Optional: query builders may consume
	// it instead of the flat keyword arrays.
	Terms []SemanticTerm `json:"terms,omitempty"`

	// ImportantPhrases are the editorial phrases that must survive
	// retrieval (e.g. "John Froelich early gasoline tractor").
	ImportantPhrases []string `json:"important_phrases,omitempty"`

	// Entities are the strongly-typed named entities (PERSON, PLACE,
	// ORGANIZATION, DATE, EVENT). They are owned by the deterministic
	// NLP and must never be invented by the small LLM.
	Entities []ExtractedEntity `json:"entities,omitempty"`

	// Retrieval carries the per-provider queries derived from this
	// profile. LLM → understanding; code → provider adaptation.
	Retrieval *RetrievalIntent `json:"retrieval,omitempty"`
}

// BuildSegmentSemanticProfile is the SINGLE canonical point where a typed
// EntityResult extraction evolves into a SegmentSemanticProfile. No adapter
// may map extraction fields into a profile with parallel logic: producers
// call this builder and project from its result.
//
// The derivation keeps the extractor's division of authority: named entities
// come from the deterministic NLP buckets (an empty result never invents an
// entity), ImportantPhrases pass through verbatim, ImportantWords become the
// weighted Keywords stream and ArtlistPhrases become the weighted
// VisualTerms stream — the extractor's order IS the importance ranking, so
// the first term carries the highest deterministic confidence. Retrieval
// queries are intentionally NOT derived here: query builders translate the
// profile per provider.
func BuildSegmentSemanticProfile(seg CanonicalSegment, res EntityResult, understandingModelVersion, promptVersion string) SegmentSemanticProfile {
	profile := SegmentSemanticProfile{
		SegmentID:                 seg.ID,
		TextHash:                  seg.TextHash,
		UnderstandingModelVersion: understandingModelVersion,
		PromptVersion:             promptVersion,
		ImportantPhrases:          append([]string(nil), res.ImportantPhrases...),
	}
	profile.Entities = appendEntityGroup(profile.Entities, res.Persons, "PERSON")
	profile.Entities = appendEntityGroup(profile.Entities, res.Places, "LOCATION")
	profile.Entities = appendEntityGroup(profile.Entities, res.Concepts, "CONCEPT")
	profile.Keywords = weightedTerms(res.ImportantWords)
	profile.VisualTerms = weightedTerms(res.ArtlistPhrases)
	return profile
}

// appendEntityGroup projects one typed EntityResult bucket onto the profile's
// ExtractedEntity stream, defaulting an empty type to the bucket's canonical
// kind (PERSON / LOCATION / CONCEPT) exactly like the legacy projection.
func appendEntityGroup(dst []ExtractedEntity, bucket []Entity, defaultKind string) []ExtractedEntity {
	for _, entity := range bucket {
		value := strings.TrimSpace(entity.Value)
		if value == "" {
			continue
		}
		kind := strings.ToUpper(strings.TrimSpace(entity.Type))
		if kind == "" {
			kind = defaultKind
		}
		dst = append(dst, ExtractedEntity{Value: value, Type: kind, Confidence: float64(entity.Score)})
	}
	return dst
}

// weightedTerms converts an ordered list of extraction strings into a
// weighted keyword stream. The extractor's order is the importance ranking,
// so the first term carries the highest confidence (the same descending
// formula the scene-annotation projection uses for important words).
func weightedTerms(values []string) []WeightedKeyword {
	var cleaned []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	out := make([]WeightedKeyword, 0, len(cleaned))
	for i, value := range cleaned {
		out = append(out, WeightedKeyword{
			Value:      value,
			Confidence: float64(len(cleaned)-i) / float64(len(cleaned)),
		})
	}
	return out
}
