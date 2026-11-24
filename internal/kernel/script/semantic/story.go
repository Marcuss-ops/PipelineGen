package semantic

// SemanticStoryGraph retains the grounded whole-script identity/fact graph.
type SemanticStoryGraph struct {
	Nodes []SemanticStoryNode `json:"nodes"`
	Edges []SemanticStoryEdge `json:"edges"`
}

type SemanticStoryNode struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Label          string   `json:"label"`
	SceneID        string   `json:"scene_id"`
	SceneIDs       []string `json:"scene_ids"`
	EntityID       *string  `json:"entity_id,omitempty"`
	FactID         *string  `json:"fact_id,omitempty"`
	NormalizedDate *string  `json:"normalized_date,omitempty"`
	SentenceIndex  *int     `json:"sentence_index,omitempty"`
	Evidence       string   `json:"evidence"`
	StartByte      int      `json:"start_byte"`
	EndByte        int      `json:"end_byte"`
}

type SemanticStoryEdge struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	FromID     string `json:"from_id"`
	ToID       string `json:"to_id"`
	CrossScene bool   `json:"cross_scene"`
	Evidence   string `json:"evidence"`
	StartByte  int    `json:"start_byte"`
	EndByte    int    `json:"end_byte"`
}

// SemanticVisualCoverage retains raw fact coverage, distinct from the selective
// consolidation view; proposal types describe representation, not retrieval.
type SemanticVisualCoverage struct {
	TotalFacts   int                     `json:"total_facts"`
	CoveredFacts int                     `json:"covered_facts"`
	Scenes       []SemanticSceneCoverage `json:"scenes"`
}

type SemanticSceneCoverage struct {
	SceneID string                 `json:"scene_id"`
	Facts   []SemanticCoverageItem `json:"facts"`
}

type SemanticCoverageItem struct {
	FactID          string   `json:"fact_id"`
	Predicate       string   `json:"predicate"`
	SubjectEntityID string   `json:"subject_entity_id"`
	Importance      float64  `json:"importance"`
	Status          string   `json:"status"`
	ProposalTypes   []string `json:"proposal_types"`
	SentenceIndex   int      `json:"sentence_index"`
	Evidence        string   `json:"evidence"`
	StartByte       int      `json:"start_byte"`
	EndByte         int      `json:"end_byte"`
}
