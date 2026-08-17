// Package usecase — stage_timing.go owns the canonical observability STAGE
// names for the single-item script-generation pipeline.
//
// These names are the STAGE dimension (business phase boundaries). The
// external technical calls (qdrant.search, sqlite.hydrate, ollama.generate,
// google_docs.publish, ...) are recorded separately as OperationReport
// observations so a phase wall time is never confused with an accumulated
// dependency time. Every stage below is measured with MeasureStageReport —
// the single canonical clock owned by internal/kernel/observability — and
// must never be re-measured with an ad-hoc time.Now().
package usecase

import kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"

const (
	stageScriptPrepare     kernobs.StageName = "script.prepare"
	stageScriptNormalize   kernobs.StageName = "script.normalize"
	stageScriptValidate    kernobs.StageName = "script.validate"
	stageSourceResolve     kernobs.StageName = "source.resolve"
	stageScriptPlan        kernobs.StageName = "script.plan"
	stageScriptEngine      kernobs.StageName = "script.engine"
	stageScriptPostprocess kernobs.StageName = "script.postprocess"
	stageAudioPipeline     kernobs.StageName = "audio.pipeline"
)
