// Package scriptgeneration defines the pure domain model for the
// durable multi-stage script-generation workflow. It contains
// ZERO I/O, ZERO external dependencies — only types and pure
// functions.
//
// Architecture (per the verdetto):
//
//	internal/scriptgeneration/
//	    model.go      — GenerateRequest, Scene, GenerateResult and value types
//	    ports.go      — Technology-independent interfaces
//	    service.go    — Linear orchestrator (Start)
//	    runner.go     — Durable stage-based execution with checkpoint
//
// The HTTP layer (internal/api/script) calls service.Start; the
// runner owns the durable background execution. No I/O happens
// inside the builder (ingress registry) — the builder is demoted
// to pure payload transformation.
package scriptgeneration

import (
	"errors"
	"time"
)

import capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
import capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
import capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
import mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
import kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

// ── Value types ─────────────────────────────────────────────────────

// Language is an ISO 639-1 two-letter code (e.g. "en", "es").
type Language string

// Source describes where the generation input comes from.
type Source struct {
	Type               SourceType                  `json:"type"`
	Topic              string                      `json:"topic,omitempty"`
	SourceText         string                      `json:"source_text,omitempty"`
	ClipIDs            []string                    `json:"clip_ids,omitempty"`
	IntroClipIDs       []string                    `json:"intro_clip_ids,omitempty"`
	NumClips           int                         `json:"num_clips,omitempty"`
	Query              string                      `json:"query,omitempty"`
	MaxClips           int                         `json:"max_clips,omitempty"`
	MinCoverage        float64                     `json:"min_coverage,omitempty"`
	MinQualityScore    *float64                    `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int                        `json:"min_transcript_words,omitempty"`
	Guidelines         string                      `json:"guidelines,omitempty"`
	TranscriptPolicy   string                      `json:"transcript_policy,omitempty"`
	OrderingStrategy   string                      `json:"ordering_strategy,omitempty"`
	GroundingPolicy    string                      `json:"grounding_policy,omitempty"`
	FallbackPolicy     string                      `json:"fallback_policy,omitempty"`
	ForceRefresh       bool                        `json:"force_refresh,omitempty"`
	Search             bool                        `json:"search,omitempty"`
	AllowTextOnly      bool                        `json:"allow_text_only,omitempty"`
	SourceFilter       string                      `json:"source_filter,omitempty"`
	MediaTypeFilter    string                      `json:"media_type_filter,omitempty"`
	CachePolicy        scriptpkg.SourceCachePolicy `json:"cache,omitempty"`
	Research           scriptpkg.ResearchPolicy    `json:"research,omitempty"`
}

// SourceType enumerates the supported generation sources.
type SourceType string

const (
	SourceText    SourceType = "text"
	SourceClips   SourceType = "clips"
	SourceCatalog SourceType = "catalog"
	SourceSearch  SourceType = "search"
	SourceCurate  SourceType = "curate"
)

// ClipReference identifies a single media clip.
type ClipReference struct {
	ID        string `json:"id"`
	SourceID  string `json:"source_id,omitempty"`
	Title     string `json:"title,omitempty"`
	DriveLink string `json:"drive_link,omitempty"`
	// Duration is the legacy wire field carrying the asset total duration in
	// float seconds. DEPRECATED for internal computation — read DurationUS
	// (integer microseconds) + DurationSource (provenance) instead.
	Duration float64 `json:"duration,omitempty"` // seconds (legacy)
	// DurationUS is the canonical total duration of the complete source asset
	// in integer microseconds, resolved once at the media boundary.
	DurationUS int64 `json:"duration_us,omitempty"`
	// DurationSource is the canonical provenance of DurationUS
	// (probe / provider_metadata / unknown).
	DurationSource kernelasset.DurationSource `json:"duration_source,omitempty"`
	AudioAssetID   string                     `json:"audio_asset_id,omitempty"`
	AudioPath      string                     `json:"audio_path,omitempty"`
	Path           string                     `json:"path,omitempty"`
	SHA256         string                     `json:"sha256,omitempty"`
	FrameCount     int64                      `json:"frame_count,omitempty"`
	SourceInMS     int64                      `json:"source_in_ms,omitempty"`
	SourceOutMS    int64                      `json:"source_out_ms,omitempty"`
	// Subject identity of the clip, threaded from the canonical asset
	// metadata (ClipSemanticMetadata). The scene↔clip identity gate uses
	// the union of Speakers + MentionedPeople + Subject to certify that a
	// clip actually features the subject its scene narrates (the
	// "Tom Holland / Adam Sandler" class of error).
	Speakers        []string `json:"speakers,omitempty"`
	MentionedPeople []string `json:"mentioned_people,omitempty"`
	Subject         string   `json:"subject,omitempty"`
}

// AssetDuration resolves the canonical total duration of this clip with
// provenance, applying the contract invariants (positive known values,
// explicit unknown — never a fabricated 0). DurationUS + DurationSource are
// the canonical fields; a legacy caller that only populated Duration (no
// source) is mapped to provider_metadata — the pre-contract value came from
// asset metadata, not a fresh local probe — so it stays known while remaining
// distinguishable from a real probe measurement.
func (c *ClipReference) AssetDuration() kernelasset.AssetDuration {
	if c == nil {
		return kernelasset.UnknownDuration()
	}
	us := c.DurationUS
	if us <= 0 && c.Duration > 0 {
		us = checkedFloatSeconds(c.Duration, "clip duration")
	}
	if us <= 0 {
		return kernelasset.UnknownDuration()
	}
	switch c.DurationSource {
	case kernelasset.DurationProbe:
		return kernelasset.ProbedDuration(us)
	case kernelasset.DurationProvider:
		return kernelasset.ProviderDuration(us)
	case kernelasset.DurationUnknown:
		return kernelasset.UnknownDuration()
	default:
		// Legacy unprovenanced value: known but not a fresh probe.
		return kernelasset.ProviderDuration(us)
	}
}

// AudioReference identifies a generated voiceover audio asset.
type AudioReference struct {
	ID       string  `json:"id"`
	URL      string  `json:"url,omitempty"`
	FilePath string  `json:"file_path,omitempty"`
	Duration float64 `json:"duration,omitempty"` // seconds
	// Timing is the canonical word-level timing captured in the SAME
	// synthesis stream that produced the audio (the Edge WordBoundary
	// payload). It is the SSOT from which the phrase→timestamp projection
	// (GenerateResult.PhraseTimings) is derived. Nil when timing capture
	// was not requested or unavailable for this voiceover.
	Timing *capabilityaudio.SpeechTimingArtifact `json:"timing,omitempty"`
	// TimingBundle carries the published timing bundle references
	// (timing.json SSOT + optional SRT/VTT links + hashes) for this
	// voiceover language. It is the document-facing summary; the word-level
	// SSOT stays in Timing (never inlined). Nil when no timing bundle was
	// published (timing disabled / unavailable / failed).
	TimingBundle *scriptpkg.VoiceoverTimingBinding `json:"timing_bundle,omitempty"`
}

// DocumentReference identifies a published Google Doc.
type DocumentReference struct {
	ID   string `json:"id"`
	Link string `json:"link"`
}

// RenderReference identifies a completed RenderingGen queue job (the future
// Chronon overlay render path) and carries the certified artifact the
// downstream document/assembly steps consume. It is retained for that path
// and is NOT part of the removed video render pipeline. The Artifact field is
// nil while the job is still queued or running.
type RenderReference struct {
	JobID    string          `json:"job_id"`
	Status   string          `json:"status"`
	Artifact *RenderArtifact `json:"artifact,omitempty"`
}

// RenderArtifact is the certified artifact produced by the central
// RenderingGen queue. It mirrors the queue's artifact contract (including the
// copy-only certification) so the document renderer and Velox copy assembly
// consume the same immutable reference without probing the file themselves.
type RenderArtifact struct {
	ID                 string `json:"id,omitempty"`
	Kind               string `json:"kind,omitempty"`
	StorageKey         string `json:"storage_key,omitempty"`
	URL                string `json:"url,omitempty"`
	SHA256             string `json:"sha256,omitempty"`
	MimeType           string `json:"mime_type,omitempty"`
	SizeBytes          int64  `json:"size_bytes,omitempty"`
	Width              int    `json:"width,omitempty"`
	Height             int    `json:"height,omitempty"`
	FPSNum             int    `json:"fps_num,omitempty"`
	FPSDen             int    `json:"fps_den,omitempty"`
	FrameCount         int    `json:"frame_count,omitempty"`
	DurationUS         int64  `json:"duration_us,omitempty"`
	ProfileID          string `json:"profile_id,omitempty"`
	CopyEligible       bool   `json:"copy_eligible,omitempty"`
	Codec              string `json:"codec,omitempty"`
	CodecProfile       string `json:"codec_profile,omitempty"`
	ClosedGOP          bool   `json:"closed_gop,omitempty"`
	FirstFrameKeyframe bool   `json:"first_frame_keyframe,omitempty"`
	// RenderMS and EncodeMS are the worker-measured wall durations of the
	// Chronon render and encode phases (from the queue artifact's metrics
	// map: render_ms / encode_ms). Zero means the worker did not report them.
	RenderMS int64 `json:"render_ms,omitempty"`
	EncodeMS int64 `json:"encode_ms,omitempty"`
	// DriveFileID and DriveLink are the Google Drive publication identity of
	// the rendered artifact (populated by the worker's publish phase). Empty
	// when the artifact was not published to Drive.
	DriveFileID string `json:"drive_file_id,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
}

