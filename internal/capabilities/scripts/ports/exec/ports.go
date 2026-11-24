// Package exec owns the execution-phase ports (render/overlay enqueue,
// localized render, audio publication and execution evidence). It sits below
// scripts/ports so platform adapters can import the thin port surface without
// importing the scripts business package.
package exec

import (
	"context"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	audiocontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/audio"
	scriptrunner "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runner"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type OverlayPrepareEnqueuer interface {
	EnqueuePrepare(context.Context, capabilityoverlay.PrepareRequest) error
}

type OverlayRenderEnqueuer interface {
	EnqueueChrononPlanItems(context.Context, capmodel.OverlayPlan) (contract.RenderReference, error)
}

type LocalizedRenderInput struct {
	RunID       string
	ParentJobID string `json:"parent_job_id,omitempty"`
	JobID       string `json:"job_id,omitempty"`

	SceneID    string
	SceneIndex int
	Language   contract.Language
	Text       string
	Voiceover  contract.AudioReference

	SourceLanguage contract.Language `json:"source_language,omitempty"`
	SourceText     string            `json:"source_text,omitempty"`

	ClipID         string `json:"clip_id,omitempty"`
	ClipAssetID    string `json:"clip_asset_id,omitempty"`
	ClipSHA256     string `json:"clip_sha256,omitempty"`
	ClipDurationMS int64  `json:"clip_duration_ms,omitempty"`

	Render   scriptpkg.VideoRenderSpec `json:"render,omitempty"`
	Overlays []LocalizedOverlayLineage `json:"-"`

	OnRendered           func(contract.LocalizedRenderResult) error  `json:"-"`
	OnRenderReady        func(contract.LocalizedRenderResult) error  `json:"-"`
	FinalJob             bool                                        `json:"-"`
	SkipDrivePublication bool                                        `json:"-"`
	ResumeFrom           *contract.LocalizedRenderResult             `json:"-"`
	OnFailed             func(contract.LocalizedRenderFailure) error `json:"-"`
}

type LocalizedOverlayLineage struct {
	RenderJobID        string `json:"render_job_id"`
	PlanFingerprint    string `json:"plan_fingerprint"`
	RenderKey          string `json:"render_key"`
	SourceVideoAssetID string `json:"source_video_asset_id"`
	StartUS            int64  `json:"start_us"`
	EndUS              int64  `json:"end_us"`
}

type LocalizedRenderEnqueuer interface {
	EnqueueLocalizedRender(context.Context, LocalizedRenderInput) error
}

type LocalizedRenderRecoveryEnqueuer interface {
	UploadRendered(context.Context, LocalizedRenderInput, contract.LocalizedRenderResult) error
}

type CombinedAudioRenderer interface {
	Render(ctx context.Context, plan audio.CompiledAudioPlan, assets audio.ResolvedAudioAssets) (audiocontract.FinalAudioReference, contract.AudioPipelineMetrics, error)
}

type MediaPreflight = scriptrunner.MediaPreflight

type FinalAudioPublishResult struct {
	AssetID     string `json:"audio_asset_id"`
	DriveFileID string `json:"-"`
	DriveLink   string `json:"drive_link"`
}

type FinalAudioPublisher interface {
	PublishFinalAudio(context.Context, string, contract.Language, audiocontract.FinalAudioReference, string) (FinalAudioPublishResult, error)
}

type (
	ExecutionContext  = scriptpkg.ExecutionContext
	ExecutionStep     = scriptpkg.ExecutionStep
	ArtifactOperation = scriptpkg.ArtifactOperation
	ExecutionRecorder = scriptpkg.ExecutionRecorder
)

var NewExecutionContext = scriptpkg.NewExecutionContext

const (
	OperationTranslation = scriptpkg.OperationTranslation
	OperationTTS         = scriptpkg.OperationTTS
	OperationSubtitles   = scriptpkg.OperationSubtitles
	OperationRender      = scriptpkg.OperationRender
	OperationValidation  = scriptpkg.OperationValidation
	OperationDriveUpload = scriptpkg.OperationDriveUpload
)
