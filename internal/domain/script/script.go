// Package script defines the canonical domain types for the script subsystem.
//
// Scripts are the central artifact of PipelineGen: AI-generated narrative
// text that drives video production from clips, images, and voiceovers.
package script

import "time"

// Script is the canonical domain entity for a generated script.
type Script struct {
	ID             int64     `json:"id"`
	Topic          string    `json:"topic"`
	Title          string    `json:"title"`
	Duration       int       `json:"duration"`
	Language       string    `json:"language"`
	Template       string    `json:"template"`
	Mode           string    `json:"mode"`
	Tone           string    `json:"tone"`
	TargetWords    int       `json:"target_words"`
	FinalWordCount int       `json:"final_word_count"`
	Status         string    `json:"status"`
	NarrativeText  string    `json:"narrative_text"`
	TimelineJSON   string    `json:"timeline_json"`
	EntitiesJSON   string    `json:"entities_json"`
	MetadataJSON   string    `json:"metadata_json"`
	FullDocument   string    `json:"full_document"`
	ModelUsed      string    `json:"model_used"`
	OllamaBaseURL  string    `json:"ollama_base_url"`
	Version        int       `json:"version"`
	ParentScriptID *int64    `json:"parent_script_id,omitempty"`
	IsDeleted      bool      `json:"is_deleted"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Section represents a section (paragraph, scene) within a script.
type Section struct {
	ID           int64  `json:"id"`
	ScriptID     int64  `json:"script_id"`
	SectionType  string `json:"section_type"`
	SectionTitle string `json:"section_title"`
	Content      string `json:"content"`
	SortOrder    int    `json:"sort_order"`
	WordCount    int    `json:"word_count"`
	Status       string `json:"status"`
}

// StockMatch represents a stock media match for a script segment.
type StockMatch struct {
	ID           int64   `json:"id"`
	ScriptID     int64   `json:"script_id"`
	SegmentIndex int     `json:"segment_index"`
	StockPath    string  `json:"stock_path"`
	StockSource  string  `json:"stock_source"`
	Score        float64 `json:"score"`
	MatchedTerms string  `json:"matched_terms"`
}

// ResearchSource represents a web/youtube/transcript source used during generation.
type ResearchSource struct {
	ID             int64     `json:"id"`
	ScriptID       int64     `json:"script_id"`
	Source         string    `json:"source,omitempty"`
	Query          string    `json:"query"`
	URL            string    `json:"url"`
	Title          string    `json:"title"`
	Snippet        string    `json:"snippet"`
	Excerpt        string    `json:"excerpt,omitempty"`
	SourceType     string    `json:"source_type"`
	UsedInSections string    `json:"used_in_sections"`
	RelevanceScore float64   `json:"relevance_score"`
	CreatedAt      time.Time `json:"created_at"`
}

// GenerationLog records a single phase of script generation for debugging.
type GenerationLog struct {
	ID          int64     `json:"id"`
	ScriptID    int64     `json:"script_id"`
	Phase       string    `json:"phase"`
	PromptHash  string    `json:"prompt_hash"`
	Model       string    `json:"model"`
	InputWords  int       `json:"input_words"`
	OutputWords int       `json:"output_words"`
	DurationMs  int64     `json:"duration_ms"`
	RetryCount  int       `json:"retry_count"`
	CacheStatus string    `json:"cache_status"`
	Error       string    `json:"error"`
	CreatedAt   time.Time `json:"created_at"`
}

// OutlineSection represents an outline section as an intermediate step.
type OutlineSection struct {
	ID            int64     `json:"id"`
	ScriptID      int64     `json:"script_id"`
	SectionIndex  int       `json:"section_index"`
	Title         string    `json:"title"`
	Purpose       string    `json:"purpose"`
	TargetWords   int       `json:"target_words"`
	KeyPointsJSON string    `json:"key_points_json"`
	EmotionalRole string    `json:"emotional_role"`
	CreatedAt     time.Time `json:"created_at"`
}

// VersionRecord represents an explicit version of a script output.
type VersionRecord struct {
	ID           int64     `json:"id"`
	ScriptID     int64     `json:"script_id"`
	Version      int       `json:"version"`
	FinalText    string    `json:"final_text"`
	MetadataJSON string    `json:"metadata_json"`
	CreatedAt    time.Time `json:"created_at"`
}
