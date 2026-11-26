package contract

import (
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// GenerateRequest is the pure-domain input for a script generation.
// The builder (ingress registry) is demoted to producing exactly this
// type from raw payload — no network, no database, no Google Drive.
//
// Verdetto invariant: the builder must limit itself to:
//
//	func BuildGenerateRequest(raw map[string]any) (GenerateRequest, error)
//
// Zero I/O in the builder.
type GenerateRequest struct {
	TranslationProvider    string `json:"translation_provider,omitempty"`
	TranslationModel       string `json:"translation_model,omitempty"`
	TranslationConcurrency int    `json:"translation_concurrency,omitempty"`
	// Model is the explicit per-item LLM model selected at the envelope
	// boundary. Empty means the runtime may apply its configured default.
	Model string `json:"model,omitempty"`
	// Tone and Style are caller editorial identity fields. They must remain
	// distinct from overlay styling and survive the durable request boundary.
	Tone  string `json:"tone,omitempty"`
	Style string `json:"style,omitempty"`
	// Render is the explicit per-clip reconstruction request. It is copied
	// once at ingress and consumed by the localized render fan-out.
	Render scriptpkg.VideoRenderSpec `json:"render,omitempty"`
	// ChannelID is the editorial channel this request is produced for (the
	// canonical YouTube channel id, or any operator-defined channel key).
	// Carried verbatim from the envelope item; empty means "no channel
	// profile applies".
	ChannelID string `json:"channel_id,omitempty"`
	// ShortPhraseMotion pins one animation for every 1-5-word phrase. A pin
	// bypasses the seeded per-word rotation; empty keeps the registry's
	// buckets. Validation happens at plan compilation.
	ShortPhraseMotion  string `json:"short_phrase_motion,omitempty"`
	PhraseMotionFamily string `json:"phrase_motion_family,omitempty"`
	// EditorialBulletStyles pins font and entrance motion per editorial
	// bullet (one bullet per scene). Empty keeps the presentation defaults.
	EditorialBulletStyles []scriptpkg.EditorialBulletStyle `json:"editorial_bullet_styles,omitempty"`
	// DateMotionFamily narrows date-card motion selection to a certified
	// ChrononTemplate date family (auto, date_v1, didone, typewriter).
	DateMotionFamily string `json:"date_motion_family,omitempty"`
	// MotionSeed is the per-render entropy for automatic motion rotation.
	// Empty preserves the historical PlanID/VideoID behavior (byte-identical
	// retries); a fresh value per render rotates the catalogs.
	MotionSeed   string   `json:"motion_seed,omitempty"`
	ImageMotions []string `json:"image_motions,omitempty"`
	// ImageWithTextMotions narrows the certified pool for captioned images.
	// Empty keeps the full seeded catalog.
	ImageWithTextMotions          []string                                       `json:"image_with_text_motions,omitempty"`
	EntityImageMotions            []string                                       `json:"entity_image_motions,omitempty"`
	EntityCaptionMotions          []string                                       `json:"entity_caption_motions,omitempty"`
	RandomLightLeakOnClipStart    bool                                           `json:"random_light_leak_on_clip_start,omitempty"`
	RandomLightLeakOnOverlayStart bool                                           `json:"random_light_leak_on_overlay_start,omitempty"`
	RandomImageEffects            bool                                           `json:"random_image_effects,omitempty"`
	OverlayCategories             map[scriptpkg.OverlayCategory]scriptpkg.Toggle `json:"overlay_categories,omitempty"`
	OverlayImages                 []scriptpkg.OverlayImageAsset                  `json:"overlay_images,omitempty"`
	MapType                       string                                         `json:"map_type,omitempty"`
	MapCameraAnimation            string                                         `json:"map_camera_animation,omitempty"`
	MapLabelAnimation             string                                         `json:"map_label_animation,omitempty"`
	MapAnimation                  string                                         `json:"map_animation,omitempty"`
	MapLevel                      string                                         `json:"map_level,omitempty"`
	// MapPresentation is the RESOLVED unified map configuration. When set it
	// is the single source of the four legacy map_* fields above plus the
	// grouping policy (group_nearby_places / max_pins_per_group / allow_route).
	MapPresentation *scriptpkg.MapPresentation `json:"map_presentation,omitempty"`
	// MaxPhraseOverlays is the caller-selected run-level ceiling for grounded
	// phrase overlays, carried verbatim from the envelope item. Zero keeps the
	// certified default; a positive value is honoured as-is.
	MaxPhraseOverlays int `json:"max_phrase_overlays,omitempty"`
	// MaxOverlaysPerScene limits the number of simultaneous editorial overlay
	// items admitted in each scene. Zero keeps the existing scene policy.
	MaxOverlaysPerScene int  `json:"max_overlays_per_scene,omitempty"`
	MapsOnly            bool `json:"maps_only,omitempty"`
	MaxMapOverlays      int  `json:"max_map_overlays,omitempty"`
	// MaxImageOverlays is the caller-selected run-level ceiling for all image
	// overlays, including scene-context and entity-bound imagery.
	MaxImageOverlays int `json:"max_image_overlays,omitempty"`
	// EditorialDensity is the single editorial budget preset (minimal,
	// balanced, dynamic). It derives unset per-kind caps; explicit caps win.
	EditorialDensity string         `json:"editorial_density,omitempty"`
	AnimationCounts  map[string]int `json:"animation_counts,omitempty"`
	// OverlayGroupLimits caps overlays per group (maps, numbers, short_phrases,
	// long_phrases, entities_with_text, images). Present 0 disables a group.
	OverlayGroupLimits map[string]int `json:"overlay_group_limits,omitempty"`
	// EntityStyleID pins the entity-card composition family for generated
	// entity cards. Resolved like ImageMotions: the envelope item may carry
	// it explicitly, and a channel profile fills the blank ("random" keeps
	// the certified full-registry sampling). Invalid selectors fail closed
	// at plan compilation.
	EntityStyleID string `json:"entity_style_id,omitempty"`
	// EntityBadgeColor enables the context plate behind a captioned entity card
	// and selects its background color. It is an optional #RRGGBB payload value.
	EntityBadgeColor      string `json:"entity_badge_color,omitempty"`
	DisableNumberOverlays bool   `json:"disable_number_overlays,omitempty"`
	DisableRecapOverlays  bool   `json:"disable_recap_overlays,omitempty"`
	DisableChartOverlays  bool   `json:"disable_chart_overlays,omitempty"`
	// DisableEditorialOverlays skips the automatic scene_title/scene_bullet
	// projection (projectEditorialOverlays). It exists for motion-catalog
	// runs where the operator-supplied grounded phrases must keep the run
	// budget: titles/bullets carry top scores (1.0/0.99) and would otherwise
	// crowd full-length phrases out of max_phrase_overlays, and their
	// truncated ≤7-word texts would fall into the short-phrase motion lane.
	// False by default: editorial cards render unless explicitly disabled.
	DisableEditorialOverlays bool `json:"disable_editorial_overlays,omitempty"`
	// EnableCharts is the explicit opt-in for chart overlays. Charts render
	// only when this is true and DisableChartOverlays is false.
	EnableCharts bool `json:"enable_charts,omitempty"`
	// HeavyPhrasePriority optionally overrides the editorial-weight threshold
	// for the prominent-motion lane. Zero keeps the certified default.
	HeavyPhrasePriority float64 `json:"heavy_phrase_priority,omitempty"`
	// OverlayBackground is the visual background selected by script.generate;
	// it is transported into the sealed OverlayPlan at render time.
	OverlayBackground *scriptpkg.OverlayBackgroundSpec `json:"overlay_background,omitempty"`
	OverlayStyle      *scriptpkg.OverlayStyleSpec      `json:"overlay_style,omitempty"`
	// AudioConfig is the audio-phase request contract: mode, voiceover
	// languages and timing, SFX/BGM intents, mix policy and publish target.
	// It is embedded so the durable request keeps its flat wire keys while
	// contracts/audio owns the fields.
	AudioConfig `json:",inline"`
	// IdempotencyKey is the caller-supplied idempotency key.
	IdempotencyKey string `json:"idempotency_key"`

	// ForceRefresh requests a new run when the idempotency key already
	// has an associated run. It mirrors the submission-layer intent so
	// the run ledger does not create duplicates on replay.
	ForceRefresh bool `json:"force_refresh,omitempty"`
	// FinalJob opts this run into the final-render handoff after the canonical
	// media plan is available: the Master owns the complete video, so the local
	// lane never assembles it. A run without FinalJob keeps the local lane.
	FinalJob bool `json:"final_job,omitempty"`
	// FinalJobRemote forces the handoff to the Master even when the local lane
	// could assemble the complete video, so a render-enabled run can opt out of
	// the local join and hand the complete video to the Master. Language fan-out
	// children also set it because their parent delivery requires a remote
	// receipt.
	FinalJobRemote bool `json:"final_job_remote,omitempty"`

	// Source describes the generation input source.
	Source Source `json:"source"`
	// MediaMode is the explicit stock/clip ownership contract carried from
	// script.generate into the durable runner.
	MediaMode scriptpkg.MediaMode `json:"media_mode,omitempty"`
	// StockBindings carries the caller-selected stock folders/assets into the
	// durable runner so acquisition can overlap scene generation.
	StockBindings []scriptpkg.StockBindingInput `json:"stock_bindings,omitempty"`
	// ScriptParams carries the canonical sizing and ordered segment contract
	// from the envelope into the durable runtime. Dropping it here makes the
	// scene planner fall back to an unbounded prose envelope.
	ScriptParams scriptpkg.ScriptSpec `json:"script_params,omitempty"`

	// MediaPlan carries the caller's visual media policy (provider toggles,
	// extraction limits, locked assignments). It is the same contract the
	// incremental VidRush coordinator consumes to run provider fan-out and
	// extraction with the caller's configured limits.
	MediaPlan mediadomain.MediaPlanSpec `json:"media_plan,omitempty"`

	// ExtractEntities carries the caller's entity-extraction intent
	// (output.extract_entities). ToggleDisabled skips the incremental VidRush
	// entity extraction (and its provider fan-out) for the run; ToggleDefault
	// and ToggleEnabled are resolved by NeedsSemanticEnrichment based on the
	// actual downstream consumers.
	ExtractEntities scriptpkg.Toggle `json:"extract_entities,omitempty"`
	// GenerateSceneImages is carried separately because image generation is a
	// semantic consumer even when named-entity extraction itself is omitted.
	GenerateSceneImages scriptpkg.Toggle `json:"generate_scene_images,omitempty"`

	// SourceLanguage is the primary language of the input (e.g. "en").
	// Scenes in this language are NOT translated.
	SourceLanguage Language `json:"source_language"`

	// Languages lists the target languages for translation and docs.
	Languages []Language `json:"languages"`

	// GenerateTimeline requests the canonical timeline metadata artifact
	// (scene durations, video segments) without binary render
	// materialization. PipelineGen is audio-only: a timeline is produced
	// from Drive-only clips (transcript + metadata) and there is no
	// video render toggle — the run stops at the certified final audio.
	GenerateTimeline bool `json:"generate_timeline,omitempty"`

	// EnforceClipIdentity promotes the scene↔clip identity gate from
	// report-only (metric + warning, no block) to fail-closed. Default
	// false: mismatches are recorded but do not fail the run, so the gate
	// can be validated on real traffic before it ever blocks.
	EnforceClipIdentity bool `json:"enforce_clip_identity,omitempty"`

	// Docs is the RETIRED Google Docs publication config. The capability has
	// been demolished; the field survives ONLY as a fail-closed tombstone so a
	// caller that still requests documents receives a typed error instead of a
	// silent no-op (NO-FAKE-AVAILABILITY). It stays JSON-decodable so existing
	// payloads are recognised, not ignored. The type is owned by
	// kernel/script (DocumentsSpec) so the envelope and the request cannot
	// drift into two tombstone contracts.
	Docs scriptpkg.DocumentsSpec `json:"docs"`

	// SaveToDB requests persistence of the generated script through the
	// canonical SQLite script writer. It is deliberately carried on the
	// durable capability request instead of being re-derived downstream.
	SaveToDB bool `json:"save_to_db,omitempty"`

	// DocsEnabled is the legacy (flat) tombstone of the retired Google Docs
	// capability. A true value fails closed exactly like Docs.Enabled.
	DocsEnabled bool `json:"docs_enabled,omitempty"`

	// Title is the output title (mirrors the caller's title).
	Title string `json:"title,omitempty"`

	// Project is the canonical semantic project namespace for artifact
	// routing (voiceover publish). It is resolved ONCE by
	// BuildGenerateRequest from the explicit generation input and propagated verbatim
	// downstream: runner → VoiceoverInput.Project → per-item command →
	// ProcessSegmentCommand.Project → VoiceoverPublishCommand.Project.
	// A voiceover-enabled generation with an empty Project fails closed
	// BEFORE the first TTS call (ErrProjectRequired) — no component may
	// silently invent a fallback namespace.
	Project string `json:"project,omitempty"`

	// OutputName is the caller-specified output filename.
	OutputName string `json:"output_name,omitempty"`

	// VoiceoverFolderID and TTSRate live in the embedded AudioConfig
	// (output.voiceover_folder_id / output.tts_rate at ingress). Keeping a
	// second declaration here would shadow the contract at the JSON boundary.

	// Intro is a literal intro section prepended verbatim. Never sent to
	// the LLM — the runner injects it as the first protected fixed_media scene
	// (role=opening) with the supplied clip bindings and original clip audio.
	// It carries no speakable text: only optional display_text.
	Intro *scriptpkg.FixedSection `json:"intro,omitempty"`

	// Outro is a literal outro section appended verbatim. Same literal
	// contract as Intro — injected as the last scene (kind=outro).
	Outro *scriptpkg.FixedSection `json:"outro,omitempty"`
}

// Scene represents a single scene within the generated output.
// Each scene has a deterministic ID, an ordered index, an optional
// clip reference, generated text per language, and an optional
// voiceover per language.
type Scene struct {
	ID                 string `json:"id"`
	Index              int    `json:"index"`
	DurationMS         int64  `json:"duration_ms,omitempty"`
	ShortPhraseOverlay bool   `json:"short_phrase_overlay,omitempty"`
	// DurationUS is the sealed internal timing value. DurationMS remains only
	// as a legacy wire field at the boundary.
	DurationUS        int64                                          `json:"duration_us,omitempty"`
	OverlayCategories map[scriptpkg.OverlayCategory]scriptpkg.Toggle `json:"overlay_categories,omitempty"`
	Clip              *ClipReference                                 `json:"clip,omitempty"`
	Clips             []*ClipReference                               `json:"clips,omitempty"`
	// Stock preserves the caller-selected stock binding alongside Clip for
	// mixed media scenes.
	Stock     *scriptpkg.StockBinding     `json:"stock,omitempty"`
	Text      map[Language]string         `json:"text"`
	Voiceover map[Language]AudioReference `json:"voiceover,omitempty"`

	// ExecutionMode is the canonical authorization boundary shared with
	// SpecScene. Empty is generated for backward compatibility.
	ExecutionMode scriptpkg.SceneExecutionMode `json:"execution_mode,omitempty"`
	// NativeClip marks an editorial no_voiceover cutaway: the database clip is
	// shown natively with its original audio, never through the localized
	// subtitle-render lane. It is deliberately explicit because legacy fixed
	// media (intro/outro) does not always carry a Role.
	NativeClip bool `json:"native_clip,omitempty"`
	// Role is the explicit editorial timeline position (opening/body/closing).
	// It is descriptive metadata only — authorization always flows through
	// ExecutionMode. It replaces the old "intro/outro inferred from scene ID"
	// convention: an ID like scene-intro is identity, never behavior.
	Role scriptpkg.SceneRole `json:"role,omitempty"`
	// FixedPlayback is present only for protected fixed-media scenes and
	// carries the authoritative original-clip source window.
	FixedPlayback *scriptpkg.FixedPlaybackPolicy `json:"fixed_playback,omitempty"`

	// TextReadyAt is the wall-clock instant this scene's text became final
	// (the SceneTextReady boundary). TranslationStartedAt and TTSStartedAt
	// mark when this scene's downstream branches first began. Together they
	// make streaming overlap durable and provable: in a streaming run,
	// scene N's translation/TTS must start before scene N+1's text is ready.
	// They are zero in the batch path, where every scene becomes ready at
	// once and no per-scene ready boundary exists.
	TextReadyAt          time.Time `json:"text_ready_at,omitempty"`
	TranslationStartedAt time.Time `json:"translation_started_at,omitempty"`
	TTSStartedAt         time.Time `json:"tts_started_at,omitempty"`
	// TTSCompletedAt closes the per-scene voiceover boundary. With
	// TTSStartedAt it makes the synthesis duration observable scene by scene;
	// the batch path leaves both zero because it has no per-scene boundary.
	TTSCompletedAt time.Time `json:"tts_completed_at,omitempty"`

	Audio        capabilityaudio.AudioIntent   `json:"audio"`
	AudioIntents []capabilityaudio.AudioIntent `json:"audio_intents,omitempty"`
	// Annotations is the deterministic scene-local semantic surface
	// (primary/secondary entities grounded in this scene's text). It is
	// projected from the VidRush segment enrichment results and surfaced
	// verbatim in the document SpecScene; nil when no enrichment produced
	// entities for this scene.
	Annotations *scriptpkg.SceneAnnotations `json:"annotations,omitempty"`
	// LocalizedAnnotations contains the same grounded semantic surface for
	// each translated scene text. Annotations remains the source-language
	// compatibility surface used by overlays and existing consumers.
	LocalizedAnnotations map[Language]*scriptpkg.SceneAnnotations `json:"annotations_by_language,omitempty"`

	// Entities is the canonical per-scene entity extraction result using the
	// SAME EntityResult model as the document aggregate (persons / places /
	// concepts / important phrases / important words). It is populated
	// automatically right after scene text generation — per scene, never a
	// second endpoint that re-reads the script. A scene that legitimately
	// carries no entities keeps an explicit empty result (entities=[]) with
	// EntityOverlayRequired=false; an entity is never invented.
	Entities *scriptpkg.EntityResult `json:"entities,omitempty"`

	// EntityOverlayRequired is true when the scene carries at least one
	// entity that may drive an overlay intent. It is derived from Entities
	// (false when entities=[]), never invented.
	EntityOverlayRequired bool `json:"entity_overlay_required"`

	// VidRush carries the canonical per-segment VidRush result (insights,
	// asset candidates and selection) when a VidRush plan is active for this
	// scene. It is projected read-only into the job view; nil when no
	// enrichment produced a result for this scene.
	VidRush *scriptpkg.VidRushSegmentResult `json:"vidrush,omitempty"`
}

// FixedPlaybackSourceInMS returns the protected source-window start for
// document projections. Runtime audio intent is the fallback when the
// playback pointer is absent on a legacy scene.
func (s Scene) FixedPlaybackSourceInMS() int64 {
	if s.FixedPlayback != nil {
		return s.FixedPlayback.SourceInMS
	}
	return s.Audio.SourceInUS / 1000
}

// FixedPlaybackSourceOutMS returns the protected source-window end for
// document projections. A zero value means the complete source clip.
func (s Scene) FixedPlaybackSourceOutMS() int64 {
	if s.FixedPlayback != nil {
		return s.FixedPlayback.SourceOutMS
	}
	if s.Audio.SourceDurationUS > 0 {
		return s.FixedPlaybackSourceInMS() + s.Audio.SourceDurationUS/1000
	}
	return 0
}

// GenerateOutput is the durable plain-text projection of the generated
// narration. Scenes remain the structured source for timeline work; this
// field keeps the canonical output.text/word_count contract available to
// durable workers and API consumers.
type GenerateOutput struct {
	Text      string `json:"text"`
	WordCount int    `json:"word_count"`
	// SourceLanguageFallbackUsed records that at least one scene had no text
	// for the requested language and a first-available language was used
	// instead. It is an internal observability signal (not part of the
	// durable output.text contract) so the runner can warn and count it.
	SourceLanguageFallbackUsed bool `json:"-"`
}
