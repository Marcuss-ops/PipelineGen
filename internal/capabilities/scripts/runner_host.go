package scriptgeneration

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptrunner "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runner"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// Host implementation: the root owns the phase ports and their implementations;
// scripts/runner owns the sequence, run-scoped state and failure semantics.

func (r *Runner) Logger() *zap.Logger {
	if r == nil || r.log == nil {
		return zap.NewNop()
	}
	return r.log
}

func (r *Runner) MeasurePhase(ctx context.Context, stage kernobs.StageName, fn func(context.Context) bool) bool {
	return r.measurePhase(ctx, stage, fn)
}

func (r *Runner) Checkpoint(ctx context.Context, runID string, result *contract.GenerateResult) error {
	return r.checkpoint(ctx, runID, result)
}

func (r *Runner) FailRunWithRetry(ctx context.Context, runID string, stage Stage, err error) {
	r.failRunWithRetry(ctx, runID, stage, err)
}

func (r *Runner) GetRun(ctx context.Context, runID string) (*GenerationRun, error) {
	return r.repo.Get(ctx, runID)
}

func (r *Runner) RepairOverlayCheckpointOnResume(ctx context.Context, runID string, resumeStage Stage, result *GenerateResult) (Stage, bool) {
	return r.repairOverlayCheckpointOnResume(ctx, runID, resumeStage, result)
}

func (r *Runner) UpdateStage(ctx context.Context, runID string, status RunStatus, stage Stage) error {
	return r.updateStage(ctx, runID, status, stage)
}

func (r *Runner) MediaPreflight() scriptrunner.MediaPreflight {
	return r.mediaPreflight
}

func (r *Runner) ValidateEnabledVidRushProviders(ctx context.Context, req contract.GenerateRequest) error {
	if availability, ok := r.mediaPreflight.(scriptrunner.VidRushProviderAvailabilityPreflight); ok {
		return availability.RunVidRushProviderAvailability(ctx, req).AsError()
	}
	return RunVidRushProviderAvailabilityPreflight(ctx, req.MediaPlan, nil).AsError()
}

func (r *Runner) BeginVidRush(ctx context.Context, runID string, req contract.GenerateRequest) (any, error) {
	coordinator, err := r.beginVidRush(ctx, runID, req)
	if coordinator == nil {
		return nil, err
	}
	return coordinator, err
}

func (r *Runner) EndVidRush(runID string) { r.endVidRush(runID) }

func (r *Runner) RunNormalizePhase(ctx context.Context, runID string, exec ExecutionContext, resumeIdx int) bool {
	return r.runNormalizePhase(ctx, runID, exec, resumeIdx)
}

func (r *Runner) RunSceneTextPhase(ctx context.Context, runID string, req contract.GenerateRequest, routing scriptpkg.ArtifactRoutingContext, exec ExecutionContext, run *GenerationRun, resumeIdx int) (*contract.GenerateResult, bool) {
	return r.runSceneTextPhase(ctx, runID, req, routing, exec, run, resumeIdx)
}

func (r *Runner) RunTranslationPhase(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, resumeIdx int, result *contract.GenerateResult) bool {
	return r.runTranslationPhase(ctx, runID, req, exec, resumeIdx, result)
}

func (r *Runner) RunVoiceoverPhase(ctx context.Context, runID string, req contract.GenerateRequest, routing scriptpkg.ArtifactRoutingContext, exec ExecutionContext, resumeIdx int, result *contract.GenerateResult) bool {
	return r.runVoiceoverPhase(ctx, runID, req, routing, exec, resumeIdx, result)
}

func (r *Runner) AudioCompilePhase(ctx context.Context, runID string, req contract.GenerateRequest, routing scriptpkg.ArtifactRoutingContext, exec ExecutionContext, resumeIdx int, result *contract.GenerateResult) bool {
	return r.audioCompilePhase(ctx, runID, req, routing, exec, resumeIdx, result)
}

func (r *Runner) SubmitFinalJob(ctx context.Context, runID string, req contract.GenerateRequest, result *contract.GenerateResult) bool {
	return r.submitFinalJob(ctx, runID, req, result)
}

func (r *Runner) PersistScript(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, resumeIdx int, result *contract.GenerateResult) bool {
	return r.persistScript(ctx, runID, req, exec, resumeIdx, result)
}

func (r *Runner) CompleteRun(ctx context.Context, runID string, result *contract.GenerateResult) error {
	return r.completeRun(ctx, runID, result)
}

func (r *Runner) AudioAssetSource() contract.AudioAssetSource { return r.audioAssetSource }

func (r *Runner) SoundEffectPool(ctx context.Context, runID string) []string {
	return r.soundEffectPool(ctx, runID)
}

func (r *Runner) SnapshotSceneText(scenes []contract.Scene, language contract.Language, req contract.GenerateRequest) []scriptrunner.SceneTextSnapshot {
	return snapshotSceneText(scenes, language, req)
}

func (r *Runner) LocalizedNLPSnapshot(result *contract.GenerateResult) *contract.GenerateResult {
	return localizedNLPSnapshot(result)
}

func (r *Runner) SourceTraceFromResult(result *contract.GenerateResult) scriptpkg.SourceTrace {
	return sourceTraceFromResult(result)
}

func (r *Runner) RunVidRushJoinAndPrepare(ctx context.Context, runID string, req contract.GenerateRequest, snapshot []scriptrunner.SceneTextSnapshot) (scriptrunner.PrepareResult, error) {
	prepared, err := r.runVidRushJoinAndPrepare(ctx, runID, req, snapshot)
	if err != nil {
		return scriptrunner.PrepareResult{}, err
	}
	return scriptrunner.PrepareResult{
		Segments:       prepared.segments,
		Annotations:    prepared.annotations,
		DisabledScenes: prepared.disabledScenes,
		Intents:        prepared.intents,
	}, nil
}

func (r *Runner) ComputeLocalizedAnnotations(ctx context.Context, req contract.GenerateRequest, result *contract.GenerateResult, sourceAnnotations map[int]*scriptpkg.SceneAnnotations) (map[int]map[contract.Language]*scriptpkg.SceneAnnotations, error) {
	return r.computeLocalizedAnnotations(ctx, req, result, sourceAnnotations)
}

func (r *Runner) ApplyVidRushPrepareProjections(result *contract.GenerateResult, prepared scriptrunner.PrepareResult) {
	applyVidRushPrepareProjections(result, vidRushPrepareResult{
		segments:       prepared.Segments,
		annotations:    prepared.Annotations,
		disabledScenes: prepared.DisabledScenes,
		intents:        prepared.Intents,
	})
}

func (r *Runner) ApplyLocalizedAnnotations(result *contract.GenerateResult, localized map[int]map[contract.Language]*scriptpkg.SceneAnnotations) {
	applyLocalizedAnnotations(result, localized)
}

func (r *Runner) VoiceoverPublishDrainer() interface{ Wait() } { return r.voiceoverPublishDrainer }
