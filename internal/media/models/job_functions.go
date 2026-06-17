package models

import "fmt"

// IsTerminal restituisce true se lo stato è terminale.
func (s JobStatus) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// IsValid returns true if the status is one of the 5 canonical values.
func (s JobStatus) IsValid() bool {
	switch s {
	case StatusQueued, StatusRunning, StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// TransitionJob is the single-point state machine for job lifecycle transitions.
// All status updates MUST flow through this function. Direct string updates in
// SQL are banned — this ensures the 5-state CHECK constraint stays consistent.
//
// Allowed transitions:
//
//	queued    → running, completed, failed, cancelled
//	running   → completed, failed, cancelled, queued (lease expiry)
//	completed → (terminal)
//	failed    → queued (retry, when retry_count < max_retries)
//	cancelled → (terminal)
func TransitionJob(current, next JobStatus) error {
	switch current {
	case StatusQueued:
		switch next {
		case StatusRunning, StatusCompleted, StatusFailed, StatusCancelled:
			return nil
		}
	case StatusRunning:
		switch next {
		case StatusCompleted, StatusFailed, StatusCancelled, StatusQueued:
			return nil
		}
	case StatusFailed:
		switch next {
		case StatusQueued:
			return nil
		}
	case StatusCompleted, StatusCancelled:
		return fmt.Errorf("cannot transition from terminal status %q to %q", current, next)
	}
	return fmt.Errorf("invalid transition: %q → %q", current, next)
}

// CanRetry restituisce true se il job può essere riprovato.
// Cancelled è terminale — TransitionJob rifiuta cancelled→queued.
func (j *Job) CanRetry() bool {
	if j == nil {
		return false
	}
	return j.RetryCount < j.MaxRetries && j.Status == StatusFailed
}
