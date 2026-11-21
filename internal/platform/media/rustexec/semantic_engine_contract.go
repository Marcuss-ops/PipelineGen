package rustexec

import (
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// semanticEngineRequest is the NDJSON request shape of the engine.
type semanticEngineRequest struct {
	Operation string                   `json:"operation"`
	Language  string                   `json:"language"`
	Scenes    []semanticEngineScene    `json:"scenes"`
	Editorial *semanticEngineEditorial `json:"editorial,omitempty"`
}

type semanticEngineEditorial struct {
	Request        map[string]any                 `json:"request"`
	Scenes         []scriptpkg.SceneAnalysisInput `json:"scenes"`
	ChapterOptions map[string]any                 `json:"chapter_options"`
}

type semanticEngineScene struct {
	SceneID string `json:"scene_id"`
	Text    string `json:"text"`
}

type semanticEngineResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// ErrorCode is the worker's machine-stable failure discriminant
	// (decode, unsupported_operation, execution). It is additive: older
	// workers omit it and Error keeps the human-readable cause those callers
	// already report. Pinned by
	// TestSemanticEngineRealBinaryErrorCodeSurvivesTheWire.
	ErrorCode string `json:"error_code,omitempty"`
	// Cached reports that the worker served this analysis from its persistent
	// manifest cache (SEMANTIC_CACHE_DIR). It is additive: workers without the
	// cache omit it and decode as false.
	Cached   bool                    `json:"cached,omitempty"`
	Manifest *semanticEngineManifest `json:"manifest,omitempty"`
}

// The wire structs mirror semantic-core::manifest.v3. Source offsets are
// re-verified before a candidate is exposed to planning.
type semanticEngineManifest struct {
	SchemaVersion       string                             `json:"schema_version"`
	Language            string                             `json:"language"`
	Fingerprint         string                             `json:"fingerprint"`
	Discourse           *semanticEngineDiscourseProjection `json:"discourse,omitempty"`
	Analyzers           semanticEngineAnalyzerVersions     `json:"analyzers"`
	Scenes              []semanticEngineSceneManifest      `json:"scenes"`
	CrossSceneRoutes    []semanticEngineCrossSceneRoute    `json:"cross_scene_routes,omitempty"`
	Events              []semanticEngineEventFrame         `json:"events,omitempty"`
	Entities            []semanticEngineLinkedEntity       `json:"entities"`
	StoryGraph          *scriptgen.SemanticStoryGraph      `json:"story_graph,omitempty"`
	VisualCoverage      *scriptgen.SemanticVisualCoverage  `json:"visual_coverage,omitempty"`
	VisualConsolidation *semanticEngineConsolidation       `json:"visual_consolidation,omitempty"`
	SearchFeedback      *semanticEngineSearchFeedback      `json:"search_feedback,omitempty"`
	Editorial           *semanticEngineEditorialResult     `json:"editorial,omitempty"`
}

// semanticEngineSearchFeedback mirrors semantic-core::manifest::SearchFeedback.
// The engine only ever writes `pending` entries; the planner owns every later
// status.
type semanticEngineSearchFeedback struct {
	Entries []semanticEngineSearchFeedbackEntry `json:"entries"`
}

type semanticEngineDiscourseProjection struct {
	Schema          string                             `json:"schema"`
	References      []semanticEngineDiscourseReference `json:"references"`
	Threads         []semanticEngineEntityThread       `json:"threads"`
	SceneContinuity []semanticEngineSceneContinuity    `json:"scene_continuity"`
	Counts          semanticEngineDiscourseCounts      `json:"counts"`
}

type semanticEngineDiscourseReference struct {
	ID                 string   `json:"id"`
	SceneID            string   `json:"scene_id"`
	SentenceIndex      int      `json:"sentence_index"`
	Mention            string   `json:"mention"`
	Type               string   `json:"type"`
	Status             string   `json:"status"`
	TargetEntityIDs    []string `json:"target_entity_ids"`
	CandidateEntityIDs []string `json:"candidate_entity_ids"`
	ResolutionBasis    *string  `json:"resolution_basis"`
	Reason             *string  `json:"reason"`
	Evidence           string   `json:"evidence"`
	StartByte          int      `json:"start_byte"`
	EndByte            int      `json:"end_byte"`
	SentenceStartByte  int      `json:"sentence_start_byte"`
	SentenceEndByte    int      `json:"sentence_end_byte"`
}

