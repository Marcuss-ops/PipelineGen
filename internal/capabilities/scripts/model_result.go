// model_result.go — rendered-output contract: GenerateResult,
// final references and pipeline metrics (split from model.go).
package scriptgeneration

import (
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"time"
)

type GenerateResult struct {
	// renderFirstStartedAt/renderLastFinishedAt are runtime-only aggregation
	// state. They are deliberately not serialized; RenderMetrics exposes the
	// derived wall/work values.
	renderFirstStartedAt time.Time
	renderLastFinishedAt time.Time
	// Output is the canonical plain-text result projection. It is derived once
	// from the ordered scenes and is never independently generated.
	Output GenerateOutput `json:"output"`
	// Render is the requested clip materialization contract. It is mirrored
	// into the canonical SpecScene response so selected clip bindings and the
	// render options travel together.
	Render scriptpkg.VideoRenderSpec `json:"render,omitempty"`
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

	// SemanticRenderBundle is the cross-stage audit contract assembled from
	// the same certified surfaces as OverlayPlan. It is a projection, never a
	// second extraction, resolver or timing source.
	SemanticRenderBundle *capabilityoverlay.SemanticRenderBundleV1 `json:"semantic_render_bundle,omitempty"`

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

	// WordCount is the generated BODY word count. It excludes DisplayText and
	// all text/content attached to protected fixed-media scenes; those remain
	// part of the timeline/specscene surface only.
	WordCount      int    `json:"word_count"`
	VoiceoverGroup string `json:"voiceover_group,omitempty"`

	AudioMode          capabilityaudio.AudioMode           `json:"audio_mode,omitempty"`
	AudioStrategy      capabilityaudio.AudioRenderStrategy `json:"audio_strategy,omitempty"`
	FinalAudio         *FinalAudioReference                `json:"final_audio,omitempty"`
	AudioMetrics       *AudioPipelineMetrics               `json:"audio_metrics,omitempty"`
	TranslationMetrics *TranslationPipelineMetrics         `json:"translation_metrics,omitempty"`

	// LocalizedRenders are the certified produced videos of the localized
	// render fan-out: one entry per successfully rendered + uploaded video
	// with its asset id, sha256, and Drive identity. Nil when the fan-out
	// produced no certified video.
	LocalizedRenders []LocalizedRenderResult `json:"localized_renders,omitempty"`
	// LocalizedRenderStaged contains locally certified RENDERED artifacts
	// whose Drive publication has not yet been confirmed. It is the durable
	// hand-off point used by resume: these entries must be uploaded without
	// invoking Chronon again.
	LocalizedRenderStaged   []LocalizedRenderResult  `json:"localized_render_staged,omitempty"`
	LocalizedRenderFailures []LocalizedRenderFailure `json:"localized_render_failures,omitempty"`
	RenderMetrics           *RenderMetrics           `json:"render_metrics,omitempty"`
	ExpectedRenderCount     int                      `json:"expected_render_count,omitempty"`

	// AudioPrefetch carries pre-resolved audio assets (BGM/SFX paths +
	// clip audio materialization) populated by the P1.1 prefetch goroutine
	// during TTS. Nil means prefetch was not needed or not wired.
	AudioPrefetch *AudioPrefetchResult `json:"-"`
}

type RenderMetrics struct {
	Expected   int   `json:"expected"`
	Successful int   `json:"successful"`
	Failed     int   `json:"failed"`
	WallMS     int64 `json:"wall_ms"`
	// WorkMS is the sum of child localized-render durations. It may exceed
	// WallMS when renders overlap and must not be used as wall-clock time.
	WorkMS        int64 `json:"work_ms,omitempty"`
	MaterializeMS int64 `json:"materialize_ms,omitempty"`
	RenderMS      int64 `json:"render_ms,omitempty"`
	UploadMS      int64 `json:"upload_ms,omitempty"`
	CPUFallbacks  int   `json:"cpu_fallbacks,omitempty"`
	GPUOOMs       int   `json:"gpu_ooms,omitempty"`
	Concurrency   int   `json:"concurrency,omitempty"`
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
