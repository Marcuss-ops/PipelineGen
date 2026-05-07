package script

import "velox/go-master/internal/service/association"

// TimelinePlan is the structured timestamp/action breakdown for a generated script.
type TimelinePlan struct {
	PrimaryFocus  string            `json:"primary_focus"`
	SegmentCount  int               `json:"segment_count"`
	TotalDuration int               `json:"total_duration"`
	Segments      []TimelineSegment `json:"segments"`
}

// TimelineSegment is one timestamp/action block in the generated script.
type TimelineSegment struct {
	Index                int                       `json:"index"`
	StartTime            float64                   `json:"start_time"`
	EndTime              float64                   `json:"end_time"`
	Timestamp            string                    `json:"timestamp"`
	Subject              string                    `json:"subject,omitempty"`
	CanonicalSubject     string                    `json:"canonical_subject,omitempty"`
	NarrativeText        string                    `json:"narrative_text,omitempty"`
	OpeningSentence      string                    `json:"opening_sentence"`
	ClosingSentence      string                    `json:"closing_sentence"`
	Keywords             []string                  `json:"keywords,omitempty"`
	Entities             []string                  `json:"entities,omitempty"`
	CanonicalKeywords    []string                  `json:"canonical_keywords,omitempty"`
	CanonicalEntities    []string                  `json:"canonical_entities,omitempty"`
	NormalizationSource  string                    `json:"normalization_source,omitempty"`
	PreferredStockGroup  string                    `json:"preferred_stock_group,omitempty"`
	PreferredStockPaths  []string                  `json:"preferred_stock_paths,omitempty"`
	PreferredStockReason string                    `json:"preferred_stock_reason,omitempty"`
	StockMatches         []association.ScoredMatch `json:"stock_matches,omitempty"`
	DriveMatches         []association.ScoredMatch `json:"drive_matches,omitempty"`
	ArtlistMatches       []association.ScoredMatch `json:"artlist_matches,omitempty"`
	VisualSubject       string                    `json:"visual_subject,omitempty"`
	VisualCaption       string                    `json:"visual_caption,omitempty"`
	SearchSuggestions    []string                  `json:"search_suggestions,omitempty"`
}

// internal LLM structures
type timelineLLMPlan struct {
	PrimaryFocus string               `json:"primary_focus"`
	Segments     []timelineLLMSegment `json:"segments"`
}

type timelineLLMSegment struct {
	Index              int      `json:"index"`
	StartTime          float64  `json:"start_time"`
	EndTime            float64  `json:"end_time"`
	Subject            string   `json:"subject"`
	NarrativeText      string   `json:"narrative_text"`
	OpeningSentence    string   `json:"opening_sentence"`
	ClosingSentence    string   `json:"closing_sentence"`
	Keywords           []string `json:"keywords"`
	Entities           []string `json:"entities"`
	SearchSuggestions  []string `json:"search_suggestions"`
}

const (
	timelineAssetSourceStockDrive     = association.AssetSourceStockDrive
	timelineAssetSourceArtlistFolder  = association.AssetSourceArtlistFolder
	timelineAssetSourceArtlistDynamic = association.AssetSourceArtlistDynamic
)

type timelineAssetDecision struct {
	Source  string                    `json:"source"`
	Folder  string                    `json:"folder"`
	Reason  string                    `json:"reason"`
	Matches []association.ScoredMatch `json:"matches,omitempty"`
}
