package models

// IsTerminal returns true if the status is one of the three terminal states.
func (s JobStatus) IsTerminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled
}

// IsActive returns true if a worker currently owns this job.
func (s JobStatus) IsActive() bool {
	return s == StatusLeased || s == StatusRunning
}

// IsValid returns true if the status is one of the 7 canonical values.
func (s JobStatus) IsValid() bool {
	switch s {
	case StatusPending, StatusLeased, StatusRunning, StatusSucceeded,
		StatusRetryWait, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// CanRetry checks if the job can be retried.
func (j *Job) CanRetry() bool {
	if j == nil {
		return false
	}
	return j.RetryCount < j.MaxRetries && (j.Status == StatusFailed || j.Status == StatusRetryWait)
}
