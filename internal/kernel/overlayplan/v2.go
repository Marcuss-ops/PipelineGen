// Package overlayplan owns the wire contract shared by the Go consumer and
// the BrainRust overlay compiler for the 13d single-call cutover.
//
// The envelope is versioned (`editorial_plan.v2`) and additive: later phases
// add typed sections without changing the meaning of existing fields. A
// missing or unknown schema_version is rejected by the Rust validator before
// any decision runs; this package only provides the exact JSON projection.
package overlayplan

const (
	// SchemaVersionV2 is the full-plan request/response envelope version.
	SchemaVersionV2 = "editorial_plan.v2"
	// EditorialManifestVersion is the per-scene editorial manifest version.
	EditorialManifestVersion = "editorial.v1"
	// ChapterManifestVersion is the chapter manifest version.
	ChapterManifestVersion = "chapter_manifest.v1"
	// EditorialProfileVersion is the certified editorial profile.
	EditorialProfileVersion = "highlights.v1"
)

// V2Request is one complete overlay-plan compilation request. It carries the
// inputs the Go planner currently feeds to the interleaved phases; the final
// single call will consume this envelope and return the sealed plan.
type V2Request struct {
	SchemaVersion  string             `json:"schema_version"`
	Plan           PlanIdentity       `json:"plan"`
	Language       string             `json:"language"`
	SourceLanguage string             `json:"source_language"`
	Canvas         Canvas             `json:"canvas"`
	Scenes         []Scene            `json:"scenes"`
	Editorial      *EditorialManifest `json:"editorial,omitempty"`
	Chapters       *ChapterManifest   `json:"chapters,omitempty"`
}

// PlanIdentity is the immutable plan identity. Motion entropy is derived from
// it, never from wall-clock state.
type PlanIdentity struct {
	PlanID        string `json:"plan_id"`
	VideoID       string `json:"video_id"`
	ProjectID     string `json:"project_id"`
	MotionSeed    string `json:"motion_seed"`
	CoherenceSeed string `json:"coherence_seed"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	FPSNum        int    `json:"fps_num"`
	FPSDen        int    `json:"fps_den"`
}

// Canvas carries the run-level selection controls. Rendering metadata stays
// opaque and is deliberately absent from this contract.
type Canvas struct {
	OverlayCategories        map[string]string `json:"overlay_categories"`
	MaxOverlaysPerScene      int               `json:"max_overlays_per_scene"`
	MaxPhraseOverlays        int               `json:"max_phrase_overlays"`
	MaxImageOverlays         int               `json:"max_image_overlays"`
	MaxMapOverlays           int               `json:"max_map_overlays"`
	DisableEditorialOverlays bool              `json:"disable_editorial_overlays"`
	DisableRecapOverlays     bool              `json:"disable_recap_overlays"`
	EnableCharts             bool              `json:"enable_charts"`
	DisableChartOverlays     bool              `json:"disable_chart_overlays"`
	MapsOnly                 bool              `json:"maps_only"`
}

// Scene is one timed scene. TimelineStartUS/DurationUS are the canonical plan
// offsets and the only authority for clipping a projected phrase.
type Scene struct {
	ID                 string            `json:"id"`
	Text               string            `json:"text"`
	DurationMS         int64             `json:"duration_ms"`
	ExecutionMode      string            `json:"execution_mode"`
	ShortPhraseOverlay bool              `json:"short_phrase_overlay"`
	OverlayCategories  map[string]string `json:"overlay_categories"`
	Voiceover          *Voiceover        `json:"voiceover,omitempty"`
	TimelineStartUS    int64             `json:"timeline_start_us"`
	DurationUS         int64             `json:"duration_us"`
}

// Voiceover is the scene's certified voiceover projection; only timing is
// needed by the editorial/chapter phase.
type Voiceover struct {
	Timing *Timing `json:"timing,omitempty"`
}

// Timing is the certified word-level timing, copied verbatim from the audio
// authority. It is never estimated from text length or scene duration.
type Timing struct {
	DurationUS int64        `json:"duration_us"`
	Words      []WordTiming `json:"words"`
}

// WordTiming is one word boundary in integer microseconds.
type WordTiming struct {
	Index   int    `json:"index"`
	Text    string `json:"text"`
	StartUS int64  `json:"start_us"`
	EndUS   int64  `json:"end_us"`
}

// EditorialManifest is the immutable editorial extractor output.
type EditorialManifest struct {
	SchemaVersion  string           `json:"schema_version"`
	SourceLanguage string           `json:"source_language"`
	ProfileVersion string           `json:"profile_version"`
	Scenes         []EditorialScene `json:"scenes"`
	Certified      bool             `json:"certified"`
	Fingerprint    string           `json:"fingerprint"`
}

// EditorialScene is one scene's extractive editorial product.
type EditorialScene struct {
	SceneID     string            `json:"scene_id"`
	Title       *string           `json:"title,omitempty"`
	TitleStatus string            `json:"title_status"`
	TitleSource string            `json:"title_source"`
	Bullets     []EditorialBullet `json:"bullets"`
}

// EditorialBullet is one extractive editorial sentence span.
type EditorialBullet struct {
	SentenceStart int    `json:"sentence_start"`
	SentenceEnd   int    `json:"sentence_end"`
	Text          string `json:"text"`
}

// ChapterManifest is the extractive chapter projection of the transcript.
type ChapterManifest struct {
	SchemaVersion string    `json:"schema_version"`
	Chapters      []Chapter `json:"chapters"`
}

// Chapter is one chapter; only its bullets participate in the projection.
type Chapter struct {
	Bullets []ChapterBullet `json:"bullets"`
}

// ChapterBullet is one extractive chapter bullet.
type ChapterBullet struct {
	Text          string `json:"text"`
	StartSentence int    `json:"start_sentence"`
	EndSentence   int    `json:"end_sentence"`
}

// V2Response is the projection response. The caller keeps its complete scene
// DTOs and appends these annotations.
type V2Response struct {
	SchemaVersion string                   `json:"schema_version"`
	ScenePhrases  map[string][]ScenePhrase `json:"scene_phrases"`
	CoveredScenes []string                 `json:"covered_scenes"`
}

// ScenePhrase is one projected editorial/chapter phrase with certified timing.
type ScenePhrase struct {
	Text             string  `json:"text"`
	StartUS          int64   `json:"start_us"`
	DurationUS       int64   `json:"duration_us"`
	Score            float64 `json:"score"`
	PresentationRole string  `json:"presentation_role"`
}
