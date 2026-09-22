package observability

import "time"

// StageName identifies one canonical pipeline phase in the global
// observability taxonomy.
//
// ── Owner of every phase name (SSOT) ────────────────────────────────
//
// THIS package is the single owner of every stage name recorded on a Run.
// A capability MUST alias the constant declared here instead of writing the
// literal a second time (`const stageX kernobs.StageName = kernobs.StageX`),
// for the same reason internal/kernel/job owns the job-type wire strings: a
// second declaration is a silent drift hazard, and it is invisible until a
// report joins two spellings of the same phase and finds nothing.
//
// Before this file carried the capability families, ~30 phase names were
// declared locally in the packages that emit them (script.prepare,
// overlay_render, clip.probe, stock.extract_clips, tts, ...) while AllStages()
// listed 21 — so the "gate tooling that must not invent stage names outside
// the registry" this comment used to promise was false.
//
// NOTE: this taxonomy is the EXECUTION/measurement dimension. It is
// intentionally distinct from internal/kernel/job.StageName
// (script/translation/voiceover/upload/persistence), which is the
// workflow-progress dimension reported to a parent job. The two vocabularies
// are kept separate so a job can report both its workflow progress and its
// execution timing without merging the two concepts.
type StageName string

const (
	// ── Generic execution phases ────────────────────────────────────
	StageValidate StageName = "validate"
	StageResolve  StageName = "resolve"
	StageGenerate StageName = "generate"
	StageDiscover StageName = "discover"
	StageAcquire  StageName = "acquire"
	StageProcess  StageName = "process"
	StageEnrich   StageName = "enrich"
	StagePersist  StageName = "persist"
	StageIndex    StageName = "index"
	StagePublish  StageName = "publish"
	StageVerify   StageName = "verify"
	StageCleanup  StageName = "cleanup"

	// ── Run-level phases of the script-generation pipeline ─────────
	//
	// These are the phases the durable Runner executes in order (see
	// capabilities/scripts/runner_execution_pipeline.go) and the phases a
	// GenerationRun reports as CurrentStage. The uppercase spellings are the
	// historical wire values of the run Stage enum and are preserved
	// verbatim: they are already persisted in pipeline_run rows and recorded
	// in RunReports, so they must not be "tidied" into lowercase.
	StageRunNormalize      StageName = "normalize"
	StageRunMediaPreflight StageName = "MEDIA_PREFLIGHT"
	StageBeginVidRush      StageName = "begin_vidrush"
	StageRunTranslation    StageName = "translation"
	StageRunVoiceover      StageName = "voiceover"
	StageRunAudioCompile   StageName = "audio_compile"
	StageRunPersistence    StageName = "persistence"
	StageRunDocument       StageName = "document"

	// StageRunCoreReady is the CORE_READY milestone: a milestone, NOT a work
	// phase — it is declared here so the name has one owner, and it is
	// deliberately excluded from allStageRegistry (see StageMilestones) so no
	// tooling can mistake it for a phase with work of its own.
	StageRunCoreReady StageName = "CORE_READY"

	// ── Script-generation sub-phases ────────────────────────────────
	StageScriptPrepare     StageName = "script.prepare"
	StageScriptNormalize   StageName = "script.normalize"
	StageScriptValidate    StageName = "script.validate"
	StageSourceResolve     StageName = "source.resolve"
	StageScriptPlan        StageName = "script.plan"
	StageScriptEngine      StageName = "script.engine"
	StageScriptPostprocess StageName = "script.postprocess"

	// ── Scene / overlay / audio / document phases ─────────────────────
	// StageSceneAnalysis hosts per-scene entity/phrase/word extraction
	// (nlp.extract operations).
	StageSceneAnalysis StageName = "scene_analysis"
	// StageOverlayPrepare is the overlay.prepare job enqueue (submitted
	// before TTS).
	StageOverlayPrepare StageName = "overlay.prepare"
	// StageOverlayRender is the BLOCKING overlay render boundary (submit +
	// wait + publish + RenderingGen phase projection). It is a sibling of the
	// audio stages, never nested inside them: sequenced inside the audio phase
	// it was charged to audio_compile and the render never reached the
	// critical path.
	StageOverlayRender   StageName = "overlay_render"
	StageAudioPipeline   StageName = "audio.pipeline"
	StageAudioFinalize   StageName = "audio_finalize"
	StageAudioPublish    StageName = "audio_publish"
	StageDocumentPrepare StageName = "document.prepare"
	StageDocumentPublish StageName = "document.publish"
	// StagePostWriterFinalize hosts the worker-side artifact/Drive
	// finalization that runs AFTER the run returns (worker_execution.go).
	StagePostWriterFinalize StageName = "post_writer_finalize"
	// StagePersistenceSQLite is the SQLite scripts-table write boundary owned
	// by the persistence processor; it nests under StageRunPersistence.
	StagePersistenceSQLite StageName = "persistence.sqlite"

	// ── Voiceover service sub-phases ───────────────────────────────
	StageTTS       StageName = "tts"
	StageAudioPost StageName = "audio_post"
	StageFinalize  StageName = "finalize"

	// ── Clip timeline stages ───────────────────────────────────────
	// Namespaced to avoid collisions with the generic phases while
	// preserving the canonical RunReport model.
	StageClipSubmitted  StageName = "clip.submitted"
	StageClipClaimed    StageName = "clip.claimed"
	StageClipPrepare    StageName = "clip.prepare"
	StageClipRenderSlot StageName = "clip.render_slot"
	StageClipFFmpeg     StageName = "clip.ffmpeg"
	StageClipHashProbe  StageName = "clip.hash_probe"
	StageClipUploadSlot StageName = "clip.upload_slot"
	StageClipDrive      StageName = "clip.drive"
	StageClipFinalize   StageName = "clip.finalize"

	// clip.render worker phases. There is no overlay phase here: a declared
	// overlay is resolved BEFORE the plan is sealed and composited inside the
	// render pass, so its cost belongs to the render stage.
	//
	// The render stage itself is NOT declared here on purpose: its literal IS
	// the clip.render JOB TYPE, owned by internal/kernel/job
	// (TypeClipRender) and enforced by percheck_identity_ssot. The capability
	// derives its stage from that owner constant, so this registry holds no
	// second spelling of it.
	StageClipDestinationResolve StageName = "clip.destination_resolve"
	StageClipSubtitles          StageName = "clip.subtitles"
	StageClipProbe              StageName = "clip.probe"
	StageClipPublish            StageName = "clip.publish"

	// ── Stock pipeline phases ──────────────────────────────────────
	// The stock.run step keys are the phases recorded by the stock
	// orchestrator (a step key IS the stage name it is measured under).
	StageStockPlan          StageName = "stock.plan"
	StageStockStageSources  StageName = "stock.stage_sources"
	StageStockExtractClips  StageName = "stock.extract_clips"
	StageStockComposeChunks StageName = "stock.compose_chunks"
	StageStockPublish       StageName = "stock.publish"
	StageStockFinalize      StageName = "stock.finalize"
	// Service-level stock phases recorded inside those steps.
	StageStockSearch          StageName = "stock.search"
	StageStockYouTubeDownload StageName = "stock.youtube_download"
	StageStockExtract         StageName = "stock.extract"
	StageStockCompose         StageName = "stock.compose"
	StageStockDurationProbe   StageName = "stock.duration_probe"
	StageStockDatabaseSave    StageName = "stock.database_save"
	StageStockIndex           StageName = "stock.index"
)

