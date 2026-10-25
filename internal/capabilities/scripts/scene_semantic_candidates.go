package scriptgeneration

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Visual semantics, one worker call per scene (RUST-02) ──
//
// The per-category VisualNER bullet and chart calls were retired: they
// analysed the same scene twice, with different entity limits, and could
// therefore disagree. The unified semantic analyzer performs ONE analyze per
// script batch (SceneSemanticBatchAnalyzer) and returns every grounded
// projection:
//
//	SemanticCandidateBundle
//	 ├── Bullets   — source sentences the planner may rank editorially
//	 ├── Charts    — verified numeric chart candidates
//	 ├── Temporal  — every source-grounded date fact (top-N independent)
//	 └── Numeric   — every source-grounded numeric fact

// TemporalFactCandidate is one source-grounded date fact projected by the
// Rust worker: Text is the exact byte span (Start..End) of sourceText, so
// the planner can anchor the fact to voiceover timing without a second
// parser. NormalizedDate is the ISO form (YYYY-MM-DD / YYYY-MM / YYYY) or
// empty for unresolved surfaces — no ISO value is ever invented. Precision
// is one of day/month/year/unresolved.
type TemporalFactCandidate struct {
	Text           string
	NormalizedDate string
	Precision      string
	Start          int
	End            int
	Ambiguous      bool
	Era            string
}

// NumericFactCandidate is one source-grounded numeric occurrence projected
// by the Rust worker: Text is the exact byte span (Start..End) of
// sourceText. Kind is NUMBER, PERCENT or MONEY; Value carries the strict
// parse when HasValue is set (a scaled quantity such as "2 million" ships
// HasValue=false instead of a guessed value).
type NumericFactCandidate struct {
	Text     string
	Kind     string
	Value    float64
	HasValue bool
	Start    int
	End      int
}

// SemanticCandidateBundle carries the grounded projections from one analyze
// call.
type SemanticCandidateBundle struct {
	SchemaVersion  string
	Language       string
	Fingerprint    string
	Analyzers      SemanticAnalyzerVersions
	Status         string
	Reason         string
	RejectedItems  int
	Entities       []SemanticEntityCandidate
	EntityFacts    []SemanticEntityFact
	Identities     []SemanticIdentity
	Relations      SemanticRelationView
	Bullets        []BulletCandidate
	ShortPhrases   []SemanticTextCandidate
	LongPhrases    []SemanticTextCandidate
	Quotes         []SemanticTextCandidate
	ImportantWords []SemanticTextCandidate
	Charts         []ChartValueCandidate
	Temporal       []TemporalFactCandidate
	Numeric        []NumericFactCandidate
	Proposals      []SemanticVisualProposal
	Groups         []SemanticGroup
	MicroSummaries []SemanticMicroSummary
	// SearchFeedback is the whole-script asset-search handshake: one pending
	// entry per image-backed proposal, carrying the exact-source query ladder.
	// It is repeated on every scene bundle for the same reason as
	// Consolidation and is never rewritten by the engine after extraction.
	SearchFeedback *SemanticSearchFeedback
	// Consolidation is the script-level selective production view. It is
	// repeated on every scene bundle so a caller that already holds one scene
	// does not need a second transport. The pointer is shared; consumers must
	// treat it as read-only.
	Consolidation *SemanticVisualConsolidation
}

type SemanticAnalyzerVersions struct {
	VisualNER    string
	SemanticCore string
	PhraseImpact string
}

type SemanticEntityFact struct {
	ID, SubjectEntityID, Predicate, Value, ValueType, NormalizedValue, TargetEntityID string
	Unit, Operator, NormalizedDate, Status, Evidence                                  string
	SentenceIndex, Start, End, EvidenceStart, EvidenceEnd                             int
	// Importance is the Rust engine's deterministic rule weight in [0, 1].
	// VisualConceptIDs are the canonical identities a visual planner may bind
	// to this fact (subject, and target when the fact links two entities).
	Importance       float64
	VisualConceptIDs []string
}

// SemanticTextCandidate preserves source-grounded phrase projections from the
// engine for future planners without making Phrase Impact a second extractor.
type SemanticTextCandidate struct {
	ID, Text, Kind, Evidence, Provenance string
	SentenceIndex, Start, End            int
	Score                                float64
}

// SemanticEntityCandidate keeps a mention and its canonical identity together.
type SemanticEntityCandidate struct {
	SemanticTextCandidate
	IdentityID string
}

