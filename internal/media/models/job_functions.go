package models

// IsTerminal restituisce true se lo stato è terminale
func (s JobStatus) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// CanRetry restituisce true se il job può essere riprovato
func (j *Job) CanRetry() bool {
	return j.RetryCount < j.MaxRetries && (j.Status == StatusFailed || j.Status == StatusCancelled)
}
