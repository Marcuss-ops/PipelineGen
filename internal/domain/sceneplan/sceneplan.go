// Package sceneplan defines the canonical domain types for scene
// planning and rendering (Fase 0 della Spina Dorsale, July 2026).
//
// The sceneplan package is the single source of truth for:
//   - ScenePlan: what a scene needs (text, visual, audio) — intent only.
//   - VisualRequirement / AudioRequirement: typed descriptors of what
//     each scene requires, without infrastructure details.
//   - ResolvedScene: the result of asset resolution — canonical asset
//     IDs, not local paths or Drive links.
//   - RenderManifest / RenderableScene: the materialised plan handed
//     to the rendering worker.
//
// Separation contract:
//
//	ScenePlan        → describes intent (no infrastructure)
//	AssetResolution  → resolves to ResolvedScene (canonical asset IDs)
//	RenderManifest   → materialises for the renderer (final metadata)
//
// SpecScene (internal/domain/script/) should NOT contain Drive links,
// local paths, provider responses, job status, or temporary filenames.
// Those belong in the rendering layer, not the scene description.
//
// Canonical reference: Piano d'Azione § Fase 5.
package sceneplan

// ── OriginPolicy ─────────────────────────────────────────────────────

// OriginPolicy controls whether a visual asset should be retrieved
// from existing media or freshly generated.
type OriginPolicy string

const (
	// OriginRetrieved means the asset should be searched in the
	// existing catalog (Qdrant + stock providers).
	OriginRetrieved OriginPolicy = "retrieved"

	// OriginGenerated means the asset must be freshly generated
	// (AI image generation, stock search with fallback).
	OriginGenerated OriginPolicy = "generated"

	// OriginRetrievedOrGenerated means the system should try
	// retrieval first, falling back to generation on miss.
	OriginRetrievedOrGenerated OriginPolicy = "retrieved_or_generated"
)

// ── VisualRequirement ────────────────────────────────────────────────

// VisualRequirement describes the visual needs of a scene without
// prescribing how to fulfil them. No Drive links, local paths, or
// provider details.
type VisualRequirement struct {
	// Kind is the type of visual asset needed.
	// Typical values: "image", "clip", "stock".
	Kind string `json:"kind"`

	// Query is the natural-language description used for search
	// or generation (e.g. "ancient roman city at sunset").
	Query string `json:"query"`

	// OriginPolicy controls retrieval vs generation strategy.
	OriginPolicy OriginPolicy `json:"origin_policy"`

	// MinWidth and MinHeight are optional minimum pixel dimensions.
	MinWidth  int `json:"min_width,omitempty"`
	MinHeight int `json:"min_height,omitempty"`

	// Style is an optional style preset name (e.g. "cinematic", "realistic").
	Style string `json:"style,omitempty"`
}

// ── AudioRequirement ─────────────────────────────────────────────────

// AudioRequirement describes the audio needs of a scene — typically
// voiceover or background music.
type AudioRequirement struct {
	// Kind is the type of audio asset needed.
	// Typical values: "voiceover", "music", "sound_effect".
	Kind string `json:"kind"`

	// Text is the text to convert to speech (voiceover only).
	Text string `json:"text,omitempty"`

	// Locale is the BCP-47 language tag for voiceover (e.g. "it-IT").
	Locale string `json:"locale,omitempty"`

	// Voice is an optional explicit TTS voice name override.
	Voice string `json:"voice,omitempty"`
}

// ── DurationPolicy ───────────────────────────────────────────────────

// DurationPolicy controls how long a scene should appear on screen.
type DurationPolicy struct {
	// Seconds is the exact duration in seconds. Zero means auto-calculate.
	Seconds float64 `json:"seconds"`

	// AutoCalculate, when true, means the renderer should derive the
	// duration from the voiceover audio length.
	AutoCalculate bool `json:"auto_calculate"`
}

// ── ScenePlan ────────────────────────────────────────────────────────

// ScenePlan is the canonical description of what a scene needs.
// It contains only intent and references — no infrastructure details,
// no local paths, no Drive links, no job status.
//
// ScenePlan is produced by the script generator and consumed by the
// asset resolution and rendering pipelines.
type ScenePlan struct {
	// ID is a unique scene identifier within the script.
	ID string `json:"id"`

	// Text is the scene narration or description.
	Text string `json:"text"`

	// Duration controls how the scene timing is determined.
	Duration DurationPolicy `json:"duration"`

	// Visual describes what visual assets this scene needs.
	// Zero value means no visual requirement.
	Visual VisualRequirement `json:"visual,omitempty"`

	// Audio describes what audio assets this scene needs.
	// Zero value means no audio requirement.
	Audio AudioRequirement `json:"audio,omitempty"`
}

// ── ResolvedScene ────────────────────────────────────────────────────

// ResolvedScene is the result of asset resolution: each requirement
// in the ScenePlan has been matched to a canonical asset ID.
//
// ResolvedScene does NOT contain local paths, Drive links, or
// provider metadata — only canonical identifiers.
type ResolvedScene struct {
	// SceneID matches ScenePlan.ID.
	SceneID string `json:"scene_id"`

	// VisualAssetID is the canonical asset ID for the visual.
	// Empty means no visual was resolved.
	VisualAssetID string `json:"visual_asset_id,omitempty"`

	// AudioAssetID is the canonical asset ID for the audio.
	// Empty means no audio was resolved.
	AudioAssetID string `json:"audio_asset_id,omitempty"`
}

