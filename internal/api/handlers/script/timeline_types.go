package script

// TimelinePlan is the structured timestamp/action breakdown for a generated script.
type TimelinePlan struct {
	PrimaryFocus  string            `json:"primary_focus"`
	SegmentCount  int               `json:"segment_count"`
	TotalDuration int               `json:"total_duration"`
	Segments      []TimelineSegment `json:"segments"`
}

// TimelineSegment is one timestamp/action block in the generated script.
type TimelineSegment struct {
	Index                int           `json:"index"`
	StartTime            float64       `json:"start_time"`
	EndTime              float64       `json:"end_time"`
	Timestamp            string        `json:"timestamp"`
	Subject              string        `json:"subject,omitempty"`
	NarrativeText        string        `json:"narrative_text,omitempty"`
	OpeningSentence      string        `json:"opening_sentence"`
	ClosingSentence      string        `json:"closing_sentence"`
	Keywords             []string      `json:"keywords,omitempty"`
	Entities             []string      `json:"entities,omitempty"`
	PreferredStockGroup  string        `json:"preferred_stock_group,omitempty"`
	PreferredStockPaths  []string      `json:"preferred_stock_paths,omitempty"`
	PreferredStockReason string        `json:"preferred_stock_reason,omitempty"`
	StockMatches         []scoredMatch `json:"stock_matches,omitempty"`
	DriveMatches         []scoredMatch `json:"drive_matches,omitempty"`
	ArtlistMatches       []scoredMatch `json:"artlist_matches,omitempty"`
}

// scoredMatch represents a potential media match with metadata.
type scoredMatch struct {
	Title   string `json:"title"`
	Path    string `json:"path"`
	Score   int    `json:"score"`
	Source  string `json:"source"`
	Link    string `json:"link"`
	Details string `json:"details"`
}

// internal LLM structures
type timelineLLMPlan struct {
	PrimaryFocus string               `json:"primary_focus"`
	Segments     []timelineLLMSegment `json:"segments"`
}

type timelineLLMSegment struct {
	Index                int      `json:"index"`
	StartTime            float64  `json:"start_time"`
	EndTime              float64  `json:"end_time"`
	Subject              string   `json:"subject"`
	NarrativeText        string   `json:"narrative_text"`
	OpeningSentence      string   `json:"opening_sentence"`
	ClosingSentence      string   `json:"closing_sentence"`
	Keywords             []string `json:"keywords"`
	Entities             []string `json:"entities"`
}

type timelineAssetSource string

const (
	timelineAssetSourceStockDrive     timelineAssetSource = "stock_drive"
	timelineAssetSourceArtlistFolder  timelineAssetSource = "artlist_folder"
	timelineAssetSourceArtlistDynamic timelineAssetSource = "artlist_dynamic"
)

type timelineFolderCandidate struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Link string `json:"link"`
}

type timelineAssetDecision struct {
	Source  string        `json:"source"`
	Folder  string        `json:"folder"`
	Reason  string        `json:"reason"`
	Matches []scoredMatch `json:"matches,omitempty"`
}
