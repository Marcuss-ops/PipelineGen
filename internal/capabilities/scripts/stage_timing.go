// Package scriptgeneration — stage_timing.go owns the canonical observability
// STAGE names for the single-item script-generation pipeline.
//
// These names are the STAGE dimension (business phase boundaries). The
// external technical calls (qdrant.search, sqlite.hydrate, ollama.generate,
// google_docs.publish, ...) are recorded separately as OperationReport
// observations so a phase wall time is never confused with an accumulated
// dependency time. Every stage below is measured with MeasureStageReport —
// the single canonical clock owned by internal/kernel/observability — and
// must never be re-measured with an ad-hoc time.Now().
package scriptgeneration

import kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"

const (
	StageScriptPrepare     kernobs.StageName = "script.prepare"
	StageScriptNormalize   kernobs.StageName = "script.normalize"
	StageScriptValidate    kernobs.StageName = "script.validate"
	StageSourceResolve     kernobs.StageName = "source.resolve"
	StageScriptPlan        kernobs.StageName = "script.plan"
	StageScriptEngine      kernobs.StageName = "script.engine"
	StageScriptPostprocess kernobs.StageName = "script.postprocess"
	StageAudioPipeline     kernobs.StageName = "audio.pipeline"
	// StageSceneAnalysis is the business stage under which per-scene
	// entity/phrase/word extraction is recorded as nlp.extract operations.
	StageSceneAnalysis kernobs.StageName = "scene_analysis"
	// StageOverlayPrepare is the stage under which the overlay.prepare job
	// enqueue (submitted before TTS) is recorded.
	StageOverlayPrepare kernobs.StageName = "overlay.prepare"
	// StageDocumentPrepare is the stage under which the document HTML
	// render (the prepare half of docs) is recorded, distinct from the
	// document.publish google_docs.publish boundary.
	StageDocumentPrepare kernobs.StageName = "document.prepare"
	// StageDocumentPublish is the stage under which the rendered documents
	// are uploaded to Google Docs (the publish half of docs).
	StageDocumentPublish kernobs.StageName = "document.publish"
)
