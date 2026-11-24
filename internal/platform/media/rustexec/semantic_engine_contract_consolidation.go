package rustexec

import "encoding/json"

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
	ID              string                    `json:"id"`
	Recipe          string                    `json:"recipe"`
	Rank            int                       `json:"rank"`
	Priority        float64                   `json:"priority"`
	SceneID         string                    `json:"scene_id"`
	SceneIDs        []string                  `json:"scene_ids"`
	EntityIDs       []string                  `json:"entity_ids"`
	FactIDs         []string                  `json:"fact_ids"`
	GroupIDs        []string                  `json:"group_ids"`
	Title           string                    `json:"title"`
	Evidence        string                    `json:"evidence"`
	StartByte       int                       `json:"start_byte"`
	EndByte         int                       `json:"end_byte"`
	SuggestedVisual string                    `json:"suggested_visual"`
	ChartItems      []semanticEngineChartItem `json:"chart_items"`
	ChartMeta       *semanticEngineChartMeta  `json:"chart_meta,omitempty"`
	// VisualPayload is the TICKET-45/46 recipe payload ("visual_recipe.v1").
	// It is forwarded verbatim to overlay-compiler; this layer never reads it.
	VisualPayload    json.RawMessage       `json:"visual_payload,omitempty"`
	TemporalSurfaces []string              `json:"temporal_surfaces"`
	AssetKey         string                `json:"asset_key"`
	Queries          []semanticEngineQuery `json:"queries"`
	RequiresMedia    bool                  `json:"requires_media"`
	DedupKey         string                `json:"dedup_key"`
	CoveredFactIDs   []string              `json:"covered_fact_ids"`
	Rationale        []string              `json:"rationale"`
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