// allStageRegistry is the ONE ordered declaration of the work phases. Every
// constant above that is a work phase MUST appear here exactly once, in
// canonical execution order; AllStages() is derived from it so the ordered
// list can no longer drift from the declarations. Pinned by
// TestRegistry_StageOrderIsCanonical and TestRegistry_StageOrderHasNoDuplicates.
var allStageRegistry = []StageName{
	// generic
	StageValidate, StageResolve, StageGenerate, StageDiscover,
	StageAcquire, StageProcess, StageEnrich, StagePersist,
	StageIndex, StagePublish, StageVerify, StageCleanup,
	// run-level, in execution order
	StageRunNormalize, StageRunMediaPreflight, StageBeginVidRush,
	StageScriptPrepare,
	StageScriptNormalize, StageSourceResolve, StageScriptValidate, StageScriptPlan,
	StageScriptEngine, StageSceneAnalysis, StageScriptPostprocess,
	StageRunTranslation, StageOverlayPrepare, StageRunVoiceover,
	StageTTS, StageAudioPost, StageOverlayRender, StageRunAudioCompile,
	StageAudioPipeline, StageAudioFinalize, StageAudioPublish,
	StageRunPersistence, StagePersistenceSQLite, StageFinalize,
	StageDocumentPrepare, StageDocumentPublish, StageRunDocument,
	StagePostWriterFinalize,
	// clip timeline
	StageClipSubmitted, StageClipClaimed, StageClipPrepare,
	StageClipRenderSlot, StageClipFFmpeg, StageClipHashProbe,
	StageClipUploadSlot, StageClipDrive, StageClipFinalize,
	StageClipDestinationResolve, StageClipSubtitles,
	StageClipProbe, StageClipPublish,
	// stock pipeline, in execution order
	StageStockPlan, StageStockStageSources, StageStockSearch,
	StageStockYouTubeDownload, StageStockExtractClips, StageStockExtract,
	StageStockDurationProbe, StageStockComposeChunks, StageStockCompose,
	StageStockDatabaseSave, StageStockIndex, StageStockPublish,
	StageStockFinalize,
}

