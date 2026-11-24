package script

// RunStatus is the high-level lifecycle status of a durable generation run.
type RunStatus string

const (
	RunStatusPending   RunStatus = "PENDING"
	RunStatusRunning   RunStatus = "RUNNING"
	RunStatusCompleted RunStatus = "COMPLETED"
	RunStatusFailed    RunStatus = "FAILED"
)

// Stage identifies the exact phase within the script-generation workflow.
type Stage string

const (
	StageNormalizing          Stage = "NORMALIZING"
	StagePreflight            Stage = "MEDIA_PREFLIGHT" // P0.5: fail-fast asset verification before GENERATING_SCENE_TEXT
	StageGeneratingSceneText  Stage = "GENERATING_SCENE_TEXT"
	StageTranslatingScenes    Stage = "TRANSLATING_SCENES"
	StageGeneratingVoiceovers Stage = "GENERATING_VOICEOVERS"
	StageCompilingAudio       Stage = "COMPILING_AUDIO"
	// StagePublishingDocuments is the LEGACY publication stage. Document
	// publication was removed with Google Docs; the stage value survives for
	// persisted-run resume compatibility and as the failure stage the remote
	// final-job/artifact publication path reports.
	StagePublishingDocuments Stage = "PUBLISHING_DOCUMENTS"
	StageCompleted           Stage = "COMPLETED"
	StageFailed              Stage = "FAILED"
	// StageCoreReady is the LEGACY CORE_READY milestone, kept only so a run
	// persisted before the Google Docs demolition still resumes at the
	// post-core publication stage instead of replaying from NORMALIZING.
	StageCoreReady Stage = "CORE_READY"
)

// IsTerminal returns true when the stage signals a terminal state.
func (s Stage) IsTerminal() bool {
	return s == StageCompleted || s == StageFailed
}