type semanticEngineThreadBeat struct {
	ReferenceID   string `json:"reference_id"`
	SceneID       string `json:"scene_id"`
	SentenceIndex int    `json:"sentence_index"`
	State         string `json:"state"`
}

type semanticEngineEntityThread struct {
	EntityID         string                     `json:"entity_id"`
	CanonicalSurface string                     `json:"canonical_surface"`
	Type             string                     `json:"type"`
	SceneIDs         []string                   `json:"scene_ids"`
	FirstSceneID     string                     `json:"first_scene_id"`
	LastSceneID      string                     `json:"last_scene_id"`
	MentionCount     int                        `json:"mention_count"`
	ReturnCount      int                        `json:"return_count"`
	Beats            []semanticEngineThreadBeat `json:"beats"`
}

type semanticEngineSceneContinuity struct {
	SceneID          string   `json:"scene_id"`
	Status           string   `json:"status"`
	Score            float64  `json:"score"`
	ActiveEntityIDs  []string `json:"active_entity_ids"`
	SharedEntityIDs  []string `json:"shared_entity_ids"`
	EnteredEntityIDs []string `json:"entered_entity_ids"`
	ExitedEntityIDs  []string `json:"exited_entity_ids"`
}

type semanticEngineDiscourseCounts struct {
	Total             int `json:"total"`
	Resolved          int `json:"resolved"`
	Ambiguous         int `json:"ambiguous"`
	Unresolved        int `json:"unresolved"`
	Threads           int `json:"threads"`
	CrossSceneThreads int `json:"cross_scene_threads"`
}

type semanticEngineSearchFeedbackEntry struct {
	ProposalID string                     `json:"proposal_id"`
	SceneID    string                     `json:"scene_id"`
	Queries    []semanticEngineQuery      `json:"queries"`
	Status     string                     `json:"status"`
	Retry      *semanticEngineSearchRetry `json:"retry,omitempty"`
}

type semanticEngineSearchRetry struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// semanticEngineConsolidation mirrors semantic-core::manifest::VisualConsolidation.
// It is the selective production view over the complete per-scene inventory.
type semanticEngineConsolidation struct {
	Schema               string                                  `json:"schema"`
	Inventory            semanticEngineConsolidationInventory    `json:"inventory"`
	ProductionCandidates []semanticEngineProductionCandidate     `json:"production_candidates"`
	Facts                []semanticEngineConsolidatedFact        `json:"facts"`
	Dates                []semanticEngineConsolidatedDate        `json:"dates"`
	ReferenceNumbers     []semanticEngineReferenceNumber         `json:"reference_numbers"`
	EventLinks           []semanticEngineEventLink               `json:"event_links"`
	Scenes               []semanticEngineConsolidationSceneStats `json:"scenes"`
	Metrics              semanticEngineConsolidationMetrics      `json:"metrics"`
}

type semanticEngineConsolidationInventory struct {
	EntityMentions          int `json:"entity_mentions"`
	UniqueIdentities        int `json:"unique_identities"`
	NamedIdentities         int `json:"named_identities"`
	Facts                   int `json:"facts"`
	CanonicalFacts          int `json:"canonical_facts"`
	Proposals               int `json:"proposals"`
	SearchEntries           int `json:"search_entries"`
	Queries                 int `json:"queries"`
	TemporalFacts           int `json:"temporal_facts"`
	CanonicalDates          int `json:"canonical_dates"`
	MicroSummaries          int `json:"micro_summaries"`
	CanonicalMicroSummaries int `json:"canonical_micro_summaries"`
	Groups                  int `json:"groups"`
	Timelines               int `json:"timelines"`
}

