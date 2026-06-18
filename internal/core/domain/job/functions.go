package job

// IsTerminal returns true if the status is one of the three terminal states.
func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// IsActive returns true if a worker currently owns this job.
func (s Status) IsActive() bool {
	return s == StatusLeased || s == StatusRunning
}

// IsTerminal returns true if the job has reached a terminal state.
func (j *Job) IsTerminal() bool {
	if j == nil {
		return false
	}
	return j.Status.IsTerminal()
}

// CanRetry checks if the job can be retried.
func (j *Job) CanRetry() bool {
	if j == nil {
		return false
	}
	return j.RetryCount < j.MaxRetries && (j.Status == StatusFailed || j.Status == StatusRetryWait)
}