type FinalAudioReference struct {
	AssetID              string `json:"audio_asset_id"`
	Path                 string `json:"path,omitempty"`
	DriveLink            string `json:"drive_link,omitempty"`
	Container            string `json:"container,omitempty"`
	AudioContractVersion string `json:"audio_contract_version,omitempty"`
	AudioPlanVersion     string `json:"audio_plan_version,omitempty"`
	PlanSHA256           string `json:"audio_plan_sha256"`
	FinalAudioSHA256     string `json:"final_audio_sha256"`
	Codec                string `json:"codec,omitempty"`
	Profile              string `json:"profile,omitempty"`
	SampleRate           int    `json:"sample_rate,omitempty"`
	Channels             int    `json:"channels,omitempty"`
	ChannelLayout        string `json:"channel_layout,omitempty"`
	Bitrate              int64  `json:"bitrate,omitempty"`
	DurationUS           int64  `json:"duration_us,omitempty"`
	DurationMS           int64  `json:"duration_ms"`
	StartPTS             int64  `json:"start_pts,omitempty"`
	SizeBytes            int64  `json:"size_bytes,omitempty"`
	FinalMix             bool   `json:"final_mix,omitempty"`
	CopyEligible         bool   `json:"copy_eligible"`
}

// AudioPipelineMetrics is an audio-friendly API projection of canonical
// observability stages and operations. It is not an authority and must not
// acquire independent timers or persistence writers.
//
// Relationship to the legacy domain contract: internal/domain/script
// .GenerationTimings carries an overlapping set of flat *_ms audio fields used
// only by the migration-only internal/application/scripts/usecase path. That
// struct is a legacy projection; this struct is the authority. Field map:
//
//	GenerationTimings (domain, legacy)  AudioPipelineMetrics (projection)
//	tts_total_ms                          tts_ms
//	audio_mix_ms                          mix_ms
//	audio_encode_ms                       aac_encode_ms
//	audio_probe_ms                        probe_ms
//	audio_hash_ms                         hash_ms
//	audio_pipeline_total_ms               total_ms
//	final_audio_duration_ms               audio_duration_ms
//	audio_encode_passes                   audio_encode_passes (unchanged)
//	tts_calls                             tts_calls (unchanged)
//	timeline_compile_ms                   timeline_compile_ms (unchanged)
//	audio_plan_compile_ms                 audio_plan_compile_ms (unchanged)
//	clip_audio_prepare_ms                 clip_audio_prepare_ms (unchanged)
//
// Projection-only fields with no legacy equivalent: media_fetch_ms, upload_ms,
// audio_rtf, audio_speed, tts_scenes.
//
// Do NOT add new timing fields here as an authority; derive them from the
// canonical RunReport and keep this struct stable for API consumers.
type AudioPipelineMetrics struct {
	TTSMS              int64             `json:"tts_ms"`
	MediaFetchMS       int64             `json:"media_fetch_ms"`
	TimelineCompileMS  int64             `json:"timeline_compile_ms"`
	AudioPlanCompileMS int64             `json:"audio_plan_compile_ms"`
	ClipAudioPrepareMS int64             `json:"clip_audio_prepare_ms"`
	MixMS              int64             `json:"mix_ms"`
	AACEncodeMS        int64             `json:"aac_encode_ms"`
	ProbeMS            int64             `json:"probe_ms"`
	HashMS             int64             `json:"hash_ms"`
	UploadMS           int64             `json:"upload_ms"`
	TotalMS            int64             `json:"total_ms"`
	AudioDurationMS    int64             `json:"audio_duration_ms"`
	TTSCalls           int               `json:"tts_calls"`
	AudioRTF           float64           `json:"audio_rtf"`
	AudioSpeed         float64           `json:"audio_speed"`
	AudioEncodePasses  int               `json:"audio_encode_passes"`
	TTSScenes          []TTSSSceneMetric `json:"tts_scenes,omitempty"`
}

