// Package scriptclips orchestrates script generation + entity extraction + clip mapping.
package scriptclips

// ProgressCallback is called during pipeline execution to report progress
// step: current step name (script_generation, entity_extraction, clip_search, clip_download, clip_upload)
// progress: 0-100 percentage
// message: human-readable message about what's happening
// entityName: (optional) current entity being processed
// clipsDone: number of clips processed so far
// clipsTotal: total number of clips to process
type ProgressCallback func(step string, progress int, message string, entityName string, clipsDone int, clipsTotal int)

// ScriptClipsRequest represents the input for script generation with clips
type ScriptClipsRequest struct {
	SourceText            string           `json:"source_text" binding:"required"`
	Title                 string           `json:"title" binding:"required"`
	Language              string           `json:"language"`
	Duration              int              `json:"duration"`
	Tone                  string           `json:"tone"`
	Model                 string           `json:"model"`
	EntityCountPerSegment int              `json:"entity_count_per_segment"`
	ProgressCallback      ProgressCallback `json:"-"` // Not serialized, used for progress updates
}

// SegmentClipMapping represents clip mapping for a single segment
type SegmentClipMapping struct {
	SegmentIndex int          `json:"segment_index"`
	Text         string       `json:"text"`
	StartTime    string       `json:"start_time"`
	EndTime      string       `json:"end_time"`
	Entities     EntityResult `json:"entities"`
	ClipMappings []ClipMapping `json:"clip_mappings"`
}

// EntityResult holds extracted entities for a segment
type EntityResult struct {
	FrasiImportanti  []string          `json:"frasi_importanti"`
	NomiSpeciali     []string          `json:"nomi_speciali"`
	ParoleImportanti []string          `json:"parole_importanti"`
	EntitaSenzaTesto map[string]string `json:"entity_senza_testo"`
}

// ClipMapping represents the mapping between an entity and a stock clip
type ClipMapping struct {
	Entity        string `json:"entity"`
	SearchQueryEN string `json:"search_query_en"`
	ClipFound     bool   `json:"clip_found"`
	ClipStatus    string `json:"clip_status"` // "found_on_drive", "downloaded_and_uploaded", "not_found", "cache_hit"
	YouTubeURL    string `json:"youtube_url,omitempty"`
	DriveURL      string `json:"drive_url,omitempty"`
	DriveFileID   string `json:"drive_file_id,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
}

// ScriptClipsResponse is the complete response with script + clip mappings
type ScriptClipsResponse struct {
	OK                bool                 `json:"ok"`
	Script            string               `json:"script"`
	WordCount         int                  `json:"word_count"`
	EstDuration       int                  `json:"est_duration"`
	Model             string               `json:"model"`
	Segments          []SegmentClipMapping `json:"segments"`
	TotalClipsFound   int                  `json:"total_clips_found"`
	TotalClipsMissing int                  `json:"total_clips_missing"`
	ProcessingTime    float64              `json:"processing_time_seconds"`
	VoiceoverFile     string               `json:"voiceover_file,omitempty"`
	VoiceoverDuration int                  `json:"voiceover_duration,omitempty"`
	VoiceoverVoice    string               `json:"voiceover_voice,omitempty"`
}
