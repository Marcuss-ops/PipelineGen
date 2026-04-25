package script

// timelineLLMPlan represents the plan structure returned by LLM for timeline generation.
type timelineLLMPlan struct {
	PrimaryFocus string               `json:"primary_focus"`
	Segments     []timelineLLMSegment `json:"segments"`
}

// timelineLLMSegment represents a segment in the LLM timeline plan.
type timelineLLMSegment struct {
	Index               int      `json:"index"`
	StartTime           float64  `json:"start_time"`
	EndTime             float64  `json:"end_time"`
	OpeningSentence     string   `json:"opening_sentence"`
	ClosingSentence     string   `json:"closing_sentence"`
	Keywords            []string `json:"keywords"`
	Entities            []string `json:"entities"`
	PreferredStockGroup string   `json:"preferred_stock_group,omitempty"`
	PreferredStockPaths []string `json:"preferred_stock_paths,omitempty"`
}