type semanticEngineProductionCandidate struct {
	ID               string                    `json:"id"`
	Recipe           string                    `json:"recipe"`
	Rank             int                       `json:"rank"`
	Priority         float64                   `json:"priority"`
	SceneID          string                    `json:"scene_id"`
	SceneIDs         []string                  `json:"scene_ids"`
	EntityIDs        []string                  `json:"entity_ids"`
	FactIDs          []string                  `json:"fact_ids"`
	GroupIDs         []string                  `json:"group_ids"`
	Title            string                    `json:"title"`
	Evidence         string                    `json:"evidence"`
	StartByte        int                       `json:"start_byte"`
	EndByte          int                       `json:"end_byte"`
	SuggestedVisual  string                    `json:"suggested_visual"`
	ChartItems       []semanticEngineChartItem `json:"chart_items"`
	TemporalSurfaces []string                  `json:"temporal_surfaces"`
	AssetKey         string                    `json:"asset_key"`
	Queries          []semanticEngineQuery     `json:"queries"`
	RequiresMedia    bool                      `json:"requires_media"`
	DedupKey         string                    `json:"dedup_key"`
	CoveredFactIDs   []string                  `json:"covered_fact_ids"`
	Rationale        []string                  `json:"rationale"`
}

type semanticEngineQuery struct {
	Kind              string                         `json:"kind"`
	Text              string                         `json:"text"`
	Origin            string                         `json:"origin"`
	Illustrative      bool                           `json:"illustrative"`
	Evidence          *string                        `json:"evidence,omitempty"`
	EvidenceStartByte *int                           `json:"evidence_start_byte,omitempty"`
	EvidenceEndByte   *int                           `json:"evidence_end_byte,omitempty"`
	Components        []semanticEngineQueryComponent `json:"components,omitempty"`
	TemplateID        *string                        `json:"template_id,omitempty"`
	DerivedTerms      []string                       `json:"derived_terms,omitempty"`
}

type semanticEngineQueryComponent struct {
	Kind          string `json:"kind"`
	ComponentType string `json:"component_type"`
	Text          string `json:"text"`
	StartByte     int    `json:"start_byte"`
	EndByte       int    `json:"end_byte"`
}

type semanticEngineConsolidatedFact struct {
	ID                string                        `json:"id"`
	FactKey           string                        `json:"fact_key"`
	FactIDs           []string                      `json:"fact_ids"`
	SubjectEntityID   string                        `json:"subject_entity_id"`
	Predicate         string                        `json:"predicate"`
	NormalizedValue   *string                       `json:"normalized_value"`
	NormalizedDate    *string                       `json:"normalized_date"`
	Status            string                        `json:"status"`
	Importance        float64                       `json:"importance"`
	SceneIDs          []string                      `json:"scene_ids"`
	SentenceIndex     int                           `json:"sentence_index"`
	Evidence          string                        `json:"evidence"`
	StartByte         int                           `json:"start_byte"`
	EndByte           int                           `json:"end_byte"`
	EvidenceStartByte int                           `json:"evidence_start_byte"`
	EvidenceEndByte   int                           `json:"evidence_end_byte"`
	Representation    *semanticEngineRepresentation `json:"representation"`
	UncoveredReason   *string                       `json:"uncovered_reason"`
}

type semanticEngineRepresentation struct {
	CandidateID string `json:"candidate_id"`
	Recipe      string `json:"recipe"`
}

type semanticEngineConsolidatedDate struct {
	NormalizedDate string   `json:"normalized_date"`
	Count          int      `json:"count"`
	Surfaces       []string `json:"surfaces"`
	Spans          []struct {
		SceneID   string `json:"scene_id"`
		Surface   string `json:"surface"`
		StartByte int    `json:"start_byte"`
		EndByte   int    `json:"end_byte"`
	} `json:"spans"`
}

type semanticEngineReferenceNumber struct {
	SceneID   string `json:"scene_id"`
	Surface   string `json:"surface"`
	Reason    string `json:"reason"`
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
}

type semanticEngineEventLink struct {
	ID              string `json:"id"`
	GroupID         string `json:"group_id"`
	SceneID         string `json:"scene_id"`
	FromCandidateID string `json:"from_candidate_id"`
	ToCandidateID   string `json:"to_candidate_id"`
	Kind            string `json:"kind"`
	Evidence        string `json:"evidence"`
	StartByte       int    `json:"start_byte"`
	EndByte         int    `json:"end_byte"`
}