type TTSSSceneMetric struct {
	SceneID          string   `json:"scene_id"`
	Language         Language `json:"language"`
	DurationMS       int64    `json:"duration_ms"`
	Characters       int      `json:"characters"`
	Words            int      `json:"words"`
	OutputDurationMS int64    `json:"output_duration_ms,omitempty"`
}

// ── DocumentsConfig ──────────────────────────────────────────────────

// DocumentsConfig is the explicit contract for Google Doc publishing.
// Per the verdetto, document creation MUST NOT be implicit based on
// whether drive_output_folder happens to be present — it must be
// explicitly requested via this config.
//
// One document per language is created (e.g. "script_en", "script_es").
// The identity is deterministic: (generation_run_id + language) drive
// properties. On retry, UpsertDocument updates the same document.
type DocumentsConfig struct {
	// Enabled explicitly requests Google Doc publishing.
	// When false, no documents are created even if Languages or
	// FolderID are populated. Default false (opt-in).
	Enabled bool `json:"enabled"`

	// Languages lists the languages for which a document should be
	// published. Each language gets its own document (one per language,
	// NOT one bilingual document). Must be non-empty when Enabled is
	// true.
	Languages []Language `json:"languages,omitempty"`

	// FolderID is the target Google Drive folder ID for documents.
	// When empty, documents are created in the default Drive location.
	FolderID string `json:"folder_id,omitempty"`
}

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
	Audio capabilityaudio.AudioMode `json:"audio_mode,omitempty"`
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
	// and ToggleEnabled preserve the canonical always-extract behavior.
	ExtractEntities scriptpkg.Toggle `json:"extract_entities,omitempty"`

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
}