// StageMilestones returns the recorded stage names that are milestones, not
// work phases: they carry no work of their own and MUST NOT be treated as a
// phase boundary by resume/report tooling (CORE_READY is mapped explicitly
// onto the first phase that still has work). They are declared with the other
// stage constants so the name has exactly one owner.
func StageMilestones() []StageName {
	return []StageName{StageRunCoreReady}
}

// ComponentName identifies one external component (the adapter boundary).
type ComponentName string

const (
	ComponentOllama         ComponentName = "ollama"
	ComponentArtlist        ComponentName = "artlist"
	ComponentYouTube        ComponentName = "youtube"
	ComponentFFmpeg         ComponentName = "ffmpeg"
	ComponentDrive          ComponentName = "drive"
	ComponentSQLite         ComponentName = "sqlite"
	ComponentQdrant         ComponentName = "qdrant"
	ComponentNLP            ComponentName = "nlp"
	ComponentTTS            ComponentName = "tts"
	ComponentGoogleDocs     ComponentName = "google_docs"
	ComponentInternetImages ComponentName = "internet_images"
	ComponentRenderQueue    ComponentName = "render_queue"
	// ComponentRenderingGen is the RenderingGen render service (the
	// Chronon overlay pipeline) whose worker-reported phase timings
	// PipelineGen projects into canonical operations.
	ComponentRenderingGen ComponentName = "renderinggen"
	// ComponentChronon is the Chronon3d GPU render engine — the owner of
	// the fine-grained render phase timings (decode/composite/subtitle
	// raster/encode/...) that PipelineGen projects onto the run as a typed
	// projection of its canonical clip report.
	ComponentChronon ComponentName = "chronon"
	// ComponentCUDA is retained as a catalog identity for historical runs:
	// the PATH B CUDA hybrid backend was removed (GPU compositing belongs
	// exclusively to Chronon), so no live writer emits this component.
	ComponentCUDA ComponentName = "cuda"
)

// WaitKind identifies a typed interval during which the run could not make progress.
type WaitKind string

const (
	WaitSemaphore       WaitKind = "semaphore_wait"
	WaitRateLimit       WaitKind = "rate_limit_wait"
	WaitRetryBackoff    WaitKind = "retry_backoff"
	WaitChildDependency WaitKind = "child_dependency_wait"
	WaitResourceLock    WaitKind = "resource_lock"
	WaitCompletion      WaitKind = "completion_wait"
	WaitOutboxDelivery  WaitKind = "outbox_delivery_wait"
)

// WaitInfo describes one blocked interval. The interval timestamps are
// supplied by the owner of the wait; the kernel never guesses them.
type WaitInfo struct {
	Kind       WaitKind
	Component  ComponentName
	StartedAt  time.Time
	FinishedAt time.Time
}

// OperationName identifies one operation at an external boundary.
type OperationName string

