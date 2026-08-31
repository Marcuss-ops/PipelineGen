package observability

import "time"

// StageName identifies one canonical pipeline stage in the global
// observability taxonomy.
//
// NOTE: this is the OBSERVABILITY stage taxonomy (validate/acquire/
// process/...). It is intentionally distinct from
// internal/kernel/job.StageName (script/translation/voiceover/...),
// which identifies the workflow-stage dimension of job progress. The
// two vocabularies are kept separate so a job can report both its
// workflow progress and its execution timing without merging the two
// concepts.
type StageName string

const (
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
	// Clip timeline stages. These remain namespaced to avoid collisions with
	// generic pipeline stages while preserving the canonical RunReport model.
	StageClipSubmitted  StageName = "clip.submitted"
	StageClipClaimed    StageName = "clip.claimed"
	StageClipPrepare    StageName = "clip.prepare"
	StageClipRenderSlot StageName = "clip.render_slot"
	StageClipFFmpeg     StageName = "clip.ffmpeg"
	StageClipHashProbe  StageName = "clip.hash_probe"
	StageClipUploadSlot StageName = "clip.upload_slot"
	StageClipDrive      StageName = "clip.drive"
	StageClipFinalize   StageName = "clip.finalize"
)

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
	// ComponentCUDA is the PATH B CUDA hybrid backend (NVDEC/NVENC chain),
	// which measures the same canonical phases when it owns the render.
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
	OperationSearch      OperationName = "search"
	OperationUpsert      OperationName = "upsert"
	OperationUpload      OperationName = "upload"
	OperationDownload    OperationName = "download"
	OperationTranscode   OperationName = "transcode"
	OperationCut         OperationName = "cut"
	OperationNormalize   OperationName = "normalize"
	OperationMerge       OperationName = "merge"
	OperationGenerate    OperationName = "generate"
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

// AllStages returns the canonical stage names in registry order. It is
// useful to gate tooling (reporting, dashboards) that must not invent
// stage names outside the registry.
func AllStages() []StageName {
	return []StageName{
		StageValidate, StageResolve, StageGenerate, StageDiscover,
		StageAcquire, StageProcess, StageEnrich, StagePersist,
		StageIndex, StagePublish, StageVerify, StageCleanup,
		StageClipSubmitted, StageClipClaimed, StageClipPrepare,
		StageClipRenderSlot, StageClipFFmpeg, StageClipHashProbe,
		StageClipUploadSlot, StageClipDrive, StageClipFinalize,
	}
}

// AllOperations returns the canonical operation names in registry order.
func AllOperations() []OperationName {
	return []OperationName{
		OperationSearch, OperationUpsert, OperationUpload, OperationDownload,
		OperationTranscode, OperationCut, OperationNormalize, OperationMerge,
		OperationGenerate, OperationSynthesize, OperationTranscribe,
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