type semanticEngineConsolidationSceneStats struct {
	SceneID              string `json:"scene_id"`
	RawProposals         int    `json:"raw_proposals"`
	ProductionCandidates int    `json:"production_candidates"`
	RawQueries           int    `json:"raw_queries"`
	CanonicalFacts       int    `json:"canonical_facts"`
	CoveredFacts         int    `json:"covered_facts"`
	TimelineCandidates   int    `json:"timeline_candidates"`
}

type semanticEngineConsolidationMetrics struct {
	RawProposals               int `json:"raw_proposals"`
	ProductionCandidates       int `json:"production_candidates"`
	RawSearchEntries           int `json:"raw_search_entries"`
	ProductionSearchCandidates int `json:"production_search_candidates"`
	RawQueries                 int `json:"raw_queries"`
	ProductionQueries          int `json:"production_queries"`
	DuplicateEntityMentions    int `json:"duplicate_entity_mentions_collapsed"`
	DuplicateFactsMerged       int `json:"duplicate_facts_merged"`
	DuplicateQueriesRemoved    int `json:"duplicate_queries_removed"`
	DuplicateProposalsRemoved  int `json:"duplicate_proposals_removed"`
	EstimatedSearchJobs        int `json:"estimated_search_jobs"`
	SharedAssetReuses          int `json:"shared_asset_reuses"`
	CanonicalFacts             int `json:"canonical_facts"`
	CoveredFacts               int `json:"covered_facts"`
	UncoveredFacts             int `json:"uncovered_facts"`
	ReferenceNumbers           int `json:"reference_numbers"`
}

type semanticEngineEditorialResult struct {
	Summary         string                           `json:"summary"`
	BulletPoints    []scriptpkg.EditorialBulletPoint `json:"bullet_points"`
	HeavySentences  []scriptpkg.ImportantSentence    `json:"heavy_sentences"`
	ChapterManifest scriptpkg.ChapterManifest        `json:"chapter_manifest"`
	SceneHighlights []scriptpkg.SceneHighlight       `json:"scene_highlights"`
	Timings         scriptpkg.PhraseImpactTimings    `json:"timings"`
}

type semanticEngineSplitResponse struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Sentences []struct {
		Text      string `json:"text"`
		StartByte int    `json:"start_byte"`
		EndByte   int    `json:"end_byte"`
	} `json:"sentences"`
}

type semanticEngineAnalyzerVersions struct {
	VisualNER       string `json:"visualner"`
	SemanticCore    string `json:"semantic_core"`
	PhraseImpact    string `json:"phrase_impact"`
	Lexicon         string `json:"lexicon"`
	EntityIndex     string `json:"entity_index,omitempty"`
	Linker          string `json:"linker,omitempty"`
	Numbers         string `json:"numbers,omitempty"`
	Dates           string `json:"dates,omitempty"`
	Quantities      string `json:"quantities,omitempty"`
	Places          string `json:"places,omitempty"`
	Routes          string `json:"routes,omitempty"`
	Roles           string `json:"roles,omitempty"`
	AgeFacts        string `json:"age_facts,omitempty"`
	PersonRelations string `json:"person_relations,omitempty"`
	Events          string `json:"events,omitempty"`
	Discourse       string `json:"discourse,omitempty"`
}

type semanticEngineLinkedEntity struct {
	LocalID             string                            `json:"local_id"`
	EditorialSurface    string                            `json:"editorial_surface"`
	EditorialType       string                            `json:"editorial_type"`
	PromptEcho          bool                              `json:"prompt_echo"`
	VisualAdmission     bool                              `json:"visual_admission"`
	SurfaceKey          string                            `json:"surface_key"`
	CanonicalSurface    string                            `json:"canonical_surface"`
	CanonicalID         *string                           `json:"canonical_id"`
	LinkStatus          string                            `json:"link_status"`
	CanonicalName       string                            `json:"canonical_name"`
	EntityType          string                            `json:"entity_type"`
	Role                string                            `json:"role"`
	DisambiguationGloss *string                           `json:"disambiguation_gloss"`
	NegativeConstraints []string                          `json:"negative_constraints"`
	Mentions            []semanticEngineMention           `json:"mentions"`
	SearchIntents       []semanticEngineSearchIntent      `json:"search_intents"`
	LinkCandidates      []scriptgen.SemanticLinkCandidate `json:"link_candidates"`
	LinkMargin          *int                              `json:"link_margin"`
	LinkRule            string                            `json:"link_rule"`
}

