// Package scriptgeneration — runner.go implements the durable
// stage-based execution of the script generation workflow. Each
// stage is executed in order, with checkpoint updates after every
// successful stage. A retry resumes from the last failed stage.
//
// Verdetto contract:
//
//	ScriptGenerationRunner
//	  ├─ Normalize
//	  ├─ GenerateSceneText
//	  ├─ TranslateScenes
//	  ├─ GenerateVoiceovers
//	  ├─ BuildRenderPayload
//	  ├─ UpsertDocuments
//	  └─ EnqueueRender
//
// Phase implementations live in runner_phase_*.go; this file retains the
// public Runner contract and linear orchestration.
// Resume-from-checkpoint: on retry, Execute reads the run from
// the repo and skips stages that are already checkpointed.
package scriptgeneration

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

func sourceTraceFromResult(result *GenerateResult) scriptpkg.SourceTrace {
	trace := scriptpkg.SourceTrace{}
	if result == nil {
		return trace
	}
	seen := make(map[string]struct{})
	for _, scene := range result.Scenes {
		refs := scene.Clips
		if len(refs) == 0 && scene.Clip != nil {
			refs = []*ClipReference{scene.Clip}
		}
		for _, ref := range refs {
			if ref == nil || ref.ID == "" {
				continue
			}
			if _, ok := seen[ref.ID]; ok {
				continue
			}
			seen[ref.ID] = struct{}{}
			trace.AcceptedClipIDs = append(trace.AcceptedClipIDs, ref.ID)
		}
	}
	return trace
}

// Runner executes the durable script generation stages.
// Each stage is checkpointed so a retry resumes from the last
// failed stage.
type Runner struct {
	repo                  RunRepository
	textGen               TextGenerator
	translator            Translator
	voiceoverGen          VoiceoverGenerator
	docPublisher          DocumentPublisher
	documentRenderer      DocumentRenderer
	renderEnqueuer        RenderEnqueuer
	combinedAudioRenderer CombinedAudioRenderer
	finalAudioPublisher   FinalAudioPublisher
	recorder              ExecutionRecorder
	sceneCommitObserver   SceneCommitObserver
	vidRushBarrier        VidRushBarrier
	log                   *zap.Logger
}

// NewRunner constructs the Runner with all required ports.
func NewRunner(
	repo RunRepository,
	textGen TextGenerator,
	translator Translator,
	voiceoverGen VoiceoverGenerator,
	docPublisher DocumentPublisher,
	renderEnqueuer RenderEnqueuer,
	documentRenderers ...DocumentRenderer,
) *Runner {
	if repo == nil {
		panic("scriptgeneration: RunRepository is required for Runner")
	}
	var documentRenderer DocumentRenderer
	if len(documentRenderers) > 0 {
		documentRenderer = documentRenderers[0]
	}
	return &Runner{
		repo:         repo,
		textGen:      textGen,
		translator:   translator,
		voiceoverGen: voiceoverGen,
		docPublisher: docPublisher, documentRenderer: documentRenderer, renderEnqueuer: renderEnqueuer,
		recorder: noopExecutionRecorder{},
		log:      zap.NewNop(),
	}
}

// SetDocumentRenderer wires the canonical presentation adapter after runner
// construction. This keeps the capability testable without importing an HTML
// or Drive implementation into the capability package.
func (r *Runner) SetDocumentRenderer(renderer DocumentRenderer) {
	if r != nil {
		r.documentRenderer = renderer
	}
}

// SetLogger sets the runner's logger. Nil-safe (no-op on nil).
func (r *Runner) SetLogger(log *zap.Logger) {
	if log != nil {
		r.log = log
	}
}

func (r *Runner) SetCombinedAudioRenderer(renderer CombinedAudioRenderer) {
	r.combinedAudioRenderer = renderer
}

// SetFinalAudioPublisher wires the canonical delivery publisher used to make
// the certified full-audio Drive link available to the document phase.
func (r *Runner) SetFinalAudioPublisher(publisher FinalAudioPublisher) {
	if r != nil {
		r.finalAudioPublisher = publisher
	}
}

// SetExecutionRecorder injects the durable execution/lineage port. A nil
// recorder restores the safe no-op implementation used by unit runtimes.
func (r *Runner) SetExecutionRecorder(recorder ExecutionRecorder) {
	if r != nil {
		r.setRecorder(recorder)
	}
}

// SetSceneCommitObserver wires the observer notified of every SceneCommitted
// event. A nil observer is safe and disables emission; a non-nil observer is
// fail-closed (a commit error fails the scene-text stage).
func (r *Runner) SetSceneCommitObserver(observer SceneCommitObserver) {
	if r != nil {
		r.sceneCommitObserver = observer
	}
}