// GenerateOutput is the durable plain-text projection of the generated
// narration. Scenes remain the structured source for timeline work; this
// field keeps the canonical output.text/word_count contract available to
// durable workers and API consumers.
type GenerateOutput struct {
	Text      string `json:"text"`
	WordCount int    `json:"word_count"`
}

// GenerateResult is the complete output of a script generation run.
// It carries every artifact produced by the workflow.
type GenerateResult struct {
	// Output is the canonical plain-text result projection. It is derived once
	// from the ordered scenes and is never independently generated.
	Output GenerateOutput `json:"output"`
	// SourceTrace is the durable retrieval trace. It is kept alongside the
	// capability result so the broker/API cannot lose the accepted Qdrant
	// clip IDs between the durable runner and the legacy job envelope.
	SourceTrace scriptpkg.SourceTrace `json:"source,omitempty"`
	// Scenes is the ordered list of generated scenes.
	Scenes []Scene `json:"scenes"`

	// Segments is the compatibility projection of the canonical VidRush
	// enrichment results. Each entry preserves insights.entities for legacy
	// consumers; it is never a second extraction source.
	Segments []scriptpkg.VidRushSegmentResult `json:"segments,omitempty"`

	// Artifacts contains compatibility projections derived from this result.
	Artifacts *GenerateArtifacts `json:"artifacts,omitempty"`

	// ResolvedScenes is the sealed technical projection consumed by canonical
	// timeline/audio/render compilation. Scenes remain editorial input.
	ResolvedScenes []ResolvedScene `json:"resolved_scenes,omitempty"`

	// CanonicalTimeline and AudioPlan are persisted so the renderer and
	// audio compiler consume the same timing decision rather than deriving
	// independent offsets on either side of the enqueue boundary.
	CanonicalTimeline *capabilityaudio.CanonicalTimeline `json:"canonical_timeline,omitempty"`
	AudioPlan         *capabilityaudio.CompiledAudioPlan `json:"audio_plan,omitempty"`

	// PhraseTimings is the deterministic per-scene phrase→timestamp
	// projection. Each entry anchors one script phrase to the canonical
	// word timing (local span) and the final combined timeline (global
	// span = the scene's canonical timeline offset + local span). It is a
	// read-only projection of the canonical SpeechTimingArtifact files —
	// recomputable and verifiable, never a source of truth.
	PhraseTimings []capabilityaudio.PhraseTiming `json:"phrase_timings,omitempty"`

	// SceneSpeechTimings is the scene-level speech timing projection: one
	// entry per scene that captured canonical word timing, bundling that
	// scene's word boundaries with its derived phrase spans. It is the
	// durable, document-facing mirror of PhraseTimings (the flat projection);
	// both are read-only projections of the canonical SpeechTimingArtifact
	// files, never a source of truth.
	SceneSpeechTimings []capabilityaudio.SceneSpeechTiming `json:"scene_speech_timings,omitempty"`

	// Entities is the deterministic typed entity aggregate (persons /
	// places / concepts) projected from the run's VidRush segment
	// enrichment results. It is the durable-surface mirror of the legacy
	// Artifacts.Entities block: consumers read the typed fields and never
	// parse raw JSON. Nil when the incremental enrichment plane did not
	// run (or produced no entities).
	Entities *scriptpkg.EntityResult `json:"entities,omitempty"`

	// ScriptID is the canonical SQLite scripts-row identifier returned by
	// the single persistence port. Zero means persistence was not requested.
	ScriptID int64 `json:"script_id,omitempty"`

	// EntityTimeline is the canonical entity→timestamp projection: every
	// entity occurrence of the source language is anchored to the real word
	// timing of the voiceover actually used (audio boundaries come from the
	// SpeechTimingArtifact, never from text-length estimates) and mapped onto
	// the final combined timeline via the scene's canonical offset. It is
	// the SSOT the overlay resolver reads to place entity cards. Nil when no
	// scene carried both annotations and word timing.
	EntityTimeline *capabilityentities.EntityTimeline `json:"entity_timeline,omitempty"`

	// OverlayPlan is the semantic overlay plan derived from the run's
	// certified timing surfaces (phrase timings + entity timeline + scene
	// annotations): IMPORTANT_PHRASE / IMPORTANT_WORD / IMAGE_OVERLAY /
	// PERSON / NUMBER / QUOTE / LOCATION / PRODUCT / LOGO, each terminating
	// in a canonical primitive (Text / Image / Video / Shape) when compiled
	// via overlays.CompileChrononPlan. It is the PipelineGen-side
	// instruction set for the RenderingGen queue. Nil when the run carried
	// no derivable overlay surface.
	OverlayPlan *capabilityoverlay.OverlayPlan `json:"overlay_plan,omitempty"`

	// OverlayRender is populated after the timing-frozen Chronon render has
	// completed and its media contract has been certified.
	OverlayRender *RenderReference `json:"overlay_render,omitempty"`

	// OverlayIntents are the pre-timing entity→template bindings, created
	// immediately after entity extraction. Each intent binds one entity
	// occurrence to its resolved template without timing dependency,
	// enabling overlay.prepare to start template resolution and asset
	// prefetch in parallel with TTS. Empty when no entities were extracted.
	OverlayIntents []capabilityoverlay.OverlayIntent `json:"overlay_intents,omitempty"`

	// EditingTimeline is the canonical editing projection built from frozen
	// facts (CanonicalTimeline + FinalAudio + OverlayPlan + EntityTimeline).
	// It is the single JSON document downstream editing consumes; no
	// component maintains a second independently calculated timeline.
	EditingTimeline *EditingTimelineV1 `json:"editing_timeline,omitempty"`

	// Documents maps each language to the published Google Doc.
	Documents               map[Language]DocumentReference `json:"documents,omitempty"`
	DocumentRenderers       map[Language]string            `json:"document_renderers,omitempty"`
	DocumentSpecSceneSHA256 map[Language]string            `json:"document_specscene_sha256,omitempty"`
	DocumentSceneCounts     map[Language]int               `json:"document_scene_counts,omitempty"`

	// Title is the output title (mirrors GenerateRequest.Title).
	Title string `json:"title,omitempty"`

	// OutputName is the caller-specified output name.
	OutputName string `json:"output_name,omitempty"`

	// WordCount is the total generated word count.
	WordCount      int    `json:"word_count"`
	VoiceoverGroup string `json:"voiceover_group,omitempty"`

	AudioMode          capabilityaudio.AudioMode           `json:"audio_mode,omitempty"`
	AudioStrategy      capabilityaudio.AudioRenderStrategy `json:"audio_strategy,omitempty"`
	FinalAudio         *FinalAudioReference                `json:"final_audio,omitempty"`
	AudioMetrics       *AudioPipelineMetrics               `json:"audio_metrics,omitempty"`
	TranslationMetrics *TranslationPipelineMetrics         `json:"translation_metrics,omitempty"`
}

