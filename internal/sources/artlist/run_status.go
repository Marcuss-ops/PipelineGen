package artlist

// RunStatus represents the status of an Artlist run
type RunStatus string

const (
	RunQueued          RunStatus = "queued"
	RunRunning         RunStatus = "running"
	RunCompleted       RunStatus = "completed"
	RunCompletedDryRun RunStatus = "completed_dry_run"
	RunFailed          RunStatus = "failed"
	RunCancelled       RunStatus = "cancelled"
	RunZombie          RunStatus = "zombie"
)

// IsTerminalRunStatus returns true if the status is a terminal state
func IsTerminalRunStatus(status RunStatus) bool {
	switch status {
	case RunCompleted, RunCompletedDryRun, RunFailed, RunCancelled, RunZombie:
		return true
	default:
		return false
	}
}
