package imagesearch

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// EntityExtractor is the structural port consumed by the Resolver. It mirrors
// the canonical adapters.EntityExtractor signature; Go structural typing keeps
// the conservative CPU extractor (internal/infrastructure/nlp/local) and the
// Ollama hybrid adapter assignable here without the capability package
// importing infrastructure.
type EntityExtractor interface {
	ExtractEntities(ctx context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error)
}

// Request is the self-contained input of one image search decision. Text is
// the scene's narration sentence (or paragraph); PriorPersons carries the
// canonical person names resolved by PREVIOUS sentences so pronoun
// coreference ("He …") can ground an antecedent.
type Request struct {
	Text     string
	Language string
	// PriorPersons are the resolved person names of previous sentences, most
	// recent first. They are the ONLY coreference context: the resolver never
	// invents an antecedent from outside this slice.
	PriorPersons []string
}

// ResolvedEntity is one typed entity of the decision: what to search for
// (Type + Text), under which canonical identity (CanonicalID), with the
// optional retrieval qualifier (Hint) and the first-mention offset used for
// stable ordering (MentionAt).
type ResolvedEntity struct {
	Type string `json:"type"`
	// Text is the surface (or canonical concept) text of the entity:
	// "Floyd Mayweather", "Apple Vision Pro", "apple fruit", "real estate".
	Text string `json:"text"`
	// CanonicalID is the stable canonical identity derived by the entities
	// package (e.g. "person:floyd-mayweather", "product:apple-vision-pro").
	// Empty for annotations that are not linkable entities (CONTEXT).
	CanonicalID string `json:"canonical_id,omitempty"`
	// Hint is the retrieval qualifier that keeps the query unambiguous
	// ("boxer", "basketball", "actor", "company", "car"). Empty when the
	// identity is unambiguous on its own.
	Hint string `json:"hint,omitempty"`

	// QueryName is the surface used inside a search query when it differs
	// from Text (e.g. "Michael B Jordan" without the period).
	QueryName string `json:"-"`
	// Verbatim is the exact surface that matched in the source text (e.g.
	// "mela" for the canonical "apple fruit", "giaguaro" for "jaguar",
	// "Arabia Saudita" for "Saudi Arabia"). Used for language-aware query
	// folding (adjective placement in Italian).
	Verbatim string `json:"-"`
	// Domain groups identities for the co-occurrence rules ("boxing",
	// "basketball", "acting", …). Empty for non-persons.
	Domain string `json:"-"`
	// MentionAt is the rune offset of the entity's first mention in the
	// source text; used only for deterministic ordering.
	MentionAt int `json:"-"`
	// KindWord is the generic kind appended to a query for generic subjects
	// ("animal", "fruit").
	KindWord string `json:"-"`
	// FoldedIntoProduct marks an org/brand entity that is represented by its
	// merged product query ("Apple" → "Apple Vision Pro") and therefore does
	// not emit a query of its own.
	FoldedIntoProduct bool `json:"-"`
}

// ImageSearchDecision is the full output of the resolver for one sentence.
type ImageSearchDecision struct {
	// Required is the image search decision: true when at least one non-
	// negated imageable entity exists, false for abstract sentences.
	Required bool `json:"image_search_required"`
	// NoImageReason explains a Required=false decision (empty when true).
	NoImageReason string `json:"no_image_reason,omitempty"`
	// Queries is the ordered image search query list: primary query first,
	// then secondary entity queries, then the optional event query. Value
	// entities (MONEY/DATE/EVENT) never appear here — they belong to the
	// visual system, not to a stock-image search.
	Queries []string `json:"queries"`
	// Primary is the entity that must drive the primary image (never nil when
	// Required is true).
	Primary *ResolvedEntity `json:"primary,omitempty"`
	// Entities are the imageable, non-negated entities in editorial priority
	// order (the set the image search must represent).
	Entities []ResolvedEntity `json:"entities"`
	// Contexts are disambiguation annotations ("basketball", "actor") that
	// qualify a person identity; they never generate queries on their own.
	Contexts []ResolvedEntity `json:"contexts,omitempty"`
	// Visual are the value entities the visual system renders as graphics /
	// badges (MONEY, DATE, EVENT) — explicitly NOT image search subjects.
	Visual []ResolvedEntity `json:"visual,omitempty"`
	// Negated are the entities the sentence explicitly excludes ("Mike Tyson"
	// in "Tyson Fury, not Mike Tyson"). They must never drive an image.
	Negated []ResolvedEntity `json:"negated,omitempty"`
	// ImportantPhrases are the editorial phrases (e.g. "earned more than
	// $100 million") that can feed the visual scheduler.
	ImportantPhrases []string `json:"important_phrases,omitempty"`
}
