// Package scripts — curation types extracted from types.go (PG-029, June 2026).
package scripts

import "go.uber.org/zap"

// ── MediaCurator ─────────────────────────────────────────────────────────

// MediaCurator orchestrates semantic clip search + script generation.
// All fields are concrete typed.
type MediaCurator struct {
	// PG-034 (June 2026): vectorStore field removed — Qdrant capability
	// deleted. Callers seed Curate with HintClipIDs instead.
	serverURL   string
	clipsRepo   interface{} // *assets.ClipsRepository (avoid import cycle)
	clipBuilder *ClipSourceBuilder
	engine      *Engine
	log         *zap.Logger
}

// ── CurateRequest / CurateResult ─────────────────────────────────────────

// CurateRequest carries the inputs for a curation job.
type CurateRequest struct {
	Query             string
	Title             string
	Language          string
	Tone              string
	Model             string
	MaxClips          int
	SelectableClips   int
	TargetWords       int
	MaxCharsPerScene  int
	MinScore          float64
	Source            string
	MediaType         string
	Type              string
	Style             string
	StyleInstructions string
	ForceRefresh      bool
	// PG-034 (June 2026): HintClipIDs lets callers seed a curation with
	// pre-resolved clip IDs from upstream sources, replacing the deleted
	// Qdrant semantic-search leg.
	HintClipIDs []string
}

// CurateResult holds the output of a curation run.
type CurateResult struct {
	Title             string
	ClipScenes        []ClipScene
	Script            string
	WordCount         int
	CacheStatus       string
	AcceptedClipIDs   []string
	NarrativePlan     *NarrativePlan
	SourceText        string
	SourceFingerprint string
	SearchResults     []SearchResultInfo
	Timings           CurateTimings
}

// CurateTimings holds timing metrics for curation phases.
type CurateTimings struct {
	SearchMs      int64
	BuildCtxMs    int64
	WriteScriptMs int64
	TotalMs       int64
}

// SearchResultInfo holds a single search result.
type SearchResultInfo struct {
	ClipID    string
	Name      string
	Score     float64
	Source    string
	DriveLink string
}

// ClipScene represents a single scene with an associated clip.
type ClipScene struct {
	SceneIndex int
	Text       string
	ClipID     string
	DriveLink  string
	Kind       string
}

// ── Narrative structures ─────────────────────────────────────────────────

// NarrativePlan holds the narrative structure plan.
type NarrativePlan struct {
	Title        string             `json:"title"`
	Sections     []NarrativeSection `json:"sections"`
	TotalWords   int                `json:"total_words"`
	Style        string             `json:"style"`
	Relationship string             `json:"relationship"`
}

// NarrativeSection is one section of a narrative plan.
type NarrativeSection struct {
	Role       string `json:"role"`
	Purpose    string `json:"purpose"`
	WordBudget int    `json:"word_budget"`
	KeyPoints  string `json:"key_points"`
}

// ── Job payloads ─────────────────────────────────────────────────────────

// JobPayloadCurate holds the payload for a curation job.
type JobPayloadCurate struct {
	Query             string   `json:"query"`
	Title             string   `json:"title"`
	Languages         []string `json:"languages,omitempty"`
	Language          string   `json:"language"`
	Tone              string   `json:"tone"`
	Model             string   `json:"model"`
	MaxClips          int      `json:"max_clips"`
	SelectableClips   int      `json:"selectable_clips"`
	TargetWords       int      `json:"target_words"`
	MaxCharsPerScene  int      `json:"max_chars_per_scene"`
	MinScore          float64  `json:"min_score"`
	Source            string   `json:"source"`
	MediaType         string   `json:"media_type"`
	Type              string   `json:"type"`
	Style             string   `json:"style"`
	StyleInstructions string   `json:"style_instructions"`
	ForceRefresh      bool     `json:"force_refresh"`
	GenerateVoiceover bool     `json:"generate_voiceover"`
	VoiceoverFolderID string   `json:"voiceover_folder_id"`
	VoiceoverGroup    string   `json:"voiceover_group"`
}

// JobPayloadCatalogScript holds the payload for catalog-first script generation.
type JobPayloadCatalogScript struct {
	Topic              string   `json:"topic"`
	ClipIDs            []string `json:"clip_ids"`
	Title              string   `json:"title"`
	OutputName         string   `json:"output_name"`
	MaxClips           int      `json:"max_clips"`
	MinCoverage        float64  `json:"min_coverage"`
	Languages          []string `json:"languages,omitempty"`
	Language           string   `json:"language"`
	Tone               string   `json:"tone"`
	Model              string   `json:"model"`
	TargetWords        int      `json:"target_words"`
	Duration           int      `json:"duration"`
	TranscriptPolicy   string   `json:"transcript_policy"`
	OrderingStrategy   string   `json:"ordering_strategy"`
	CreateDoc          bool     `json:"create_doc"`
	SaveToDB           bool     `json:"save_to_db"`
	GenerateTimeline   bool     `json:"generate_timeline"`
	ForceRefresh       bool     `json:"force_refresh"`
	MinQualityScore    *float64 `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int     `json:"min_transcript_words,omitempty"`
}