type semanticEngineSearchIntent struct {
	Kind  string `json:"kind"`
	Query string `json:"query"`
	Lang  string `json:"lang"`
}

type semanticEngineSceneManifest struct {
	SceneID                string                               `json:"scene_id"`
	Status                 string                               `json:"status"`
	Reason                 *string                              `json:"reason"`
	RejectedItems          int                                  `json:"rejected_items"`
	Entities               []semanticEngineCandidate            `json:"entities"`
	EntityFacts            []semanticEngineEntityFact           `json:"entity_facts"`
	Identities             []semanticEngineIdentity             `json:"identities"`
	Relations              semanticEngineRelationView           `json:"relations"`
	NumericFacts           []semanticEngineNumeric              `json:"numeric_facts"`
	Quantities             []semanticEngineQuantity             `json:"quantities,omitempty"`
	QuantityComparisons    []semanticEngineQuantityComparison   `json:"quantity_comparisons,omitempty"`
	Places                 []semanticEnginePlace                `json:"places,omitempty"`
	Routes                 []semanticEngineRoute                `json:"routes,omitempty"`
	PersonRoles            []semanticEnginePersonRole           `json:"person_roles,omitempty"`
	AgeFacts               []semanticEnginePersonAgeFact        `json:"age_facts,omitempty"`
	PersonRelations        []semanticEnginePersonRelation       `json:"person_relations,omitempty"`
	TemporalFacts          []semanticEngineTemporal             `json:"temporal_facts"`
	Bullets                []semanticEngineCandidate            `json:"bullets"`
	ShortPhrases           []semanticEngineCandidate            `json:"short_phrases"`
	LongPhrases            []semanticEngineCandidate            `json:"long_phrases"`
	Quotes                 []semanticEngineCandidate            `json:"quotes"`
	ImportantWords         []semanticEngineCandidate            `json:"important_words"`
	ImportantSegments      []semanticEngineImportantSegment     `json:"important_segments"`
	ComposedImageQueries   []semanticEngineComposedImageQuery   `json:"composed_image_queries"`
	ContextualImageQueries []semanticEngineContextualImageQuery `json:"contextual_image_queries"`
	VisualProposals        []semanticEngineProposal             `json:"visual_proposals"`
	SemanticGroups         []semanticEngineGroup                `json:"semantic_groups"`
	MicroSummaries         []semanticEngineMicroSummary         `json:"micro_summaries"`
}

type semanticEnginePersonRole struct {
	ID             string                  `json:"id"`
	PersonID       string                  `json:"person_id"`
	RoleCode       string                  `json:"role_code"`
	RoleSurface    string                  `json:"role_surface"`
	Gender         string                  `json:"gender"`
	Rank           string                  `json:"rank"`
	OrganizationID *string                 `json:"organization_id"`
	PlaceID        *string                 `json:"place_id"`
	Temporal       *semanticEngineTemporal `json:"temporal"`
	Tense          string                  `json:"tense"`
	Status         string                  `json:"status"`
	StartByte      int                     `json:"start_byte"`
	EndByte        int                     `json:"end_byte"`
	SentenceIndex  int                     `json:"sentence_index"`
	Evidence       string                  `json:"evidence"`
}

type semanticEnginePersonAgeFact struct {
	PersonID         string  `json:"person_id"`
	Age              int     `json:"age"`
	AsOf             *string `json:"as_of"`
	DerivedBirthYear *int    `json:"derived_birth_year"`
	StartByte        int     `json:"start_byte"`
	EndByte          int     `json:"end_byte"`
	SentenceIndex    int     `json:"sentence_index"`
	Evidence         string  `json:"evidence"`
}

