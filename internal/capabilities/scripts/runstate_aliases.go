// runstate_aliases.go — facade for the durable run aggregate.
//
// GenerationRun, the Stage/RunStatus vocabulary and the resume/retry
// helpers are the state model of script generation: they depend only on
// the generate contract, so they live in the leaf scripts/runstate and
// the root re-exports them here. Consumers keep naming scriptgen.Stage,
// and the final-job stage can use the stage vocabulary without importing
// the orchestrator.
package scriptgeneration

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runstate"

type (
	GenerationRun = runstate.GenerationRun
	RunStatus     = runstate.RunStatus
	Stage         = runstate.Stage
)

const (
	RunStatusPending   = runstate.RunStatusPending
	RunStatusRunning   = runstate.RunStatusRunning
	RunStatusCompleted = runstate.RunStatusCompleted
	RunStatusFailed    = runstate.RunStatusFailed

	StageNormalizing          = runstate.StageNormalizing
	StagePreflight            = runstate.StagePreflight
	StageGeneratingSceneText  = runstate.StageGeneratingSceneText
	StageTranslatingScenes    = runstate.StageTranslatingScenes
	StageGeneratingVoiceovers = runstate.StageGeneratingVoiceovers
	StageCompilingAudio       = runstate.StageCompilingAudio
	StagePublishingDocuments  = runstate.StagePublishingDocuments
	StageCompleted            = runstate.StageCompleted
	StageFailed               = runstate.StageFailed
	StageCoreReady            = runstate.StageCoreReady

	MaxRetries = runstate.MaxRetries
)

var (
	ErrProjectRequired    = runstate.ErrProjectRequired
	ErrEmptyGeneratedText = runstate.ErrEmptyGeneratedText
	ErrGoogleDocsRetired  = runstate.ErrGoogleDocsRetired

	stageOrder = runstate.StageOrder

	RetryDelay  = runstate.RetryDelay
	ShouldRetry = runstate.ShouldRetry
	StageIndex  = runstate.StageIndex
	ResumeFrom  = runstate.ResumeFrom
)
