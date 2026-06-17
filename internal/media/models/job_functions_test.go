package models

import "testing"

func TestJobCanRetryNilSafe(t *testing.T) {
	var job *Job
	if job.CanRetry() {
		t.Fatal("expected nil job to not be retryable")
	}
}

func TestJobCanRetry(t *testing.T) {
	job := &Job{RetryCount: 1, MaxRetries: 3, Status: StatusFailed}
	if !job.CanRetry() {
		t.Fatal("expected failed job within retry limit to be retryable")
	}

	job.Status = StatusCompleted
	if job.CanRetry() {
		t.Fatal("expected completed job to not be retryable")
	}
}
