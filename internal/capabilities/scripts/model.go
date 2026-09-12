// Package scriptgeneration defines the pure domain model for the
// durable multi-stage script-generation workflow. It contains
// ZERO I/O, ZERO external dependencies — only types and pure
// functions.
//
// Architecture (per the verdetto):
//
//	internal/scriptgeneration/
//	    model.go        — GenerateRequest, Scene, GenerateResult (aggregates)
//	    model_values.go — Language, Source, Clip/Audio/DocumentReference
//	    model_render.go — RenderReference, RenderArtifact, audio projections
//	    ports*.go       — Technology-independent interfaces
//	    service.go      — Linear orchestrator (Start)
//	    runner*.go      — Durable stage-based execution with checkpoint
//
// The HTTP layer (internal/capabilities/script) calls service.Start; the
// runner owns the durable background execution. No I/O happens
// inside the builder (ingress registry) — the builder is demoted
//
// model.go was split three ways on 2026-09-12 (values | render | aggregates)
// to keep every file under max_lines_per_file_strict=600 (godlike/08).
package scriptgeneration

import (
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Domain aggregates ───────────────────────────────────────────────

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
	// OverlayBackground is the visual background selected by script.generate;
	// it is transported into the sealed OverlayPlan at render time.
	OverlayBackground *scriptpkg.OverlayBackgroundSpec `json:"overlay_background,omitempty"`
	OverlayStyle      *scriptpkg.OverlayStyleSpec      `json:"overlay_style,omitempty"`
	Audio             capabilityaudio.AudioMode        `json:"audio_mode,omitempty"`
	// Timing is the canonical voiceover timing policy nested inside the
	// audio config (wire key "timing"). nil means the pipeline applies the
	// canonical defaults (best_effort / word / [json]) — timing capture is
	// never implicitly mandatory. Forwarded to the per-scene VoiceoverInput
	// so the required/best-effort fail-closed semantics are honoured
	// end-to-end by the per-item voiceover pipeline.
	Timing *capabilityaudio.TimingRequest `json:"voiceover_timing,omitempty"`
	// MixPolicy is the editorial mix decision requested by the caller
	// (audio.mix_policy). Empty means no policy (legacy full-volume
	// overlap). The wire alias "voiceover_with_ducked_clip" is normalized
	// to the canonical VOICEOVER_DUCKED_CLIP by AudioMixPolicy.Normalize
	// when the plan is compiled.
	MixPolicy capabilityaudio.AudioMixPolicy `json:"mix_policy,omitempty"`
	// BackgroundMusic is the normalized list of BGM layer intents. It is
	// ALWAYS a slice in the domain, even when the wire carried a single
	// object (AudioOutputConfig.UnmarshalJSON normalizes at the boundary)
	// — supporting multiple segmented musics later needs no schema change.
	// Entries reference assets by asset_id only; resolution to physical
	// paths happens downstream.
	BackgroundMusic []scriptpkg.BackgroundMusicIntent `json:"background_music,omitempty"`
	// SoundEffects is the list of SFX intents, placed at absolute timeline
	// offsets or relative to a scene (anchor + offset). Entries reference
	// assets by asset_id only.
	SoundEffects []scriptpkg.SoundEffectIntent `json:"sound_effects,omitempty"`
	// IdempotencyKey is the caller-supplied idempotency key.
	IdempotencyKey string `json:"idempotency_key"`

	// ForceRefresh requests a new run when the idempotency key already
	// has an associated run. It mirrors the submission-layer intent so
	// the run ledger does not create duplicates on replay.
	ForceRefresh bool `json:"force_refresh,omitempty"`

	// Source describes the generation input source.
	Source Source `json:"source"`
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

	// Docs is the explicit document publishing config.
	// Verdetto: document creation MUST be explicit (docs.enabled),
	// NOT implicit based on whether drive_output_folder is present.
	// One document per language is created, not one bilingual doc.
	Docs DocumentsConfig `json:"docs"`

	// SaveToDB requests persistence of the generated script through the
	// canonical SQLite script writer. It is deliberately carried on the
	// durable capability request instead of being re-derived downstream.
	SaveToDB bool `json:"save_to_db,omitempty"`

	// DEPRECATED: use Docs.Enabled instead. Kept for backward compat.
	// Remove after all callers migrate to the Docs config struct.
	DocsEnabled bool `json:"docs_enabled,omitempty"`

	// DEPRECATED: use Docs.FolderID instead. Kept for backward compat.
	// Remove after all callers migrate to the Docs config struct.
	DriveFolderID string `json:"drive_folder_id,omitempty"`

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

	// VoiceoverFolderID is the explicit Drive folder for voiceover
	// artifacts supplied by the caller (output.voiceover_folder_id).
	// Empty means "use the configured default" (drive.voiceover_root_folder).
	// It is resolved ONCE by BuildGenerateRequest from the generation
	// input and threaded verbatim through the routing context into the
	// per-scene TTS command so a caller-explicit destination is never
	// replaced by the configured default (mirror of plan.VoiceoverFolderID
	// honored by the legacy processor_voiceover path).
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"`

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
	ID         string `json:"id"`
	Index      int    `json:"index"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	// DurationUS is the sealed internal timing value. DurationMS remains only
	// as a legacy wire field at the boundary.
	DurationUS int64                       `json:"duration_us,omitempty"`
	Clip       *ClipReference              `json:"clip,omitempty"`
	Clips      []*ClipReference            `json:"clips,omitempty"`
	Text       map[Language]string         `json:"text"`
	Voiceover  map[Language]AudioReference `json:"voiceover,omitempty"`

	// ExecutionMode is the canonical authorization boundary shared with
	// SpecScene. Empty is generated for backward compatibility.
	ExecutionMode scriptpkg.SceneExecutionMode `json:"execution_mode,omitempty"`
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

	Audio        capabilityaudio.AudioIntent   `json:"audio"`
	AudioIntents []capabilityaudio.AudioIntent `json:"audio_intents,omitempty"`
	// Annotations is the deterministic scene-local semantic surface
	// (primary/secondary entities grounded in this scene's text). It is
	// projected from the VidRush segment enrichment results and surfaced
	// verbatim in the document SpecScene; nil when no enrichment produced
	// entities for this scene.
	Annotations *scriptpkg.SceneAnnotations `json:"annotations,omitempty"`

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
