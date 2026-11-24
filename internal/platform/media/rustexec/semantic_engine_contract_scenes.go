package rustexec

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

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
