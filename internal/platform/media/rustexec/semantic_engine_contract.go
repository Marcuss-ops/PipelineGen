package rustexec

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

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
	OK       bool                    `json:"ok"`
	Error    string                  `json:"error,omitempty"`
	Manifest *semanticEngineManifest `json:"manifest,omitempty"`
}

// The wire structs mirror semantic-core::manifest. The complete current V2
// surface is retained by the scripts contract, and every source offset is
// re-verified before a candidate is exposed to planning.
type semanticEngineManifest struct {
	SchemaVersion string                         `json:"schema_version"`
	Language      string                         `json:"language"`
	Fingerprint   string                         `json:"fingerprint"`
	Analyzers     semanticEngineAnalyzerVersions `json:"analyzers"`
	Scenes        []semanticEngineSceneManifest  `json:"scenes"`
	Editorial     *semanticEngineEditorialResult `json:"editorial,omitempty"`
}

type semanticEngineEditorialResult struct {
	Summary      string `json:"summary"`
	BulletPoints []struct {
		Text string `json:"text"`
	} `json:"bullet_points"`
	HeavySentences  []scriptpkg.ImportantSentence `json:"heavy_sentences"`
	ChapterManifest scriptpkg.ChapterManifest     `json:"chapter_manifest"`
	SceneHighlights []scriptpkg.SceneHighlight    `json:"scene_highlights"`
	Timings         scriptpkg.PhraseImpactTimings `json:"timings"`
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
	VisualNER    string `json:"visualner"`
	SemanticCore string `json:"semantic_core"`
	PhraseImpact string `json:"phrase_impact"`
}

type semanticEngineSceneManifest struct {
	SceneID         string                       `json:"scene_id"`
	Status          string                       `json:"status"`
	Reason          *string                      `json:"reason"`
	RejectedItems   int                          `json:"rejected_items"`
	Entities        []semanticEngineCandidate    `json:"entities"`
	EntityFacts     []semanticEngineEntityFact   `json:"entity_facts"`
	Identities      []semanticEngineIdentity     `json:"identities"`
	Relations       semanticEngineRelationView   `json:"relations"`
	NumericFacts    []semanticEngineNumeric      `json:"numeric_facts"`
	TemporalFacts   []semanticEngineTemporal     `json:"temporal_facts"`
	Bullets         []semanticEngineCandidate    `json:"bullets"`
	ShortPhrases    []semanticEngineCandidate    `json:"short_phrases"`
	LongPhrases     []semanticEngineCandidate    `json:"long_phrases"`
	Quotes          []semanticEngineCandidate    `json:"quotes"`
	ImportantWords  []semanticEngineCandidate    `json:"important_words"`
	VisualProposals []semanticEngineProposal     `json:"visual_proposals"`
	SemanticGroups  []semanticEngineGroup        `json:"semantic_groups"`
	MicroSummaries  []semanticEngineMicroSummary `json:"micro_summaries"`
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
}

type semanticEngineProposal struct {
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
