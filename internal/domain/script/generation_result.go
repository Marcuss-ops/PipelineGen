// Package script — generation_result.go defines the canonical
// GenerationResult and its nested result types. Every generation
// (single or batch item) produces exactly one GenerationResult.
//
// No durable field uses interface{}, any, or map[string]any.
package script

// GenerationResult is the canonical output of a single generation
// item. It carries the generated script, postprocessor outputs,
// and timing metadata. The caller matches it to the original
// GenerationItemV2 via the ID field.
type GenerationResult struct {
	// ID echoes GenerationItemV2.ID for correlation.
	ID string `json:"id,omitempty"`

	// ── Script ────────────────────────────────────────────────────────
	Script      string `json:"script"`
	WordCount   int    `json:"word_count"`
	Title       string `json:"title,omitempty"`
	Language    string `json:"language,omitempty"`
	Model       string `json:"model,omitempty"`
	CacheStatus string `json:"cache_status,omitempty"` // "exact_hit", "generated"
	CacheHit    bool   `json:"cache_hit,omitempty"`

	// ── Entities ──────────────────────────────────────────────────────
	EntitiesJSON string `json:"entities_json,omitempty"`

	// ── Metadata ──────────────────────────────────────────────────────
	Metadata []VideoMetadata `json:"metadata,omitempty"`

	// ── Voiceover ─────────────────────────────────────────────────────
	Voiceovers []VoiceoverResult `json:"voiceovers,omitempty"`

	// ── Scene images ──────────────────────────────────────────────────
	SceneImages []SceneImageResult `json:"scene_images,omitempty"`
	ScenesJSON  string             `json:"scenes_json,omitempty"`

	// ── Clip scenes (from clip-aware generation) ──────────────────────
	ClipScenes []ClipSceneResult `json:"clip_scenes,omitempty"`

	// ── Document ──────────────────────────────────────────────────────
	DocLink string `json:"doc_link,omitempty"`
	DocID   string `json:"doc_id,omitempty"`

	// ── Search artifacts ──────────────────────────────────────────────
	SearchResults       []SearchResultItem `json:"search_results,omitempty"`
	AcceptedClipIDs     []string           `json:"accepted_clip_ids,omitempty"`

	// ── Timings ───────────────────────────────────────────────────────
	Timings GenerationTimings `json:"timings,omitempty"`

	// ── Errors (non-fatal per-postprocessor) ──────────────────────────
	Warnings []string `json:"warnings,omitempty"`
}

// VideoMetadata holds YouTube-style metadata for a script result.
type VideoMetadata struct {
	Language    string   `json:"language"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

// VoiceoverResult holds the result of a single voiceover generation.
type VoiceoverResult struct {
	SceneIndex int    `json:"scene_index"`
	Text       string `json:"text"`
	Status     string `json:"status"` // "completed", "failed"
	Link       string `json:"link,omitempty"`
	LocalPath  string `json:"local_path,omitempty"`
	Language   string `json:"language,omitempty"`
}

// SceneImageResult holds the result of a single scene image generation.
type SceneImageResult struct {
	SceneIndex int    `json:"scene_index"`
	Text       string `json:"text"`
	ImageURL   string `json:"image_url,omitempty"`
	DriveLink  string `json:"drive_link,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

// ClipSceneResult holds a single clip-anchored scene from clip-aware
// generation.
type ClipSceneResult struct {
	SceneIndex int    `json:"scene_index"`
	Text       string `json:"text"`
	ClipID     string `json:"clip_id,omitempty"`
	DriveLink  string `json:"drive_link,omitempty"`
	Kind       string `json:"kind,omitempty"` // "clip", "narration"
}

// GenerationTimings holds elapsed-time metrics for each generation phase.
type GenerationTimings struct {
	SourceResolveMs int64 `json:"source_resolve_ms,omitempty"`
	PlanBuildMs     int64 `json:"plan_build_ms,omitempty"`
	EngineMs        int64 `json:"engine_ms,omitempty"`

	// Per-postprocessor timings (keyed by processor name).
	PostprocessMs map[string]int64 `json:"postprocess_ms,omitempty"`

	TotalMs int64 `json:"total_ms"`
}
