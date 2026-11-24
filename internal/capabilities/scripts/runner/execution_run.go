package runner

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runstate"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// NewExecution binds one run's immutable inputs to the host that implements
// the phase ports. The orchestrator owns the mutable run-scoped state.
func NewExecution(host Host, ctx context.Context, runID string, req contract.GenerateRequest, exec scriptpkg.ExecutionContext) *Execution {
	return &Execution{host: host, ctx: ctx, runID: runID, req: req, exec: exec}
}

// Run executes the ordered durable workflow. Every phase is an explicit named
// step; a phase returning false stops the run (the terminal error was already
// classified and persisted by the host).
func (e *Execution) Run() {
	if !e.start() {
		return
	}
	if !e.normalize() {
		return
	}
	if !e.mediaPreflightPhase() {
		return
	}
	if !e.beginVidRushPhase() {
		return
	}
	// The VidRush coordinator wiring lives for the whole run: release it only
	// after every phase that consumes the fan-out completes (or fails).
	if e.coordinator != nil {
		defer e.host.EndVidRush(e.runID)
	}
	if !e.generate() {
		return
	}
	e.ensureResult()
	if !e.translate() {
		return
	}
	if !e.sceneTextReady() {
		return
	}
	if !e.host.AudioCompilePhase(e.ctx, e.runID, e.req, e.routing, e.exec, e.resumeIdx, e.result) {
		return
	}
	if !e.persist() {
		return
	}
	if !e.finalJob() {
		return
	}
	// Google Docs publication was removed completely: there is no post-core
	// document leg to defer or run. The run completes after the non-document
	// phases and the async voiceover pool is joined inside complete().
	e.complete()
}

// RunnerPhaseSequence returns the canonical ordered phase sequence executed by
// the durable Runner. The phase literals are owned by the kernel observability
// registry; this function owns the ORDER.
func RunnerPhaseSequence() []kernobs.StageName {
	return []kernobs.StageName{
		kernobs.StageRunNormalize,
		kernobs.StageRunMediaPreflight,
		kernobs.StageBeginVidRush,
		kernobs.StageGenerate,
		kernobs.StageRunTranslation,
		kernobs.StageRunVoiceover,
		kernobs.StageRunAudioCompile,
		kernobs.StageRunPersistence,
		kernobs.StageRunFinalJob,
	}
}

// RunStagePhase maps a durable-run stage to the observability phase the run
// reports while it is in that stage. It is the bridge between the durable
// caller-visible stage and the wall-time measurement phase.
func RunStagePhase(stage runstate.Stage) (kernobs.StageName, bool) {
	switch stage {
	case runstate.StageNormalizing:
		return kernobs.StageRunNormalize, true
	case runstate.StagePreflight:
		return kernobs.StageRunMediaPreflight, true
	case runstate.StageGeneratingSceneText:
		return kernobs.StageGenerate, true
	case runstate.StageTranslatingScenes:
		return kernobs.StageRunTranslation, true
	case runstate.StageGeneratingVoiceovers:
		return kernobs.StageRunVoiceover, true
	case runstate.StageCompilingAudio:
		return kernobs.StageRunAudioCompile, true
	case runstate.StagePublishingDocuments:
		// Legacy resume marker for the post-core publication leg. Its work
		// is the remote final job, so it reports under that phase.
		return kernobs.StageRunFinalJob, true
	default:
		return "", false
	}
}

// stageSkipped reports whether a stage is already behind the resume point.
func stageSkipped(resumeIdx int, stage runstate.Stage) bool {
	return resumeIdx >= 0 && runstate.StageIndex(stage) < resumeIdx
}
