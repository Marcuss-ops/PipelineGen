// Package generation owns the serializable production-plan DTOs shared by
// the semantic planner, the search scheduler and the overlay binder. The
// planner wrapper and its private revocation state stay in scriptgeneration.
package generation

// SemanticPlannedIdentity is one canonical identity the plan knows about.
// Media reports whether the identity was admitted for an image; the overlay
// planner admits an identity image only when it is true.
type SemanticPlannedIdentity struct {
	ID, Surface, Kind string
	Media             bool
	AssetKey          string
	CandidateIDs      []string
}

// SemanticProductionUnit is one composition the planner may produce.
type SemanticProductionUnit struct {
	CandidateID, Recipe string
	Rank                int
	Priority            float64
	SceneID             string
	SceneIDs            []string
	EntityIDs           []string
	FactIDs             []string
	Title, Evidence     string
	Start, End          int
	AssetKey            string
	RequiresMedia       bool
	Status              string
	DroppedReason       string
	// ReusedFrom is the candidate id whose retrieval serves this asset key.
	ReusedFrom string
	// Queries is the current widening ladder. It is empty for text/data
	// compositions and for asset reuse.
	Queries         []SemanticSearchQuery
	SearchStatus    string
	Rung            int
	ResolvedAssetID string
	Retry           *SemanticSearchRetry
	// ResolvedImage is the first hit a satisfied search produced, carried
	// with its content address and locations so the overlay binder can use
	// it without a second provider call. Nil until a search hits.
	ResolvedImage *SemanticResolvedImage
}

// SemanticResolvedImage is the durable projection of a planner hit. SHA256 is
// the admission key: without verified bytes a hit stays a reference and never
// becomes a rendered overlay asset.
type SemanticResolvedImage struct {
	AssetID, URL, DriveLink, SHA256, MediaType, Provider, Query, Entity string
	VerifierVersion                                                     string
	QueryOrigin                                                         string
	TemplateID                                                          string
	Illustrative                                                        bool
	Width, Height                                                       int
}

// SemanticProductionCoverage accounts for every canonical fact after the run
// budget has been applied.
type SemanticProductionCoverage struct {
	CanonicalFacts int
	CoveredFacts   int
	DeferredFacts  int
	UncoveredFacts int
	Deferred       []SemanticDeferredFact
	Uncovered      []SemanticDeferredFact
}

// SemanticDeferredFact is one canonical fact without an admitted visual and
// the explicit reason why.
type SemanticDeferredFact struct {
	FactID, Reason string
}

// SemanticProductionMetrics are measured planner-level counts, never
// provider measurements.
type SemanticProductionMetrics struct {
	RawCandidates        int
	AdmittedUnits        int
	MediaUnits           int
	ReusedAssets         int
	DroppedBudget        int
	DroppedSceneBudget   int
	PlannedQueries       int
	CanonicalFacts       int
	CoveredFacts         int
	DeferredFacts        int
	UncoveredFacts       int
	SearchAttempts       int
	SearchesSatisfied    int
	SearchesFailed       int
	SearchesSkipped      int
	SearchBroadens       int
	SearchesTooLate      int
	DuplicateQueriesDrop int
}

// SemanticSearchRetry is the planner's instruction after an exhausted ladder:
// `broaden` (widen the BrainRust query) or `skip_visual` (leave the composition
// unproduced).
type SemanticSearchRetry struct {
	Action, Reason string
}

// SemanticSearchQuery is one rung of a production query ladder. Context queries
// retain the sentence and source spans that justify combining their terms.
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

// SemanticSearchQueryComponent is one source-grounded part of a query.
type SemanticSearchQueryComponent struct {
	Kind          string `json:"kind"`
	ComponentType string `json:"component_type"`
	Text          string `json:"text"`
	StartByte     int    `json:"start_byte"`
	EndByte       int    `json:"end_byte"`
}
