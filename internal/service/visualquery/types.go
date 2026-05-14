package visualquery

const (
	DefaultMaxQueries = 3
	MinQueryWords     = 2
	MaxQueryWords     = 4
	cacheVersion      = "v1"
)

// VisualQueryResult contains the enriched query result from LLM
type VisualQueryResult struct {
	VisualSubject string   `json:"visual_subject"`
	VisualCaption string   `json:"visual_caption"`
	Queries       []string `json:"queries"`
	EntityQueries []string `json:"entity_queries"`
	VisualPrompts []string `json:"visual_prompts"`
}

// BatchSegmentInput represents a segment for batch processing
type BatchSegmentInput struct {
	Index     int
	Subject   string
	Narrative string
}