type SemanticIdentityMention struct {
	SceneID, Surface string
	Start, End       int
}

type SemanticIdentity struct {
	ID, CanonicalSurface, Kind string
	Mentions                   []SemanticIdentityMention
}

type SemanticCoMention struct {
	LeftID, RightID, Evidence string
	SentenceIndex             int
}

type SemanticAssertedRelation struct {
	SourceID, TargetID, Evidence, Predicate, Status string
	SentenceIndex                                   int
}

type SemanticRelationView struct {
	CoMentions        []SemanticCoMention
	AssertedRelations []SemanticAssertedRelation
}

type SemanticChartItem struct {
	Label, Text, Kind string
	Value             float64
	Start, End        int
}

type SemanticVisualProposal struct {
	// ID is the engine's stable scene-local proposal id. The search-feedback
	// handshake addresses proposals through it, so it is never re-derived.
	ID                                                  string
	Type, SuggestedVisual, Status, Evidence             string
	EntityIDs, TemporalSurfaces, VisualQuerySuggestions []string
	Score                                               float64
	ChartItems                                          []SemanticChartItem
	Start, End                                          int
}

type SemanticGroupMember struct {
	CandidateID, Text, Evidence, TemporalSurface string
	SceneID                                      string
	Rank                                         *int
	SentenceIndex, Start, End                    int
}

type SemanticGroup struct {
	ID, Type, Title, RankingBasis string
	TargetCount                   *int
	Complete                      *bool
	Members                       []SemanticGroupMember
}

// SemanticMicroSummary is a fact-backed short phrase produced by the Rust
// engine. Text may paraphrase the source, so it is never displayed as a
// transcript-locked overlay: Evidence is the verbatim sentence that carries
// the claim, Start/End locate that sentence inside the scene text, and
// EvidenceFactIDs name the atomic facts that justify every claim. The planner
// uses the evidence span for timing and selection while the summary text stays
// attached as provenance.
type SemanticMicroSummary struct {
	ID              string
	Kind            string
	Text            string
	EvidenceFactIDs []string
	SubjectEntityID string
	NormalizedDate  string
	SentenceIndex   int
	Evidence        string
	Start           int
	End             int
	Score           float64
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
	Queries             []SemanticSearchQuery
	// Status is `pending` while the engine owns the entry; the planner writes
	// `satisfied` or `failed` after retrieval.
	Status string
	// Retry is nil until a search failed.
	Retry *SemanticSearchRetry
}

// SemanticSearchRetry is the planner's instruction after an exhausted ladder:
// `broaden` (widen the query), `drop_entity` (search the remaining name) or
// `skip_visual` (leave the composition unproduced).
type SemanticSearchRetry struct {
	Action, Reason string
}

// SemanticVisualConsolidation is the script-level selective production view
// returned in the same `semantic_manifest.v2` response. Raw per-scene
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
	SuggestedVisual     string
	ChartItems          []SemanticChartItem
	TemporalSurfaces    []string
	AssetKey            string
	Queries             []SemanticSearchQuery
	RequiresMedia       bool
	DedupKey            string
	CoveredFactIDs      []string
	Rationale           []string
}

// SemanticSearchQuery is one rung of a production query ladder.
type SemanticSearchQuery struct {
	Kind, Text string
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

// SceneSemanticInput is one scene in a batch analysis request.
type SemanticSceneInput struct {
	SceneID string
	Text    string
}

// SceneSemanticAnalyzer returns the unified semantic bundle for one scene.
// It remains the portable per-scene contract; production uses the batch
// interface below so a whole script is analysed once.
type SceneSemanticAnalyzer interface {
	SemanticCandidates(ctx context.Context, language, sourceText string, limit int) (*SemanticCandidateBundle, error)
}

// SceneSemanticBatchAnalyzer analyses every scene of a script in ONE worker
// call and returns the bundles keyed by scene id. When a backend implements
// it, the runner never issues a per-scene call.
type SceneSemanticBatchAnalyzer interface {
	SemanticBundles(ctx context.Context, language string, scenes []SemanticSceneInput) (map[string]*SemanticCandidateBundle, error)
}

// SceneSemanticEditorialBatchAnalyzer returns semantic candidates and
// extractive editorial products from the same whole-script worker request.
type SceneSemanticEditorialBatchAnalyzer interface {
	AnalyzeSemanticEditorial(context.Context, string, string, []scriptpkg.SceneAnalysisInput) (map[string]*SemanticCandidateBundle, scriptpkg.PhraseImpactResult, error)
}