// TranslationPipelineMetrics is a read-only API projection of canonical
// translation operations. The RunReport/observability database is the only
// measurement authority.
type TranslationPipelineMetrics struct {
	Calls       int   `json:"calls"`
	Concurrency int   `json:"concurrency"`
	WallMS      int64 `json:"wall_ms"`
}

// GenerationRun is the canonical aggregate that tracks a single
// execution of the script generation workflow. It is created BEFORE
// any external I/O (verdetto invariant: the POST handler creates
// the pipeline_run first).
type GenerationRun struct {
	// ID is the canonical run identifier (pipeline_run.id).
	ID string `json:"id"`

	// JobID is the canonical worker-assigned job identifier, set
	// after the submission service returns. Used by GET /full to
	// correlate the job with its generation run.
	JobID string `json:"job_id,omitempty"`

	// Request is the original generation request.
	Request GenerateRequest `json:"request"`

	// Status is the high-level lifecycle status.
	Status RunStatus `json:"status"`

	// CurrentStage identifies the exact workflow phase.
	CurrentStage Stage `json:"current_stage"`

	// Result is populated when the run reaches a terminal state.
	Result *GenerateResult `json:"result,omitempty"`

	// ErrorCode, ErrorMessage, and FailedStage capture the failure reason.
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	FailedStage  Stage  `json:"failed_stage,omitempty"`

	// RetryCount tracks how many times this run has been retried.
	AttemptCount int `json:"attempt_count"`

	// NextRetryAt is the earliest time a retry should be attempted.
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`

	// CreatedAt and UpdatedAt track the run lifecycle.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RunStatus is the high-level lifecycle status of a GenerationRun.
type RunStatus string

const (
	RunStatusPending   RunStatus = "PENDING"
	RunStatusRunning   RunStatus = "RUNNING"
	RunStatusCompleted RunStatus = "COMPLETED"
	RunStatusFailed    RunStatus = "FAILED"
)

// Stage identifies the exact phase within the workflow.
// Verdetto: extend CurrentStage with these explicit values.
type Stage string

const (
	StageNormalizing          Stage = "NORMALIZING"
	StageGeneratingSceneText  Stage = "GENERATING_SCENE_TEXT"
	StageTranslatingScenes    Stage = "TRANSLATING_SCENES"
	StageGeneratingVoiceovers Stage = "GENERATING_VOICEOVERS"
	StageCompilingAudio       Stage = "COMPILING_AUDIO"
	StagePublishingDocuments  Stage = "PUBLISHING_DOCUMENTS"
	StageCompleted            Stage = "COMPLETED"
	StageFailed               Stage = "FAILED"
)

// ErrProjectRequired is the typed sentinel surfaced when a
// voiceover-enabled generation reaches the voiceover phase with no resolved
// Project. The runner fails the run BEFORE the first TTS call instead of
// letting the publisher fail after TTS work is already spent
// (godlike/07 NO-FAKE-AVAILABILITY — no silent "scene" namespace fallback).
var ErrProjectRequired = errors.New("scriptgeneration: Project is required for voiceover-enabled generation (resolve it once via BuildGenerateRequest before the voiceover phase)")

// ErrMinimumTextGate identifies a durable generation that produced no usable
// narration or fewer words than the caller's explicit script_params.min_words.
var ErrMinimumTextGate = errors.New("scriptgeneration: generated text failed the minimum word gate")

// ── Document config helper ──────────────────────────────────────────

// EntityExtractionDisabled reports whether the caller explicitly disabled
// per-scene entity extraction via output.extract_entities=disabled. The
// incremental VidRush pipeline (entity extraction → provider fan-out) is
// skipped for such runs; ToggleDefault and ToggleEnabled preserve the
// canonical always-extract behavior.
func (req GenerateRequest) EntityExtractionDisabled() bool {
	return req.ExtractEntities == scriptpkg.ToggleDisabled
}

// ResolveDocsConfig resolves the effective document publishing config
// from a GenerateRequest, applying backward-compat fallback from the
// deprecated flat fields (DocsEnabled, DriveFolderID) to the canonical
// DocumentsConfig struct (Docs).
//
// Returns (enabled, languages, folderID):
//   - enabled: true when Docs.Enabled || DocsEnabled
//   - languages: Docs.Languages, falling back to top-level Languages
//   - folderID: Docs.FolderID, falling back to DriveFolderID
//
// Once all callers migrate to the Docs struct, this helper can be
// removed and callers can read req.Docs directly.
func (req GenerateRequest) ResolveDocsConfig() (enabled bool, languages []Language, folderID string) {
	enabled = req.Docs.Enabled || req.DocsEnabled
	languages = req.Docs.Languages
	if len(languages) == 0 && enabled {
		languages = req.Languages
	}
	folderID = req.Docs.FolderID
	if folderID == "" {
		folderID = req.DriveFolderID
	}
	return
}

// IsTerminal returns true when the stage signals a terminal state.
func (s Stage) IsTerminal() bool {
	return s == StageCompleted || s == StageFailed
}

// ── Completion contract ─────────────────────────────────────────────

// IsRunCompletable checks whether a run has satisfied all preconditions
// for transitioning to COMPLETED. Verdetto contract:
//
//	scene normalizzate > 0
//	ogni scena ha testo EN
//	ogni scena ha testo ES
//	ogni voiceover richiesto è READY
//	documento EN ha id e link
//	documento ES ha id e link
//	nessuna fase richiesta è PENDING
func IsRunCompletable(result *GenerateResult, wantedLanguages []Language) bool {
	if result == nil {
		return false
	}
	if len(result.Scenes) == 0 {
		return false
	}

	// Every scene must have text for every wanted language.
	for _, scene := range result.Scenes {
		for _, lang := range wantedLanguages {
			if scene.Text[lang] == "" {
				return false
			}
		}
	}

	// Every wanted language must have a published document with ID and link.
	for _, lang := range wantedLanguages {
		doc, ok := result.Documents[lang]
		if !ok || doc.ID == "" || doc.Link == "" {
			return false
		}
	}

	return true
}

// ── Retry helpers ───────────────────────────────────────────────────

// MaxRetries is the maximum number of retry attempts per run.
const MaxRetries = 3

// RetryDelay computes the exponential backoff delay for the given
// attempt number. Base delay is 5 seconds, doubling each retry
// up to a max of 120 seconds.
func RetryDelay(attempt int) time.Duration {
	d := time.Duration(5<<uint(attempt)) * time.Second
	if d > 120*time.Second {
		d = 120 * time.Second
	}
	return d
}

// ShouldRetry checks whether a failed run should be retried based on
// the attempt count and current retry schedule.
func ShouldRetry(run *GenerationRun) bool {
	if run == nil {
		return false
	}
	if run.Status != RunStatusFailed && run.Status != RunStatusRunning {
		return false
	}
	if run.AttemptCount >= MaxRetries {
		return false
	}
	if run.NextRetryAt != nil && run.NextRetryAt.After(time.Now()) {
		return false
	}
	return true
}

// stageOrder defines the canonical stage execution order for
// resume-from-checkpoint logic. Each stage maps to its index
// in the ordered list.
var stageOrder = []Stage{
	StageNormalizing,
	StageGeneratingSceneText,
	StageTranslatingScenes,
	StageGeneratingVoiceovers,
	StageCompilingAudio,
	StagePublishingDocuments,
}

// StageIndex returns the zero-based index of a stage in the
// canonical execution order, or -1 if the stage is not in the
// order (e.g. terminal stages like COMPLETED/FAILED).
func StageIndex(s Stage) int {
	for i, st := range stageOrder {
		if st == s {
			return i
		}
	}
	return -1
}

// ResumeFrom returns the first non-terminal stage that should be
// executed. If the run is COMPLETED, returns StageCompleted.
// If the run failed at a specific stage, returns that stage.
// Otherwise returns StageNormalizing.
func ResumeFrom(run *GenerationRun) Stage {
	if run == nil {
		return StageNormalizing
	}
	switch run.Status {
	case RunStatusCompleted:
		return StageCompleted
	case RunStatusFailed:
		// Resume from the failed stage (will retry).
		if run.FailedStage != "" && StageIndex(run.FailedStage) >= 0 {
			return run.FailedStage
		}
		return StageNormalizing
	case RunStatusRunning:
		// Already running — resume from current stage.
		if run.CurrentStage != "" && StageIndex(run.CurrentStage) >= 0 {
			return run.CurrentStage
		}
		return StageNormalizing
	default:
		return StageNormalizing
	}
}