// SetVidRushBarrier wires the final barrier awaited after scene generation
// completes. A nil barrier is safe and skips the wait; a non-nil barrier is
// fail-closed (a barrier error fails the run) and blocks only for enrichments
// still running, never re-running the whole-document EntitiesProcessor.
func (r *Runner) SetVidRushBarrier(barrier VidRushBarrier) {
	if r != nil {
		r.vidRushBarrier = barrier
	}
}

// waitForVidRush awaits the final incremental-VidRush barrier when one is
// wired. A nil barrier is a safe no-op (batch workflows may enrich the whole
// document later); when present, a barrier error fails the run fail-closed.
func (r *Runner) waitForVidRush(ctx context.Context, runID string) error {
	if r.vidRushBarrier == nil {
		return nil
	}
	_, err := r.vidRushBarrier.WaitForVidRush(ctx, runID)
	return err
}

// Execute runs the complete generation workflow for the given run.
// It reads the run from the repository and resumes from the last
// checkpointed stage, skipping already-completed stages.
//
// Resume flow:
//  1. Read GenerationRun from repo (or use provided req for new runs)
//  2. Determine resume stage via ResumeFrom()
//  3. Skip stages before the resume stage
//  4. Execute from resume stage onward with checkpoint after each
//  5. On failure: persist error, return
//  6. On completion: persist final result, mark COMPLETED
//
// The handler must NOT wait for Execute to complete. This method
// is intended to be launched as a goroutine.
func (r *Runner) Execute(ctx context.Context, runID string, req GenerateRequest) {
	r.ExecuteWithContext(ctx, runID, req, NewExecutionContext(runID, req.IdempotencyKey))
}

// ExecuteWithContext is the worker-facing entry point that preserves the
// broker's root/parent/project/video correlation across every stage.
func (r *Runner) ExecuteWithContext(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext) {

	if exec.JobID == "" {
		exec.JobID = runID
	}
	if exec.RootJobID == "" {
		exec.RootJobID = exec.JobID
	}
	if exec.CorrelationID == "" {
		exec.CorrelationID = req.IdempotencyKey
	}
	if err := exec.Validate(); err != nil {
		if r.log != nil {
			r.log.Warn("scriptgeneration: invalid execution context", zap.Error(err))
		}
		r.failRunWithRetry(ctx, runID, StageNormalizing, err)
		return
	}
	r.log.Info("scriptgeneration: starting execution",
		zap.String("run_id", runID),
		zap.String("source_type", string(req.Source.Type)),
	)

	// Determine resume stage from existing run (if any).
	run, err := r.repo.Get(ctx, runID)
	resumeIdx := -1 // -1 means start from beginning
	if err == nil && run != nil {
		resumeStage := ResumeFrom(run)
		if resumeStage == StageCompleted {
			r.log.Info("run already completed", zap.String("run_id", runID))
			return
		}
		resumeIdx = StageIndex(resumeStage)
		r.log.Info("resuming from checkpoint",
			zap.String("run_id", runID),
			zap.String("resume_stage", string(resumeStage)),
			zap.Int("attempt", run.AttemptCount+1),
		)
	} else {
		// New run — set RUNNING.
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageNormalizing); err != nil {
			r.failRunWithRetry(ctx, runID, StageNormalizing, err)
			return
		}
	}
	if exec.Attempt <= 0 {
		exec.Attempt = 1
		if run != nil && run.AttemptCount > 0 {
			exec.Attempt = run.AttemptCount + 1
		}
	}

	if !r.runNormalizePhase(ctx, runID, exec, resumeIdx) {
		return
	}
	result, ok := r.runSceneTextPhase(ctx, runID, req, exec, run, resumeIdx)
	if !ok {
		return
	}
	if result == nil {
		result = &GenerateResult{Scenes: []Scene{}, Title: req.Title, OutputName: req.OutputName, VoiceoverGroup: req.ScriptParams.VoiceoverGroup}
	}
	// Final VidRush barrier: wait only for enrichments still running, never
	// re-running the whole-document EntitiesProcessor.
	if err := r.waitForVidRush(ctx, runID); err != nil {
		r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
		return
	}
	result.SourceTrace = sourceTraceFromResult(result)
	if !r.runTranslationPhase(ctx, runID, req, exec, resumeIdx, result) {
		return
	}
	if !r.runVoiceoverPhase(ctx, runID, req, exec, resumeIdx, result) {
		return
	}
	if !r.runRenderPayloadPhase(ctx, runID, req, exec, resumeIdx, result) {
		return
	}
	if !r.publishFinalAudio(ctx, runID, req, result) {
		return
	}
	// Render is enqueued (and, for the central queue, awaited) BEFORE the
	// document phase so the document can project the certified overlay
	// reference. When RenderVideo is false the enqueue phase is skipped.
	if !r.runEnqueuePhase(ctx, runID, req, exec, resumeIdx, result) {
		return
	}
	if !r.runDocumentPhase(ctx, runID, req, exec, resumeIdx, result) {
		return
	}
	r.completeRun(ctx, runID, result)
}