// ── RenderableScene ──────────────────────────────────────────────────

// RenderableScene is the fully materialised scene handed to the
// rendering worker. It combines the scene plan with resolved asset
// metadata needed for rendering (local paths, Drive links, durations).
//
// This is NOT a domain-intent type — it lives at the rendering
// boundary. It contains infrastructure details ONLY because the
// renderer needs them to produce the final video.
type RenderableScene struct {
	// SceneID matches ScenePlan.ID.
	SceneID string `json:"scene_id"`

	// Text is the scene narration.
	Text string `json:"text"`

	// DurationSec is the resolved scene duration in seconds.
	DurationSec float64 `json:"duration_sec"`

	// VisualPath is the local or remote path to the visual asset
	// the renderer will display.
	VisualPath string `json:"visual_path,omitempty"`

	// VisualDriveLink is the Google Drive link for the visual asset.
	VisualDriveLink string `json:"visual_drive_link,omitempty"`

	// AudioPath is the local or remote path to the audio asset
	// the renderer will play.
	AudioPath string `json:"audio_path,omitempty"`

	// AudioDriveLink is the Google Drive link for the audio asset.
	AudioDriveLink string `json:"audio_drive_link,omitempty"`

	// Transition is an optional transition effect between scenes.
	Transition string `json:"transition,omitempty"`
}

// ── RenderManifest ───────────────────────────────────────────────────

// RenderManifest is the complete materialised plan for the rendering
// worker. It contains all the scenes in order, plus global rendering
// configuration.
type RenderManifest struct {
	// Scenes is the ordered sequence of renderable scenes.
	Scenes []RenderableScene `json:"scenes"`

	// OutputFormat is the target video format (e.g. "mp4", "webm").
	OutputFormat string `json:"output_format"`

	// Resolution is the target resolution (e.g. "1920x1080").
	Resolution string `json:"resolution,omitempty"`

	// FPS is the target frames per second.
	FPS int `json:"fps,omitempty"`

	// BackgroundMusicPath is an optional path to background audio.
	BackgroundMusicPath string `json:"background_music_path,omitempty"`
}

// ── AssetRequirements (Fase 2 Spina Dorsale, July 2026) ─────────────

// AssetRequirements is the canonical downstream-work descriptor
// emitted by script.generate. It describes what each scene needs
// without materialising any artifact — images, voiceover, and
// documents are produced by separate downstream jobs.
//
// AssetRequirements replaces the inline postprocessor chain for
// voiceover, images, and document generation. The script generator
// emits one AssetRequirements envelope per generation; downstream
// jobs (assets.resolve, images.generate, voiceover.generate,
// document.generate) each consume the relevant subset.
type AssetRequirements struct {
	// ScriptID is the canonical script row identifier (populated
	// by the persistence postprocessor).
	ScriptID int64 `json:"script_id,omitempty"`

	// CorrelationID links all downstream jobs to the same workflow
	// invocation.
	CorrelationID string `json:"correlation_id"`

	// Scenes holds per-scene asset requirements. One entry per
	// scene that needs visual or audio assets.
	Scenes []SceneAssetRequirement `json:"scenes,omitempty"`

	// DocumentRequest, when non-nil, signals that a Google Doc
	// should be produced from the generated script.
	DocumentRequest *DocumentGenerationRequest `json:"document_request,omitempty"`

	// Metadata carries multilingual metadata for downstream
	// publication (title, description, tags per language).
	Metadata []AssetMetadata `json:"metadata,omitempty"`
}

// SceneAssetRequirement pairs a scene index with its visual and
// audio requirements, derived from the generated SpecScene.
type SceneAssetRequirement struct {
	// SceneIndex is the zero-based position in the SpecScene.Scenes array.
	SceneIndex int `json:"scene_index"`

	// SceneID matches SpecScene.ID.
	SceneID string `json:"scene_id"`

	// Text is the scene narration text (used as voiceover input).
	Text string `json:"text"`

	// Visual describes what visual assets this scene needs.
	// Zero value means no visual requirement.
	Visual VisualRequirement `json:"visual,omitempty"`

	// Audio describes what audio assets this scene needs.
	// Zero value means no audio requirement.
	Audio AudioRequirement `json:"audio,omitempty"`
}

// DocumentGenerationRequest carries the parameters needed to
// produce a Google Doc from the generated script.
type DocumentGenerationRequest struct {
	// DriveFolderID is the target Google Drive folder.
	DriveFolderID string `json:"drive_folder_id"`

	// Title is the document title.
	Title string `json:"title,omitempty"`

	// Language is the BCP-47 language tag of the script.
	Language string `json:"language,omitempty"`
}

// AssetMetadata carries per-language title, description, and tags
// for downstream publication (e.g. YouTube video metadata).
type AssetMetadata struct {
	Language    string   `json:"language"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}
