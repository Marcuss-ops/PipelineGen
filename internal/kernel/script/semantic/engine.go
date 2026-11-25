package semantic

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

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
// sourceText. Kind is NUMBER, PERCENT, MONEY or FRACTION; Value carries the
// strict parse when HasValue is set (a scaled quantity such as "2 million"
// ships HasValue=false instead of a guessed value).
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
	SchemaVersion          string
	Language               string
	Fingerprint            string
	Analyzers              SemanticAnalyzerVersions
	Status                 string
	Reason                 string
	RejectedItems          int
	Entities               []SemanticEntityCandidate
	EntityFacts            []SemanticEntityFact
	Identities             []SemanticIdentity
	Relations              SemanticRelationView
	Bullets                []BulletCandidate
	ShortPhrases           []SemanticTextCandidate
	LongPhrases            []SemanticTextCandidate
	Quotes                 []SemanticTextCandidate
	ImportantWords         []SemanticTextCandidate
	ImportantSegments      []SemanticImportantSegment
	ComposedImageQueries   []SemanticComposedImageQuery
	ContextualImageQueries []SemanticContextualImageQuery
	Charts                 []ChartValueCandidate
	Temporal               []TemporalFactCandidate
	Numeric                []NumericFactCandidate
	Quantities             []SemanticQuantity
	QuantityComparisons    []SemanticQuantityComparison
	Places                 []SemanticPlace
	Routes                 []SemanticRoute
	CrossSceneRoutes       []SemanticCrossSceneRoute
	PersonRoles            []SemanticPersonRole
	AgeFacts               []SemanticPersonAgeFact
	PersonRelations        []SemanticPersonRelation
	Events                 []SemanticEventFrame
	Discourse              *SemanticDiscourseProjection
	Proposals              []SemanticVisualProposal
	Groups                 []SemanticGroup
	MicroSummaries         []SemanticMicroSummary
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
	// Whole-script views are shared and read-only, like Consolidation.
	StoryGraph     *SemanticStoryGraph
	VisualCoverage *SemanticVisualCoverage
}

// SemanticImportantSegment is a complete, source-grounded passage. Sentence
// indices are inclusive and evidence must match the exact UTF-8 source span.
type SemanticImportantSegment struct {
	ID, Kind, Evidence                  string
	SentenceStart, SentenceEnd          int
	Start, End                          int
	Score                               float64
	VisualMentions, MeasurementMentions int
}

// SemanticQueryComponent is one source-grounded part of a composed image query.
type SemanticQueryComponent struct {
	Kind, ComponentType, Text string
	Start, End                int
}

// SemanticComposedImageQuery keeps the query and the evidence spans that
// justify combining its components.
type SemanticComposedImageQuery struct {
	ID, Kind, Query           string
	Start, End, SentenceIndex int
	Components                []SemanticQueryComponent
}

// SemanticContextualImageQuery is a compact provider query joined from
// source-grounded entities in one sentence. A co-mention is a retrieval cue,
// not an assertion that the entities are related; sentence evidence and
// component spans remain exact source slices.
type SemanticContextualImageQuery struct {
	ID, Kind, Query, Evidence                 string
	EvidenceStart, EvidenceEnd, SentenceIndex int
	Components                                []SemanticQueryComponent
}

type SemanticAnalyzerVersions struct {
	VisualNER       string
	SemanticCore    string
	PhraseImpact    string
	Lexicon         string
	EntityIndex     string
	Numbers         string
	Dates           string
	Quantities      string
	Places          string
	Routes          string
	Roles           string
	AgeFacts        string
	PersonRelations string
	Events          string
	Discourse       string
}

// SemanticQuantity is an exact-source typed measure projected by BrainRust.
type SemanticQuantity struct {
	ID, Dimension, Unit, UnitSurface, Qualifier, RangeID, Evidence, Status string
	Value, RawValue, Scale                                                 *float64
	Period                                                                 *SemanticQuantityPeriod
	SubjectEntityID, Predicate                                             string
	SentenceIndex, Start, End                                              int
}

type SemanticQuantityPeriod struct {
	Year, FactIndex int
}

type SemanticQuantityComparison struct {
	Kind, Left, Right, Dimension, Evidence string
	Ratio, Difference                      *float64
	EvidenceStart, EvidenceEnd             int
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
	NormalizedDate                               string
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
