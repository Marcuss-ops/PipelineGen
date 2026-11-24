package rustexec

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
	ChartMeta              *semanticEngineChartMeta  `json:"chart_meta,omitempty"`
	TemporalSurfaces       []string                  `json:"temporal_surfaces"`
	Evidence               string                    `json:"evidence"`
	StartByte              int                       `json:"start_byte"`
	EndByte                int                       `json:"end_byte"`
}

// semanticEngineChartMeta mirrors semantic-core::manifest::ChartMetadata: the
// grounded metric/unit/dimension/period that downstream renderers use without
// re-parsing the evidence text.
type semanticEngineChartMeta struct {
	Metric     string `json:"metric,omitempty"`
	Unit       string `json:"unit,omitempty"`
	Dimension  string `json:"dimension,omitempty"`
	Period     string `json:"period,omitempty"`
	SeriesType string `json:"series_type,omitempty"`
}

type semanticEngineChartItem struct {
	Label     string  `json:"label"`
	Text      string  `json:"text"`
	Kind      string  `json:"kind"`
	Value     float64 `json:"value"`
	StartByte int     `json:"start_byte"`
	EndByte   int     `json:"end_byte"`
	// Qualifier/Period/Lower/Upper/RangeID mirror the additive
	// semantic-core::manifest::ChartItem fields (ChartsPack.hpp cues). Rust
	// omits them when absent, so they stay pointers: a nil value here means
	// "not asserted", never "zero".
	Qualifier *string  `json:"qualifier,omitempty"`
	Period    *string  `json:"period,omitempty"`
	Lower     *float64 `json:"lower,omitempty"`
	Upper     *float64 `json:"upper,omitempty"`
	RangeID   *string  `json:"range_id,omitempty"`
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
