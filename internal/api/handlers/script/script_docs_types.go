package script

// ScriptDocsRequest is the input for modular script-doc generation.
type ScriptDocsRequest struct {
	Topic       string `json:"topic" binding:"required"`
	Duration    int    `json:"duration"`
	Language    string `json:"language"`
	Template    string `json:"template"`
	PreviewOnly bool   `json:"preview_only"`
	SourceText  string `json:"source_text"`
	Voiceover   bool   `json:"voiceover"`
}

func (r *ScriptDocsRequest) normalize() {
	if r.Duration <= 0 {
		r.Duration = 60
	}
	if r.Language == "" {
		r.Language = "it"
	}
	if r.Template == "" {
		r.Template = "documentary"
	}
}

// ScriptSection is a named section in the generated document.
type ScriptSection struct {
	Title string
	Body  string
}

// ScriptDocument is the final assembled output before upload/preview.
type ScriptDocument struct {
	Title    string
	Content  string
	Sections []ScriptSection
	Timeline *TimelinePlan
}

// TimelinePlan is the structured timestamp/action breakdown for a generated script.
type TimelinePlan struct {
	PrimaryFocus  string            `json:"primary_focus"`
	SegmentCount  int               `json:"segment_count"`
	TotalDuration int               `json:"total_duration"`
	Segments      []TimelineSegment `json:"segments"`
}

// TimelineSegment is one timestamp/action block in the generated script.
type TimelineSegment struct {
	Index               int           `json:"index"`
	StartTime           float64       `json:"start_time"`
	EndTime             float64       `json:"end_time"`
	Timestamp           string        `json:"timestamp"`
	OpeningSentence     string        `json:"opening_sentence"`
	ClosingSentence     string        `json:"closing_sentence"`
	Keywords            []string      `json:"keywords,omitempty"`
	Entities            []string      `json:"entities,omitempty"`
	PreferredStockGroup string        `json:"preferred_stock_group,omitempty"`
	PreferredStockPaths []string      `json:"preferred_stock_paths,omitempty"`
	StockMatches        []scoredMatch `json:"stock_matches,omitempty"`
	DriveMatches        []scoredMatch `json:"drive_matches,omitempty"`
	ArtlistMatches      []scoredMatch `json:"artlist_matches,omitempty"`
}
