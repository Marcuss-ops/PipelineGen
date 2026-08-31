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

import (
	"fmt"
	"math"
	"strings"
)

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
// deterministically from one segment profile. Each provider keeps its own
// query language while sharing the same semantic understanding. The small
// LLM produces understanding; the query builders produce these provider-specific
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

	// Actions are the visual/editorial actions described by the segment.
	Actions []string `json:"actions,omitempty"`
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
	// VisualConcepts preserves the model's unweighted visual concepts.
	VisualConcepts []string `json:"visual_concepts,omitempty"`
	// Terms is the scalable typed term stream (subject/context/visual/
	// temporal/action/technology). Optional: query builders may consume
	// it instead of the flat keyword arrays.
	Terms []SemanticTerm `json:"terms,omitempty"`

	// ImportantPhrases are the editorial phrases that must survive
	// retrieval (e.g. "John Froelich early gasoline tractor").
	ImportantPhrases []string `json:"important_phrases,omitempty"`

	// NounChunks are the source-grounded multi-word noun phrases from
	// the deterministic NLP layer (e.g. "latte art", "specialty coffee
	// shop"). They pass through verbatim, like ImportantPhrases, and
	// are preserved into the per-segment insights surface.
	NounChunks []string `json:"noun_chunks,omitempty"`

	// Entities are the strongly-typed named entities (PERSON, PLACE,
	// ORGANIZATION, DATE, EVENT). They are owned by the deterministic
	// NLP and must never be invented by the small LLM.
	Entities []ExtractedEntity `json:"entities,omitempty"`

	// Retrieval carries the per-provider queries derived from this
	// profile. LLM → understanding; code → provider adaptation.
	Retrieval *RetrievalIntent `json:"retrieval,omitempty"`
}

// Validate checks the profile's identity, confidence ranges and required
// semantic values. It is intentionally side-effect free so callers can use
// it at persistence and provider boundaries.
func (p SegmentSemanticProfile) Validate() error {
	if strings.TrimSpace(p.SegmentID) == "" {
		return fmt.Errorf("segment semantic profile: segment_id is required")
	}
	if strings.TrimSpace(p.TextHash) == "" {
		return fmt.Errorf("segment semantic profile: text_hash is required")
	}
	for i, keyword := range p.Keywords {
		if err := validateWeightedKeyword(keyword, fmt.Sprintf("keywords[%d]", i)); err != nil {
			return err
		}
	}
	for i, term := range p.VisualTerms {
		if err := validateWeightedKeyword(term, fmt.Sprintf("visual_terms[%d]", i)); err != nil {
			return err
		}
	}
	for i, term := range p.Terms {
		if strings.TrimSpace(term.Value) == "" || term.Kind == "" {
			return fmt.Errorf("segment semantic profile: terms[%d] requires value and kind", i)
		}
		if err := validateConfidence(term.Score, fmt.Sprintf("terms[%d].score", i)); err != nil {
			return err
		}
	}
	for i, entity := range p.Entities {
		if strings.TrimSpace(entity.Value) == "" || strings.TrimSpace(entity.Type) == "" {
			return fmt.Errorf("segment semantic profile: entities[%d] requires value and type", i)
		}
		if err := validateConfidence(entity.Confidence, fmt.Sprintf("entities[%d].confidence", i)); err != nil {
			return err
		}
	}
	return nil
}

func validateWeightedKeyword(keyword WeightedKeyword, field string) error {
	if strings.TrimSpace(keyword.Value) == "" {
		return fmt.Errorf("segment semantic profile: %s.value is required", field)
	}
	return validateConfidence(keyword.Confidence, field+".confidence")
}

func validateConfidence(value float64, field string) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return fmt.Errorf("segment semantic profile: %s must be between 0 and 1", field)
	}
	return nil
}

