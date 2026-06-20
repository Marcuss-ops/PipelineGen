package scripts

// GenerateFromCatalogRequest is the input for catalog-first script generation.
type GenerateFromCatalogRequest struct {
	Topic       string  `json:"topic" binding:"required"`
	MaxClips    int     `json:"max_clips"`
	MinCoverage float64 `json:"min_coverage"` // 0.0-1.0

	Title      string `json:"title"`
	OutputName string `json:"output_name,omitempty"`
	Language   string `json:"language,omitempty"`
	Tone       string `json:"tone,omitempty"`
	Model      string `json:"model,omitempty"`

	TargetWords      int    `json:"target_words,omitempty"`
	Duration         int    `json:"duration,omitempty"`
	TranscriptPolicy string `json:"transcript_policy,omitempty"`
	OrderingStrategy string `json:"ordering_strategy,omitempty"`

	CreateDoc        bool `json:"create_doc,omitempty"`
	SaveToDB         bool `json:"save_to_db,omitempty"`
	GenerateTimeline bool `json:"generate_timeline,omitempty"`
	ForceRefresh     bool `json:"force_refresh,omitempty"`

	MinQualityScore    *float64 `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int     `json:"min_transcript_words,omitempty"`
}

// GenerateFromCatalogResponse is the async job response.
type GenerateFromCatalogResponse struct {
	OK     bool   `json:"ok"`
	JobID  string `json:"job_id"`
	Status string `json:"status"`

	CatalogReport *CatalogReport `json:"catalog_report,omitempty"`
}

// CurateRequest is the input for POST /api/script/curate.
type CurateRequest struct {
	Query string `json:"query" binding:"required"`

	Title    string `json:"title,omitempty"`
	Language string `json:"language,omitempty"`
	Tone     string `json:"tone,omitempty"`
	Model    string `json:"model,omitempty"`

	MaxClips         int     `json:"max_clips,omitempty"`
	TargetWords      int     `json:"target_words,omitempty"`
	MaxCharsPerScene int     `json:"max_chars_per_scene,omitempty"`
	MinScore         float64 `json:"min_score,omitempty"`

	Source    string `json:"source,omitempty"`
	MediaType string `json:"media_type,omitempty"`

	Type              string `json:"type,omitempty"`
	Style             string `json:"style,omitempty"`
	StyleInstructions string `json:"style_instructions,omitempty"`

	SelectableClips int `json:"selectable_clips,omitempty"`

	GenerateVoiceover bool   `json:"generate_voiceover,omitempty"`
	VoiceoverGroup    string `json:"voiceover_group,omitempty"`
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"`

	ForceRefresh bool `json:"force_refresh,omitempty"`
}

// JobPayloadCatalogScript is the runtime payload for catalog-first script generation.
type JobPayloadCatalogScript struct {
	ClipIDs          []string `json:"clip_ids"`
	Title            string   `json:"title"`
	OutputName       string   `json:"output_name"`
	Language         string   `json:"language"`
	Tone             string   `json:"tone"`
	Model            string   `json:"model"`
	TargetWords      int      `json:"target_words"`
	Duration         int      `json:"duration"`
	TranscriptPolicy string   `json:"transcript_policy"`
	OrderingStrategy string   `json:"ordering_strategy"`
	CreateDoc        bool     `json:"create_doc"`
	SaveToDB         bool     `json:"save_to_db"`
	ForceRefresh     bool     `json:"force_refresh"`

	MinQualityScore    *float64 `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int     `json:"min_transcript_words,omitempty"`
}

// JobPayloadCurate is the runtime payload for script.curate job processing.
type JobPayloadCurate struct {
	Query             string  `json:"query"`
	Title             string  `json:"title"`
	Language          string  `json:"language"`
	Tone              string  `json:"tone"`
	Model             string  `json:"model"`
	MaxClips          int     `json:"max_clips"`
	TargetWords       int     `json:"target_words"`
	MaxCharsPerScene  int     `json:"max_chars_per_scene"`
	MinScore          float64 `json:"min_score"`
	Source            string  `json:"source"`
	MediaType         string  `json:"media_type"`
	Type              string  `json:"type"`
	Style             string  `json:"style"`
	StyleInstructions string  `json:"style_instructions"`
	SelectableClips   int     `json:"selectable_clips"`
	GenerateVoiceover bool    `json:"generate_voiceover"`
	VoiceoverGroup    string  `json:"voiceover_group"`
	VoiceoverFolderID string  `json:"voiceover_folder_id"`
	ForceRefresh      bool    `json:"force_refresh"`
}
