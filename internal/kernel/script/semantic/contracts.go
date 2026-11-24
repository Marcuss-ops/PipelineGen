package semantic

import (
	"encoding/json"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Wire contracts shared by the scripts capability and platform adapters.
// They live in the kernel so the adapters never import the business package.

type VisualEntity struct {
	Text              string               `json:"text"`
	Type              scriptpkg.EntityType `json:"type"`
	Score             float32              `json:"score"`
	Start             int                  `json:"start"`
	End               int                  `json:"end"`
	Evidence          string               `json:"evidence,omitempty"`
	IdentityID        string               `json:"identity_id,omitempty"`
	CanonicalID       string               `json:"canonical_id,omitempty"`
	Role              string               `json:"role,omitempty"`
	SearchIntents     []string             `json:"search_intents,omitempty"`
	EditorialDecision bool                 `json:"editorial_decision,omitempty"`
	EditorialSurface  string               `json:"editorial_surface,omitempty"`
	EditorialType     string               `json:"editorial_type,omitempty"`
	PromptEcho        bool                 `json:"prompt_echo,omitempty"`
	VisualAdmission   bool                 `json:"visual_admission,omitempty"`
	// EntityIndex is the packaged analyzer index that produced the linked ids.
	EntityIndex string `json:"entity_index,omitempty"`
}

type BulletCandidate struct {
	ID, Kind, Evidence, Provenance string
	Text                           string
	SentenceIndex                  int
	Start                          int
	End                            int
	// Score is the semantic engine's grounded salience; it travels to
	// Phrase Impact so candidate ranking uses one shared priority function.
	Score float64
}

type ChartValueCandidate struct {
	Text  string
	Value float64
}

type SemanticIdentity struct {
	ID, CanonicalSurface, Kind string
	Mentions                   []SemanticIdentityMention
	// Link fields are populated by semantic_manifest.v3. Empty canonical_id
	// means the entity is ambiguous or unlinked; it must not be treated as a KB ID.
	SurfaceKey          string
	CanonicalID         string
	LinkStatus          string
	CanonicalName       string
	Role                string
	DisambiguationGloss string
	NegativeConstraints []string
	SearchIntents       []SemanticSearchIntent
	LinkCandidates      []SemanticLinkCandidate `json:"link_candidates,omitempty"`
	LinkMargin          *int                    `json:"link_margin,omitempty"`
	LinkRule            string                  `json:"link_rule,omitempty"`
	// EditorialDecision is true when BrainRust supplied the v3 editorial
	// projection. Consumers must not re-run NER repair or prompt-echo rules.
	EditorialDecision bool
	EditorialSurface  string
	EditorialType     string
	PromptEcho        bool
	VisualAdmission   bool
}

type SemanticIdentityMention struct {
	SceneID, Surface string
	Start, End       int
}

type SemanticSearchIntent struct {
	Kind, Query, Lang string
}

type SemanticLinkEvidenceSpan struct {
	SceneID   string `json:"scene_id"`
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
}

type SemanticLinkFeature struct {
	Name  string                     `json:"name"`
	Value int                        `json:"value"`
	Terms []string                   `json:"terms,omitempty"`
	Spans []SemanticLinkEvidenceSpan `json:"spans,omitempty"`
}

type SemanticLinkCandidate struct {
	KBID     string                `json:"kb_id"`
	Name     string                `json:"name"`
	Score    int                   `json:"score"`
	Features []SemanticLinkFeature `json:"features"`
}

type SemanticChartItem struct {
	Label, Text, Kind string
	Value             float64
	Start, End        int
}

type SemanticSearchFeedback struct {
	Entries []SemanticSearchFeedbackEntry
}

type SemanticSearchFeedbackEntry struct {
	ProposalID, SceneID string
	Queries             []SemanticSearchQuery
	// Status is `pending` while the engine owns the entry; the planner writes
	// `satisfied` or `failed` after retrieval.
	Status string
	// Retry is nil until a search failed.
	Retry *SemanticSearchRetry
}

type SemanticVisualConsolidation struct {
	Schema               string
	Inventory            SemanticConsolidationInventory
	ProductionCandidates []SemanticProductionCandidate
	Facts                []SemanticConsolidatedFact
	Dates                []SemanticConsolidatedDate
	ReferenceNumbers     []SemanticReferenceNumber
	EventLinks           []SemanticEventLink
	Scenes               []SemanticConsolidationSceneStats
	Metrics              SemanticConsolidationMetrics
}

type SemanticConsolidationInventory struct {
	EntityMentions, UniqueIdentities, NamedIdentities int
	Facts, CanonicalFacts, Proposals                  int
	SearchEntries, Queries                            int
	TemporalFacts, CanonicalDates                     int
	MicroSummaries, CanonicalMicroSummaries           int
	Groups, Timelines                                 int
}

type SemanticProductionCandidate struct {
	ID, Recipe          string
	Rank                int
	Priority            float64
	SceneID             string
	SceneIDs, EntityIDs []string
	FactIDs, GroupIDs   []string
	Title, Evidence     string
	Start, End          int
	// VisualPayload is the grounded recipe payload (visual_recipe.v1) forwarded
	// verbatim to overlay-compiler. Nil for every candidate without a recipe.
	VisualPayload    json.RawMessage
	SuggestedVisual  string
	ChartItems       []SemanticChartItem
	TemporalSurfaces []string
	AssetKey         string
	Queries          []SemanticSearchQuery
	RequiresMedia    bool
	DedupKey         string
	CoveredFactIDs   []string
	Rationale        []string
}

type SemanticConsolidatedFact struct {
	ID, FactKey                string
	FactIDs                    []string
	SubjectEntityID, Predicate string
	NormalizedValue            string
	NormalizedDate             string
	Status                     string
	Importance                 float64
	SceneIDs                   []string
	SentenceIndex              int
	Evidence                   string
	Start, End                 int
	EvidenceStart, EvidenceEnd int
	Representation             *SemanticFactRepresentation
	UncoveredReason            string
}

type SemanticFactRepresentation struct {
	CandidateID, Recipe string
}

type SemanticConsolidatedDate struct {
	NormalizedDate string
	Count          int
	Surfaces       []string
	Spans          []SemanticConsolidatedDateSpan
}

type SemanticConsolidatedDateSpan struct {
	SceneID, Surface string
	Start, End       int
}

type SemanticReferenceNumber struct {
	SceneID, Surface, Reason string
	Start, End               int
}

type SemanticEventLink struct {
	ID, GroupID, SceneID string
	FromCandidateID      string
	ToCandidateID        string
	Kind, Evidence       string
	Start, End           int
}

type SemanticConsolidationSceneStats struct {
	SceneID              string
	RawProposals         int
	ProductionCandidates int
	RawQueries           int
	CanonicalFacts       int
	CoveredFacts         int
	TimelineCandidates   int
}

type SemanticConsolidationMetrics struct {
	RawProposals               int
	ProductionCandidates       int
	RawSearchEntries           int
	ProductionSearchCandidates int
	RawQueries                 int
	ProductionQueries          int
	DuplicateEntityMentions    int
	DuplicateFactsMerged       int
	DuplicateQueriesRemoved    int
	DuplicateProposalsRemoved  int
	EstimatedSearchJobs        int
	SharedAssetReuses          int
	CanonicalFacts             int
	CoveredFacts               int
	UncoveredFacts             int
	ReferenceNumbers           int
}

type SemanticSearchRetry struct {
	Action, Reason string
}

type SemanticSearchQuery struct {
	Kind              string                         `json:"kind"`
	Text              string                         `json:"text"`
	Origin            string                         `json:"origin,omitempty"`
	Illustrative      bool                           `json:"illustrative,omitempty"`
	Evidence          string                         `json:"evidence,omitempty"`
	EvidenceStartByte int                            `json:"evidence_start_byte"`
	EvidenceEndByte   int                            `json:"evidence_end_byte"`
	Components        []SemanticSearchQueryComponent `json:"components,omitempty"`
	TemplateID        string                         `json:"template_id,omitempty"`
	DerivedTerms      []string                       `json:"derived_terms,omitempty"`
}

type SemanticSearchQueryComponent struct {
	Kind          string `json:"kind"`
	ComponentType string `json:"component_type"`
	Text          string `json:"text"`
	StartByte     int    `json:"start_byte"`
	EndByte       int    `json:"end_byte"`
}