type semanticEnginePersonRelation struct {
	FromID        string `json:"from_id"`
	ToID          string `json:"to_id"`
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	StartByte     int    `json:"start_byte"`
	EndByte       int    `json:"end_byte"`
	SentenceIndex int    `json:"sentence_index"`
	Evidence      string `json:"evidence"`
}

type semanticEngineEventParticipant struct {
	Role     string `json:"role"`
	EntityID string `json:"entity_id"`
}

type semanticEngineEventEvidence struct {
	SceneID   string `json:"scene_id"`
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	Text      string `json:"text"`
}

type semanticEngineEventTime struct {
	FactIndex      int     `json:"fact_index"`
	NormalizedDate *string `json:"normalized_date"`
	Derived        bool    `json:"derived"`
}

type semanticEngineEventFrame struct {
	ID               string                           `json:"id"`
	EventType        string                           `json:"event_type"`
	Trigger          string                           `json:"trigger"`
	TriggerStartByte int                              `json:"trigger_start_byte"`
	TriggerEndByte   int                              `json:"trigger_end_byte"`
	Participants     []semanticEngineEventParticipant `json:"participants"`
	PlaceID          *string                          `json:"place_id"`
	Time             *semanticEngineEventTime         `json:"time"`
	Status           string                           `json:"status"`
	SceneIDs         []string                         `json:"scene_ids"`
	Evidence         []semanticEngineEventEvidence    `json:"evidence"`
	ConfidenceBasis  []string                         `json:"confidence_basis"`
	SourceFactIDs    []string                         `json:"source_fact_ids"`
}