// Clone returns an independent profile snapshot suitable for persistence or
// cache storage. It prevents callers from mutating the canonical profile via
// shared slices or the Retrieval pointer.
func (p SegmentSemanticProfile) Clone() SegmentSemanticProfile {
	clone := p
	clone.Subtopics = append([]string(nil), p.Subtopics...)
	clone.Keywords = append([]WeightedKeyword(nil), p.Keywords...)
	clone.VisualTerms = append([]WeightedKeyword(nil), p.VisualTerms...)
	clone.Terms = append([]SemanticTerm(nil), p.Terms...)
	clone.ImportantPhrases = append([]string(nil), p.ImportantPhrases...)
	clone.NounChunks = append([]string(nil), p.NounChunks...)
	clone.Actions = append([]string(nil), p.Actions...)
	clone.VisualConcepts = append([]string(nil), p.VisualConcepts...)
	clone.Entities = append([]ExtractedEntity(nil), p.Entities...)
	if p.Retrieval != nil {
		intent := *p.Retrieval
		intent.YouTube = append([]string(nil), p.Retrieval.YouTube...)
		intent.Artlist = append([]string(nil), p.Retrieval.Artlist...)
		intent.Images = append([]string(nil), p.Retrieval.Images...)
		clone.Retrieval = &intent
	}
	return clone
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
		NounChunks:                append([]string(nil), res.NounChunks...),
		Actions:                   append([]string(nil), res.Actions...),
		VisualConcepts:            append([]string(nil), res.VisualConcepts...),
	}
	profile.Entities = appendEntityGroup(profile.Entities, res.Persons, "PERSON")
	profile.Entities = appendEntityGroup(profile.Entities, res.Places, "LOCATION")
	profile.Entities = appendEntityGroup(profile.Entities, res.Concepts, "CONCEPT")
	profile.Keywords = weightedTerms(res.ImportantWords)
	visualValues := append([]string(nil), res.NounChunks...)
	visualValues = append(visualValues, res.ArtlistPhrases...)
	visualValues = append(visualValues, res.VisualConcepts...)
	profile.VisualTerms = weightedTerms(visualValues)
	if profile.Topic == "" {
		profile.Topic, profile.Subtopics = deriveUnderstanding(profile, seg.Text)
	}
	profile.Actions = appendUniqueProfileStrings(profile.Actions)
	for _, visual := range res.VisualConcepts {
		profile.VisualConcepts = appendUniqueProfileStrings(profile.VisualConcepts, visual)
	}
	profile.Terms = deriveSemanticTerms(profile)
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

// deriveUnderstanding supplies deterministic, source-grounded understanding
// fields when the legacy extraction surface has not yet carried explicit LLM
// topic fields. It deliberately never invents entities or facts: the segment
// text and extracted terms are the only inputs.
func deriveUnderstanding(profile SegmentSemanticProfile, text string) (string, []string) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	topic := strings.TrimSpace(text)
	if len(profile.ImportantPhrases) > 0 {
		topic = strings.TrimSpace(profile.ImportantPhrases[0])
	}
	var subtopics []string
	for _, term := range profile.Keywords {
		if value := strings.TrimSpace(term.Value); value != "" && !strings.EqualFold(value, topic) {
			subtopics = append(subtopics, value)
		}
		if len(subtopics) == 3 {
			break
		}
	}
	return topic, subtopics
}

func appendUniqueProfileStrings(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		seen[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

func deriveSemanticTerms(profile SegmentSemanticProfile) []SemanticTerm {
	terms := make([]SemanticTerm, 0, len(profile.Entities)+len(profile.Keywords)+len(profile.VisualTerms))
	for _, entity := range profile.Entities {
		kind := TermKindSubject
		switch strings.ToUpper(strings.TrimSpace(entity.Type)) {
		case "DATE", "TIME", "CARDINAL":
			kind = TermKindTemporal
		case "EVENT":
			kind = TermKindContext
		}
		terms = append(terms, SemanticTerm{Value: entity.Value, Kind: kind, Score: entity.Confidence})
	}
	for _, keyword := range profile.Keywords {
		terms = append(terms, SemanticTerm{Value: keyword.Value, Kind: classifyKeywordTerm(keyword.Value), Score: keyword.Confidence})
	}
	for _, visual := range profile.VisualTerms {
		terms = append(terms, SemanticTerm{Value: visual.Value, Kind: TermKindVisual, Score: visual.Confidence})
	}
	if len(terms) == 0 {
		return nil
	}
	return terms
}

func classifyKeywordTerm(value string) TermKind {
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(lower, "engine") || strings.Contains(lower, "motor") || strings.Contains(lower, "macchin") || strings.Contains(lower, "tractor") || strings.Contains(lower, "trattor") {
		return TermKindTechnology
	}
	if strings.Contains(lower, "farm") || strings.Contains(lower, "agricol") || strings.Contains(lower, "agriculture") || strings.Contains(lower, "field") || strings.Contains(lower, "campo") {
		return TermKindContext
	}
	if strings.Contains(lower, "plow") || strings.Contains(lower, "aratur") || strings.Contains(lower, "harvest") || strings.Contains(lower, "lavor") {
		return TermKindAction
	}
	return TermKindSubject
}

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
