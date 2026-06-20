package domain

import "time"

// GenerationSpec is the typed input for the unified script generation
// use case. It mirrors the union of POST /api/script/generate-from-clips
// and POST /api/script/generate-with-images payloads, minus transport
// concerns (Gin, JSON, multipart).
//
// Field-for-field parity with the legacy application/scriptflow types
// is intentional: Agent 1 will provide the request→spec conversion at
// the transport boundary.
type GenerationSpec struct {
	Topic               string   `json:"topic,omitempty"`
	SourceText          string   `json:"source_text,omitempty"`
	Guidelines          string   `json:"guidelines,omitempty"`
	Title               string   `json:"title,omitempty"`
	OutputName          string   `json:"output_name,omitempty"`
	Language            string   `json:"language,omitempty"`
	Tone                string   `json:"tone,omitempty"`
	Style               string   `json:"style,omitempty"`
	Model               string   `json:"model,omitempty"`
	DriveFolderID       string   `json:"drive_folder_id,omitempty"`
	TargetWords         int      `json:"target_words,omitempty"`
	Duration            int      `json:"duration,omitempty"`
	MinWords            int      `json:"min_words,omitempty"`
	SentencesPerImage   int      `json:"sentences_per_image,omitempty"`
	ImagesPerScene      int      `json:"images_per_scene,omitempty"`
	ClipIDs             []string `json:"clip_ids,omitempty"`
	NumClips            int      `json:"num_clips,omitempty"`
	Languages           []string `json:"languages,omitempty"`
	ArtlistSearch       bool     `json:"artlist_search,omitempty"`
	StockSearch         bool     `json:"stock_search,omitempty"`
	ExtractEntities     bool     `json:"extract_entities,omitempty"`
	GenerateMetadata    bool     `json:"generate_metadata,omitempty"`
	GenerateVoiceover   bool     `json:"generate_voiceover,omitempty"`
	VoiceoverGroup      string   `json:"voiceover_group,omitempty"`
	VoiceoverFolderID   string   `json:"voiceover_folder_id,omitempty"`
	GenerateSceneImages bool     `json:"generate_scene_images,omitempty"`
	GenerateDocs        bool     `json:"generate_docs,omitempty"`
	TranscriptPolicy    string   `json:"transcript_policy,omitempty"`
	OrderingStrategy    string   `json:"ordering_strategy,omitempty"`
	SaveToDB            bool     `json:"save_to_db,omitempty"`
	GenerateTimeline    bool     `json:"generate_timeline,omitempty"`
	ForceRefresh        bool     `json:"force_refresh,omitempty"`
	MinQualityScore     *float64 `json:"min_quality_score,omitempty"`
	MinTranscriptWords  *int     `json:"min_transcript_words,omitempty"`
	PromptVersion       string   `json:"prompt_version,omitempty"`
	EditorPromptVersion string   `json:"editor_prompt_version,omitempty"`
	QAPromptVersion     string   `json:"qa_prompt_version,omitempty"`
}

// GenerationResult is the typed output of the unified use case.
//
// Async submission (Sync==false): only OK and JobID/Status are populated.
// Sync execution (Sync==true): Script, Plan, Scenes, WordCount, Doc*
// fields are populated inline.
type GenerationResult struct {
	OK         bool      `json:"ok"`
	JobID      string    `json:"job_id,omitempty"`
	Status     string    `json:"status"`
	Sync       bool      `json:"sync"`
	Script     *Script   `json:"script,omitempty"`
	Plan       *Plan     `json:"plan,omitempty"`
	Scenes     []Scene   `json:"scenes,omitempty"`
	WordCount  int       `json:"word_count"`
	DocID      string    `json:"doc_id,omitempty"`
	DocURL     string    `json:"doc_url,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// JobReference identifies an async submission and is the minimal piece
// of information the HTTP transport needs to surface to the caller.
type JobReference struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}