type semanticEnginePlace struct {
	ID            string   `json:"id"`
	EntityLocalID string   `json:"entity_local_id"`
	KBID          *string  `json:"kb_id"`
	LinkStatus    string   `json:"link_status"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	Lat           *float64 `json:"lat"`
	Lon           *float64 `json:"lon"`
	ParentIDs     []string `json:"parent_ids"`
	Role          string   `json:"role"`
	StartByte     int      `json:"start_byte"`
	EndByte       int      `json:"end_byte"`
	SentenceIndex int      `json:"sentence_index"`
	Evidence      string   `json:"evidence"`
}

type semanticEngineRouteStop struct {
	PlaceID string `json:"place_id"`
	Role    string `json:"role"`
}

type semanticEngineRoute struct {
	ID             string                    `json:"id"`
	Mode           string                    `json:"mode"`
	SubjectID      *string                   `json:"subject_id"`
	Stops          []semanticEngineRouteStop `json:"stops"`
	StraightLineKM *float64                  `json:"straight_line_km"`
	Status         string                    `json:"status"`
	StartByte      int                       `json:"start_byte"`
	EndByte        int                       `json:"end_byte"`
	Evidence       string                    `json:"evidence"`
}

type semanticEngineCrossSceneRouteStop struct {
	SceneID string `json:"scene_id"`
	PlaceID string `json:"place_id"`
	Role    string `json:"role"`
}

type semanticEngineRouteEvidence struct {
	SceneID   string `json:"scene_id"`
	RouteID   string `json:"route_id"`
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	Evidence  string `json:"evidence"`
}

type semanticEngineCrossSceneRoute struct {
	ID             string                              `json:"id"`
	SubjectID      string                              `json:"subject_id"`
	Mode           string                              `json:"mode"`
	Stops          []semanticEngineCrossSceneRouteStop `json:"stops"`
	StraightLineKM *float64                            `json:"straight_line_km"`
	Status         string                              `json:"status"`
	Evidence       []semanticEngineRouteEvidence       `json:"evidence"`
}

type semanticEngineImportantSegment struct {
	ID                  string  `json:"id"`
	Kind                string  `json:"kind"`
	SentenceStart       int     `json:"sentence_index_start"`
	SentenceEnd         int     `json:"sentence_index_end"`
	Evidence            string  `json:"evidence"`
	StartByte           int     `json:"start_byte"`
	EndByte             int     `json:"end_byte"`
	Score               float64 `json:"score"`
	VisualMentions      int     `json:"visual_mentions"`
	MeasurementMentions int     `json:"measurement_mentions"`
}

type semanticEngineComposedImageQuery struct {
	ID            string                         `json:"id"`
	Kind          string                         `json:"kind"`
	Query         string                         `json:"query"`
	StartByte     int                            `json:"start_byte"`
	EndByte       int                            `json:"end_byte"`
	SentenceIndex int                            `json:"sentence_index"`
	Components    []semanticEngineQueryComponent `json:"components"`
}

type semanticEngineContextualImageQuery struct {
	ID                string                         `json:"id"`
	Kind              string                         `json:"kind"`
	Query             string                         `json:"query"`
	Evidence          string                         `json:"evidence"`
	EvidenceStartByte int                            `json:"evidence_start_byte"`
	EvidenceEndByte   int                            `json:"evidence_end_byte"`
	SentenceIndex     int                            `json:"sentence_index"`
	Components        []semanticEngineQueryComponent `json:"components"`
}

// semanticEngineMicroSummary mirrors semantic-core::manifest::MicroSummary.
// StartByte/EndByte locate the verbatim Evidence sentence (not the paraphrased
// Text), so the Go boundary can re-verify the claim's source before planning.
type semanticEngineMicroSummary struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	Text            string   `json:"text"`
	EvidenceFactIDs []string `json:"evidence_fact_ids"`
	SubjectEntityID *string  `json:"subject_entity_id"`
	NormalizedDate  *string  `json:"normalized_date"`
	SentenceIndex   int      `json:"sentence_index"`
	Evidence        string   `json:"evidence"`
	StartByte       int      `json:"start_byte"`
	EndByte         int      `json:"end_byte"`
	Score           float64  `json:"score"`
}

type semanticEngineCandidate struct {
	ID            string  `json:"id"`
	Text          string  `json:"text"`
	Kind          string  `json:"type"`
	StartByte     int     `json:"start_byte"`
	EndByte       int     `json:"end_byte"`
	SentenceIndex int     `json:"sentence_index"`
	Evidence      string  `json:"evidence"`
	Score         float64 `json:"score"`
	Provenance    string  `json:"provenance"`
}

type semanticEngineEntityFact struct {
	ID                string   `json:"id"`
	SubjectEntityID   string   `json:"subject_entity_id"`
	Predicate         string   `json:"predicate"`
	Value             string   `json:"value"`
	ValueType         string   `json:"type"`
	NormalizedValue   *string  `json:"normalized_value"`
	TargetEntityID    *string  `json:"target_entity_id"`
	Unit              *string  `json:"unit"`
	Operator          *string  `json:"operator"`
	NormalizedDate    *string  `json:"normalized_date"`
	Status            string   `json:"status"`
	Importance        float64  `json:"importance"`
	VisualConceptIDs  []string `json:"visual_concept_ids"`
	SentenceIndex     int      `json:"sentence_index"`
	Evidence          string   `json:"evidence"`
	StartByte         int      `json:"start_byte"`
	EndByte           int      `json:"end_byte"`
	EvidenceStartByte int      `json:"evidence_start_byte"`
	EvidenceEndByte   int      `json:"evidence_end_byte"`
}

type semanticEngineIdentity struct {
	ID               string                  `json:"id"`
	CanonicalSurface string                  `json:"canonical_surface"`
	CanonicalID      *string                 `json:"canonical_id,omitempty"`
	Kind             string                  `json:"type"`
	Mentions         []semanticEngineMention `json:"mentions"`
}

type semanticEngineMention struct {
	SceneID   string `json:"scene_id"`
	Surface   string `json:"surface"`
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
}

type semanticEngineRelationView struct {
	CoMentions        []semanticEngineCoMention        `json:"co_mentions"`
	AssertedRelations []semanticEngineAssertedRelation `json:"asserted_relations"`
}

type semanticEngineCoMention struct {
	LeftID        string `json:"left_id"`
	RightID       string `json:"right_id"`
	SentenceIndex int    `json:"sentence_index"`
	Evidence      string `json:"evidence"`
}

type semanticEngineAssertedRelation struct {
	SourceID      string  `json:"source_id"`
	TargetID      string  `json:"target_id"`
	Predicate     *string `json:"predicate"`
	Status        string  `json:"status"`
	SentenceIndex int     `json:"sentence_index"`
	Evidence      string  `json:"evidence"`
}

type semanticEngineGroup struct {
	ID           string                      `json:"id"`
	GroupType    string                      `json:"group_type"`
	Title        string                      `json:"title"`
	RankingBasis *string                     `json:"ranking_basis"`
	TargetCount  *int                        `json:"target_count"`
	Complete     *bool                       `json:"complete"`
	Members      []semanticEngineGroupMember `json:"members"`
}

type semanticEngineGroupMember struct {
	CandidateID     string  `json:"candidate_id"`
	SceneID         string  `json:"scene_id"`
	Rank            *int    `json:"rank"`
	SentenceIndex   int     `json:"sentence_index"`
	Text            string  `json:"text"`
	Evidence        string  `json:"evidence"`
	StartByte       int     `json:"start_byte"`
	EndByte         int     `json:"end_byte"`
	TemporalSurface *string `json:"temporal_surface"`
	NormalizedDate  *string `json:"normalized_date"`
}

type semanticEngineProposal struct {
	ID                     string                    `json:"id"`
	ProposalType           string                    `json:"proposal_type"`
	EntityIDs              []string                  `json:"entity_ids"`
	VisualQuerySuggestions []string                  `json:"visual_query_suggestions"`
	SuggestedVisual        string                    `json:"suggested_visual"`
	Status                 string                    `json:"status"`
	Score                  float64                   `json:"score"`
	ChartItems             []semanticEngineChartItem `json:"chart_items"`
	TemporalSurfaces       []string                  `json:"temporal_surfaces"`
	Evidence               string                    `json:"evidence"`
	StartByte              int                       `json:"start_byte"`
	EndByte                int                       `json:"end_byte"`
}

type semanticEngineChartItem struct {
	Label     string  `json:"label"`
	Text      string  `json:"text"`
	Kind      string  `json:"kind"`
	Value     float64 `json:"value"`
	StartByte int     `json:"start_byte"`
	EndByte   int     `json:"end_byte"`
}

type semanticEngineNumeric struct {
	Surface   string   `json:"surface"`
	Kind      string   `json:"type"`
	Value     *float64 `json:"value"`
	StartByte int      `json:"start_byte"`
	EndByte   int      `json:"end_byte"`
	Evidence  string   `json:"evidence"`
}

type semanticEngineQuantity struct {
	ID              string                        `json:"id"`
	Value           *float64                      `json:"value"`
	RawValue        *float64                      `json:"raw_value"`
	Scale           *float64                      `json:"scale"`
	Dimension       string                        `json:"dimension"`
	Unit            string                        `json:"unit"`
	UnitSurface     string                        `json:"unit_surface"`
	Qualifier       string                        `json:"qualifier"`
	RangeID         *string                       `json:"range_id"`
	Period          *semanticEngineQuantityPeriod `json:"period"`
	SubjectEntityID *string                       `json:"subject_entity_id"`
	Predicate       *string                       `json:"predicate"`
	StartByte       int                           `json:"start_byte"`
	EndByte         int                           `json:"end_byte"`
	Evidence        string                        `json:"evidence"`
	SentenceIndex   int                           `json:"sentence_index"`
	Status          string                        `json:"status"`
}

type semanticEngineQuantityPeriod struct {
	Year      int `json:"year"`
	FactIndex int `json:"fact_index"`
}

type semanticEngineQuantityComparison struct {
	Kind              string   `json:"kind"`
	Left              string   `json:"left"`
	Right             string   `json:"right"`
	Dimension         string   `json:"dimension"`
	Ratio             *float64 `json:"ratio"`
	Difference        *float64 `json:"difference"`
	EvidenceStartByte int      `json:"evidence_start_byte"`
	EvidenceEndByte   int      `json:"evidence_end_byte"`
}

type semanticEngineTemporal struct {
	Surface        string  `json:"surface"`
	Kind           string  `json:"type"`
	NormalizedDate *string `json:"normalized_date"`
	Precision      string  `json:"precision"`
	StartByte      int     `json:"start_byte"`
	EndByte        int     `json:"end_byte"`
	Evidence       string  `json:"evidence"`
	Era            *string `json:"era,omitempty"`
	Ambiguous      bool    `json:"ambiguous,omitempty"`
}
