package contract

import (
	"encoding/json"

	generationcontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/generation"
)

// SemanticSearchIntent is a typed, short retrieval query produced by the
// BrainRust entity linker. Go carries it unchanged to retrieval consumers.
type SemanticSearchIntent struct {
	Kind, Query, Lang string
}

type SemanticIdentityMention struct {
	SceneID, Surface string
	Start, End       int
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

type SemanticChartItem struct {
	Label, Text, Kind string
	Value             float64
	Start, End        int
}

// SemanticSearchFeedback is the whole-script asset-search handshake emitted by
// the engine. Every entry stays `pending` until PipelineGen performs retrieval;
// the planner then rewrites `status` and `retry` through
// SemanticProductionPlan so a failed search is reported in the same contract
// instead of disappearing into planner logs.
type SemanticSearchFeedback struct {
	Entries []SemanticSearchFeedbackEntry
}

// SemanticSearchFeedbackEntry is one image-backed proposal's search ladder.
type SemanticSearchFeedbackEntry struct {
	ProposalID, SceneID string
	Queries             []generationcontract.SemanticSearchQuery
	// Status is `pending` while the engine owns the entry; the planner writes
	// `satisfied` or `failed` after retrieval.
	Status string
	// Retry is nil until a search failed.
	Retry *generationcontract.SemanticSearchRetry
}

// SemanticVisualConsolidation is the script-level selective production view
// returned in the same semantic-engine response (v2 or v3). Raw per-scene
// collections remain the complete inventory; this view is deduplicated and
// ranked for the planner. Every candidate still carries an exact source span.
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

// SemanticProductionCandidate is one deduplicated, ranked composition unit.
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
	Queries          []generationcontract.SemanticSearchQuery
	RequiresMedia    bool
	DedupKey         string
	CoveredFactIDs   []string
	Rationale        []string
}

// SemanticConsolidatedFact is a canonical fact after duplicate mentions are
// merged. Representation is nil exactly when UncoveredReason is non-empty.
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
