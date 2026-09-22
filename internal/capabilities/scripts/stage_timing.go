// Package scriptgeneration — stage_timing.go owns the canonical observability
// STAGE names for the single-item script-generation pipeline.
//
// These names are the STAGE dimension (business phase boundaries) and are
// ALIASES of the canonical constants in internal/kernel/observability — the
// registry is the single owner of every phase literal (see registry.go). The
// external technical calls (qdrant.search, sqlite.hydrate, ollama.generate,
// google_docs.publish, ...) are recorded separately as OperationReport
// observations so a phase wall time is never confused with an accumulated
// dependency time. Every stage below is measured with MeasureStageReport —
// the single canonical clock owned by internal/kernel/observability — and
// must never be re-measured with an ad-hoc time.Now().
package scriptgeneration

import kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"

const (
	StageScriptPrepare     = kernobs.StageScriptPrepare
	StageScriptNormalize   = kernobs.StageScriptNormalize
	StageScriptValidate    = kernobs.StageScriptValidate
	StageSourceResolve     = kernobs.StageSourceResolve
	StageScriptPlan        = kernobs.StageScriptPlan
	StageScriptEngine      = kernobs.StageScriptEngine
	StageScriptPostprocess = kernobs.StageScriptPostprocess
	StageAudioPipeline     = kernobs.StageAudioPipeline
	// StageSceneAnalysis is the business stage under which per-scene
	// entity/phrase/word extraction is recorded as nlp.extract operations.
	StageSceneAnalysis = kernobs.StageSceneAnalysis
	// StageOverlayPrepare is the stage under which the overlay.prepare job
	// enqueue (submitted before TTS) is recorded.
	StageOverlayPrepare = kernobs.StageOverlayPrepare
	// StageOverlayRender is the stage under which the BLOCKING overlay render
	// (submit + wait for completion + publish + RenderingGen phase projection)
	// is recorded.
	//
	// It exists because that render was sequenced inside the audio phase and
	// therefore charged to `audio_compile`: the audio stage reported a wall
	// time dominated by a video render it does not own, the render never
	// appeared on the critical path, and the run's reported bottleneck was the
	// audio stage with a misleading dominant operation. The render is a
	// distinct business boundary, so it gets its own stage — sibling to the
	// audio stage, never nested inside it, or the breakdown would re-attribute
	// it to the enclosing stage.
	StageOverlayRender = kernobs.StageOverlayRender
	// StageAudioFinalize is the stage under which the canonical
	// EditingTimelineV1 projection and the AUDIO_COMPILE step completion are
	// recorded. It is separate from the compile stage because it runs AFTER the
	// overlay render (the timeline's overlay span carries the certified render
	// artifact), and because audio_compile must report only the work it owns.
	StageAudioFinalize = kernobs.StageAudioFinalize
	// StageAudioPublish is the stage under which the certified final-audio
	// artifact is uploaded to Drive. It is separated from the audio compile
	// stage for the same reason: publishing is IO against an external system,
	// not audio compilation, and merging the two made drive.upload the
	// reported dominant operation of the audio stage.
	StageAudioPublish = kernobs.StageAudioPublish
	// StageDocumentPrepare is the stage under which the document HTML
	// render (the prepare half of docs) is recorded, distinct from the
	// document.publish google_docs.publish boundary.
	StageDocumentPrepare = kernobs.StageDocumentPrepare
	// StageDocumentPublish is the stage under which the rendered documents
	// are uploaded to Google Docs (the publish half of docs).
	StageDocumentPublish = kernobs.StageDocumentPublish
)
