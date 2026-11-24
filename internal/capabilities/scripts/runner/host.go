package runner

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runstate"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// Host is implemented by the script-generation composition root. The
// orchestrator owns ordering, run-scoped state, checkpointing and failure
// classification; the host owns the phase implementations and their ports.
// The dependency direction is root -> runner, never runner -> root.
type Host interface {
	Logger() *zap.Logger
	MeasurePhase(ctx context.Context, stage kernobs.StageName, fn func(context.Context) bool) bool
	Checkpoint(ctx context.Context, runID string, result *contract.GenerateResult) error
	FailRunWithRetry(ctx context.Context, runID string, stage runstate.Stage, err error)
	GetRun(ctx context.Context, runID string) (*runstate.GenerationRun, error)
	RepairOverlayCheckpointOnResume(ctx context.Context, runID string, resumeStage runstate.Stage, result *contract.GenerateResult) (runstate.Stage, bool)
	UpdateStage(ctx context.Context, runID string, status runstate.RunStatus, stage runstate.Stage) error

	MediaPreflight() MediaPreflight
	ValidateEnabledVidRushProviders(ctx context.Context, req contract.GenerateRequest) error
	BeginVidRush(ctx context.Context, runID string, req contract.GenerateRequest) (any, error)
	EndVidRush(runID string)

	RunNormalizePhase(ctx context.Context, runID string, exec scriptpkg.ExecutionContext, resumeIdx int) bool
	RunSceneTextPhase(ctx context.Context, runID string, req contract.GenerateRequest, routing scriptpkg.ArtifactRoutingContext, exec scriptpkg.ExecutionContext, run *runstate.GenerationRun, resumeIdx int) (*contract.GenerateResult, bool)
	RunTranslationPhase(ctx context.Context, runID string, req contract.GenerateRequest, exec scriptpkg.ExecutionContext, resumeIdx int, result *contract.GenerateResult) bool
	RunVoiceoverPhase(ctx context.Context, runID string, req contract.GenerateRequest, routing scriptpkg.ArtifactRoutingContext, exec scriptpkg.ExecutionContext, resumeIdx int, result *contract.GenerateResult) bool
	AudioCompilePhase(ctx context.Context, runID string, req contract.GenerateRequest, routing scriptpkg.ArtifactRoutingContext, exec scriptpkg.ExecutionContext, resumeIdx int, result *contract.GenerateResult) bool
	SubmitFinalJob(ctx context.Context, runID string, req contract.GenerateRequest, result *contract.GenerateResult) bool
	PersistScript(ctx context.Context, runID string, req contract.GenerateRequest, exec scriptpkg.ExecutionContext, resumeIdx int, result *contract.GenerateResult) bool
	CompleteRun(ctx context.Context, runID string, result *contract.GenerateResult) error

	AudioAssetSource() contract.AudioAssetSource
	SoundEffectPool(ctx context.Context, runID string) []string
	SnapshotSceneText(scenes []contract.Scene, language contract.Language, req contract.GenerateRequest) []SceneTextSnapshot
	LocalizedNLPSnapshot(result *contract.GenerateResult) *contract.GenerateResult
	SourceTraceFromResult(result *contract.GenerateResult) scriptpkg.SourceTrace

	RunVidRushJoinAndPrepare(ctx context.Context, runID string, req contract.GenerateRequest, snapshot []SceneTextSnapshot) (PrepareResult, error)
	ComputeLocalizedAnnotations(ctx context.Context, req contract.GenerateRequest, result *contract.GenerateResult, sourceAnnotations map[int]*scriptpkg.SceneAnnotations) (map[int]map[contract.Language]*scriptpkg.SceneAnnotations, error)
	ApplyVidRushPrepareProjections(result *contract.GenerateResult, prepared PrepareResult)
	ApplyLocalizedAnnotations(result *contract.GenerateResult, localized map[int]map[contract.Language]*scriptpkg.SceneAnnotations)

	VoiceoverPublishDrainer() interface{ Wait() }
	OverlayPublicationDrainer() interface{ Wait(context.Context) error }
}
