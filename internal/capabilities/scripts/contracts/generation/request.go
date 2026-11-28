package generation

import (
	scenecontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Request is the generation-phase projection of GenerateRequest: editorial
// identity, translation policy, channel, languages, visual/motion budget and
// the ordered script spec. Source acquisition, submission identity, audio
// configuration, publication and final-job opt-in stay in their own phases;
// they are deliberately absent so this contract cannot become a second god
// model.
type Request struct {
	TranslationProvider    string                    `json:"translation_provider,omitempty"`
	TranslationModel       string                    `json:"translation_model,omitempty"`
	TranslationConcurrency int                       `json:"translation_concurrency,omitempty"`
	Model                  string                    `json:"model,omitempty"`
	Tone                   string                    `json:"tone,omitempty"`
	Style                  string                    `json:"style,omitempty"`
	Render                 scriptpkg.VideoRenderSpec `json:"render,omitempty"`
	ChannelID              string                    `json:"channel_id,omitempty"`

	ShortPhraseMotion             string                                         `json:"short_phrase_motion,omitempty"`
	PhraseMotionFamily            string                                         `json:"phrase_motion_family,omitempty"`
	ChapterOverlay                *scriptpkg.ChapterOverlaySpec                  `json:"chapter_overlay,omitempty"`
	EditorialBulletStyles         []scriptpkg.EditorialBulletStyle               `json:"editorial_bullet_styles,omitempty"`
	DateMotionFamily              string                                         `json:"date_motion_family,omitempty"`
	MotionSeed                    string                                         `json:"motion_seed,omitempty"`
	ImageMotions                  []string                                       `json:"image_motions,omitempty"`
	ImageWithTextMotions          []string                                       `json:"image_with_text_motions,omitempty"`
	EntityImageMotions            []string                                       `json:"entity_image_motions,omitempty"`
	EntityCaptionMotions          []string                                       `json:"entity_caption_motions,omitempty"`
	RandomLightLeakOnClipStart    bool                                           `json:"random_light_leak_on_clip_start,omitempty"`
	RandomLightLeakOnOverlayStart bool                                           `json:"random_light_leak_on_overlay_start,omitempty"`
	RandomImageEffects            bool                                           `json:"random_image_effects,omitempty"`
	OverlayCategories             map[scriptpkg.OverlayCategory]scriptpkg.Toggle `json:"overlay_categories,omitempty"`
	OverlayImages                 []scriptpkg.OverlayImageAsset                  `json:"overlay_images,omitempty"`

	MapType                  string                           `json:"map_type,omitempty"`
	MapCameraAnimation       string                           `json:"map_camera_animation,omitempty"`
	MapLabelAnimation        string                           `json:"map_label_animation,omitempty"`
	MapAnimation             string                           `json:"map_animation,omitempty"`
	MapLevel                 string                           `json:"map_level,omitempty"`
	MapPresentation          *scriptpkg.MapPresentation       `json:"map_presentation,omitempty"`
	MaxPhraseOverlays        int                              `json:"max_phrase_overlays,omitempty"`
	MaxOverlaysPerScene      int                              `json:"max_overlays_per_scene,omitempty"`
	MapsOnly                 bool                             `json:"maps_only,omitempty"`
	MaxMapOverlays           int                              `json:"max_map_overlays,omitempty"`
	MaxImageOverlays         int                              `json:"max_image_overlays,omitempty"`
	EditorialDensity         string                           `json:"editorial_density,omitempty"`
	AnimationCounts          map[string]int                   `json:"animation_counts,omitempty"`
	OverlayGroupLimits       map[string]int                   `json:"overlay_group_limits,omitempty"`
	EntityStyleID            string                           `json:"entity_style_id,omitempty"`
	EntityBadgeColor         string                           `json:"entity_badge_color,omitempty"`
	DisableNumberOverlays    bool                             `json:"disable_number_overlays,omitempty"`
	DisableRecapOverlays     bool                             `json:"disable_recap_overlays,omitempty"`
	DisableChartOverlays     bool                             `json:"disable_chart_overlays,omitempty"`
	DisableEditorialOverlays bool                             `json:"disable_editorial_overlays,omitempty"`
	EnableCharts             bool                             `json:"enable_charts,omitempty"`
	HeavyPhrasePriority      float64                          `json:"heavy_phrase_priority,omitempty"`
	OverlayBackground        *scriptpkg.OverlayBackgroundSpec `json:"overlay_background,omitempty"`
	OverlayStyle             *scriptpkg.OverlayStyleSpec      `json:"overlay_style,omitempty"`

	ScriptParams        scriptpkg.ScriptSpec     `json:"script_params,omitempty"`
	SourceLanguage      scenecontract.Language   `json:"source_language"`
	Languages           []scenecontract.Language `json:"languages"`
	GenerateTimeline    bool                     `json:"generate_timeline,omitempty"`
	EnforceClipIdentity bool                     `json:"enforce_clip_identity,omitempty"`
	Title               string                   `json:"title,omitempty"`
	Project             string                   `json:"project,omitempty"`
	OutputName          string                   `json:"output_name,omitempty"`
	Intro               *scriptpkg.FixedSection  `json:"intro,omitempty"`
	Outro               *scriptpkg.FixedSection  `json:"outro,omitempty"`
}

// Output is the canonical plain-text projection of the generated narration.
// It is owned here so generation consumers do not import the root aggregate.
type Output struct {
	Text                       string `json:"text"`
	WordCount                  int    `json:"word_count"`
	SourceLanguageFallbackUsed bool   `json:"-"`
}