const (
	OperationSearch    OperationName = "search"
	OperationUpsert    OperationName = "upsert"
	OperationUpload    OperationName = "upload"
	OperationDownload  OperationName = "download"
	OperationTranscode OperationName = "transcode"
	OperationCut       OperationName = "cut"
	OperationNormalize OperationName = "normalize"
	OperationMerge     OperationName = "merge"
	OperationGenerate  OperationName = "generate"
	// OperationWarm is the explicit model-residency warm-up probe. It is a
	// distinct operation rather than a second OperationGenerate so the model
	// load paid by the warm-up is visible as its own measured fact; folded
	// into generate it became invisible stage-wall time that hid a cold start
	// behind a warm-up which claimed to prevent one.
	OperationWarm        OperationName = "warm"
	OperationSynthesize  OperationName = "synthesize"
	OperationTranscribe  OperationName = "transcribe"
	OperationTransaction OperationName = "transaction"
	OperationIndex       OperationName = "index"
	OperationEmbed       OperationName = "embed"
	OperationPublish     OperationName = "publish"
	OperationProbe       OperationName = "probe"
	OperationFetch       OperationName = "fetch"
	OperationExtract     OperationName = "extract"
	OperationResolve     OperationName = "resolve"
	OperationEnrich      OperationName = "enrich"
	OperationVerify      OperationName = "verify"
	// RenderingGen phase operations. These are the render worker's OWN
	// phases (materialize/plan/render/hash/objectstore_upload/
	// drive_publish) mapped onto the canonical model when PipelineGen
	// orchestrates the work — never a parallel timing family.
	OperationMaterialize       OperationName = "materialize"
	OperationPlan              OperationName = "plan"
	OperationRender            OperationName = "render"
	OperationHash              OperationName = "hash"
	OperationObjectStoreUpload OperationName = "objectstore_upload"
	OperationDrivePublish      OperationName = "drive_publish"
	// Chronon render phase operations. These are the render engine's OWN
	// phases (startup/probe/decode/composite/subtitle_raster/watermark_
	// raster/frame_conversion/encode/audio_mux + the GPU byte counters),
	// reported in the canonical clip report and projected onto the run —
	// never a parallel timing family. gpu_copy / gpu_readback carry Bytes,
	// not a fake duration.
	OperationRendererStartup OperationName = "renderer_startup"
	OperationDecode          OperationName = "decode"
	OperationComposite       OperationName = "composite"
	OperationSubtitleRaster  OperationName = "subtitle_raster"
	OperationWatermarkRaster OperationName = "watermark_raster"
	OperationFrameConversion OperationName = "frame_conversion"
	OperationEncode          OperationName = "encode"
	OperationAudioMux        OperationName = "audio_mux"
	OperationGPUCopy         OperationName = "gpu_copy"
	OperationGPUUpload       OperationName = "gpu_upload"
	OperationGPUReadback     OperationName = "gpu_readback"
)

// AllStages returns every canonical work-phase name in canonical order. It
// is the gate for tooling (reporting, dashboards) that must not invent stage
// names outside the registry: a name absent here is not a phase. Derived from
// allStageRegistry, never re-listed by hand. Milestones are NOT included —
// see StageMilestones.
func AllStages() []StageName {
	out := make([]StageName, len(allStageRegistry))
	copy(out, allStageRegistry)
	return out
}

// AllOperations returns the canonical operation names in registry order.
func AllOperations() []OperationName {
	return []OperationName{
		OperationSearch, OperationUpsert, OperationUpload, OperationDownload,
		OperationTranscode, OperationCut, OperationNormalize, OperationMerge,
		OperationGenerate, OperationWarm, OperationSynthesize, OperationTranscribe,
		OperationTransaction, OperationIndex, OperationEmbed, OperationPublish,
		OperationProbe, OperationFetch, OperationExtract, OperationResolve,
		OperationEnrich, OperationVerify,
		OperationMaterialize, OperationPlan, OperationRender, OperationHash,
		OperationObjectStoreUpload, OperationDrivePublish,
		OperationRendererStartup, OperationDecode, OperationComposite,
		OperationSubtitleRaster, OperationWatermarkRaster,
		OperationFrameConversion, OperationEncode, OperationAudioMux,
		OperationGPUCopy, OperationGPUUpload, OperationGPUReadback,
	}
}

// AllComponents returns the canonical component names in registry order.
func AllComponents() []ComponentName {
	return []ComponentName{
		ComponentOllama, ComponentArtlist, ComponentYouTube,
		ComponentFFmpeg, ComponentDrive, ComponentSQLite,
		ComponentQdrant, ComponentNLP, ComponentTTS,
		ComponentGoogleDocs, ComponentInternetImages,
		ComponentRenderQueue, ComponentRenderingGen,
		ComponentChronon, ComponentCUDA,
	}
}
