package rustexec

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	semantic "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/semantic"
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

// The wire structs mirror semantic-core::manifest. v3 is the default shape;
// v4 moves the identity registry to the document-level `identities` table and
// leaves each scene with `identity_ids` references. Source offsets are
// re-verified before a candidate is exposed to planning.
type semanticEngineManifest struct {
	SchemaVersion string `json:"schema_version"`
	// Identities is the normalized v4 document table. v3 leaves it nil; v4
	// carries every CanonicalIdentity once, in registration order.
	Identities          []semanticEngineIdentity           `json:"identities,omitempty"`
	Language            string                             `json:"language"`
	Fingerprint         string                             `json:"fingerprint"`
	Discourse           *semanticEngineDiscourseProjection `json:"discourse,omitempty"`
	Analyzers           semanticEngineAnalyzerVersions     `json:"analyzers"`
	Scenes              []semanticEngineSceneManifest      `json:"scenes"`
	CrossSceneRoutes    []semanticEngineCrossSceneRoute    `json:"cross_scene_routes,omitempty"`
	Events              []semanticEngineEventFrame         `json:"events,omitempty"`
	Entities            []semanticEngineLinkedEntity       `json:"entities"`
	StoryGraph          *semantic.SemanticStoryGraph       `json:"story_graph,omitempty"`
	VisualCoverage      *semantic.SemanticVisualCoverage   `json:"visual_coverage,omitempty"`
	VisualConsolidation *semanticEngineConsolidation       `json:"visual_consolidation,omitempty"`
	SearchFeedback      *semanticEngineSearchFeedback      `json:"search_feedback,omitempty"`
	Editorial           *semanticEngineEditorialResult     `json:"editorial,omitempty"`
}

type semanticEngineLinkedEntity struct {
	LocalID             string                           `json:"local_id"`
	EditorialSurface    string                           `json:"editorial_surface"`
	EditorialType       string                           `json:"editorial_type"`
	PromptEcho          bool                             `json:"prompt_echo"`
	VisualAdmission     bool                             `json:"visual_admission"`
	SurfaceKey          string                           `json:"surface_key"`
	CanonicalSurface    string                           `json:"canonical_surface"`
	CanonicalID         *string                          `json:"canonical_id"`
	LinkStatus          string                           `json:"link_status"`
	CanonicalName       string                           `json:"canonical_name"`
	EntityType          string                           `json:"entity_type"`
	Role                string                           `json:"role"`
	DisambiguationGloss *string                          `json:"disambiguation_gloss"`
	NegativeConstraints []string                         `json:"negative_constraints"`
	Mentions            []semanticEngineMention          `json:"mentions"`
	SearchIntents       []semanticEngineSearchIntent     `json:"search_intents"`
	LinkCandidates      []semantic.SemanticLinkCandidate `json:"link_candidates"`
	LinkMargin          *int                             `json:"link_margin"`
	LinkRule            string                           `json:"link_rule"`
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
