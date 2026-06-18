package job

// IsTerminal returns true if the status is one of the terminal states.
func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// CanRetry checks if the job can be retried.
func (j *Job) CanRetry() bool {
	if j == nil {
		return false
	}
	return j.RetryCount < j.MaxRetries && j.Status == StatusFailed
}
