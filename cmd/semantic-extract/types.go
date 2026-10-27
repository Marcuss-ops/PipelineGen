package main

const maxInputBytes = 20 << 20

type request struct {
	Operation string  `json:"operation"`
	Language  string  `json:"language"`
	Scenes    []scene `json:"scenes"`
}

type scene struct {
	SceneID string `json:"scene_id"`
	Text    string `json:"text"`
}

type extractionReport struct {
	Schema    string          `json:"schema"`
	Input     inputReport     `json:"input"`
	Engine    engineReport    `json:"engine"`
	Inventory inventoryReport `json:"inventory"`
	Plan      planReport      `json:"production_plan"`
	Scenes    []sceneReport   `json:"scenes"`
}

type inputReport struct {
	Path       string `json:"path"`
	Language   string `json:"language"`
	WordCount  int    `json:"word_count"`
	SceneCount int    `json:"scene_count"`
	SceneWords int    `json:"scene_words_target"`
}

type engineReport struct {
	SchemaVersion string         `json:"schema_version"`
	Fingerprint   string         `json:"fingerprint"`
	Analyzers     analyzerReport `json:"analyzers"`
}

type analyzerReport struct {
	VisualNER    string `json:"visual_ner"`
	SemanticCore string `json:"semantic_core"`
	PhraseImpact string `json:"phrase_impact"`
	Lexicon      string `json:"lexicon"`
}

type inventoryReport struct {
	EntityMentions       int `json:"entity_mentions"`
	UniqueIdentities     int `json:"unique_identities"`
	NamedIdentities      int `json:"named_identities"`
	Proposals            int `json:"proposals"`
	SearchEntries        int `json:"search_entries"`
	QueryCandidates      int `json:"query_candidates"`
	ContextualQueries    int `json:"contextual_queries"`
	ComposedQueries      int `json:"composed_queries"`
	ProductionCandidates int `json:"production_candidates"`
}

type planReport struct {
	Schema             string            `json:"schema"`
	Metrics            planMetricsReport `json:"metrics"`
	SelectedMediaUnits int               `json:"selected_media_units"`
	Reused             int               `json:"reused"`
	DroppedBudget      int               `json:"dropped_budget"`
	DroppedSceneBudget int               `json:"dropped_scene_budget"`
}

type planMetricsReport struct {
	RawCandidates           int `json:"raw_candidates"`
	AdmittedUnits           int `json:"admitted_units"`
	MediaUnits              int `json:"media_units"`
	ReusedAssets            int `json:"reused_assets"`
	DroppedBudget           int `json:"dropped_budget"`
	DroppedSceneBudget      int `json:"dropped_scene_budget"`
	PlannedQueries          int `json:"planned_queries"`
	DuplicateQueriesDropped int `json:"duplicate_queries_dropped"`
}

type sceneReport struct {
	SceneID           string         `json:"scene_id"`
	Text              string         `json:"text"`
	Status            string         `json:"status"`
	Entities          []entityReport `json:"entities"`
	ContextualQueries []queryReport  `json:"contextual_image_queries"`
	ComposedQueries   []queryReport  `json:"composed_image_queries"`
	ProductionUnits   []unitReport   `json:"production_units"`
}

type entityReport struct {
	ID       string  `json:"id"`
	Text     string  `json:"text"`
	Kind     string  `json:"kind"`
	Score    float64 `json:"score"`
	Evidence string  `json:"evidence"`
	Start    int     `json:"start_byte"`
	End      int     `json:"end_byte"`
}

type queryReport struct {
	ID         string            `json:"id"`
	Query      string            `json:"query"`
	Evidence   string            `json:"source_evidence"`
	Components []componentReport `json:"components"`
}

type componentReport struct {
	Kind  string `json:"kind"`
	Type  string `json:"type"`
	Text  string `json:"text"`
	Start int    `json:"start_byte"`
	End   int    `json:"end_byte"`
}

type unitReport struct {
	CandidateID     string        `json:"candidate_id"`
	Recipe          string        `json:"recipe"`
	Rank            int           `json:"rank"`
	Priority        float64       `json:"priority"`
	Title           string        `json:"title"`
	Evidence        string        `json:"evidence"`
	Status          string        `json:"status"`
	SearchStatus    string        `json:"search_status"`
	RequiresMedia   bool          `json:"requires_media"`
	RejectionReason string        `json:"rejection_reason,omitempty"`
	Queries         []queryReport `json:"queries"`
}
