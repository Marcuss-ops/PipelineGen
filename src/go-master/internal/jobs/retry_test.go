package jobs

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

func TestRetryerRetryFailed(t *testing.T) {
	store, _, cleanup := setupTestStoreWithEvents(t)
	defer cleanup()

	retryer := NewRetryer(store, zap.NewNop(), DefaultRetryPolicy())

	job := &Job{
		ID:         "job-001",
		Type:       "test",
		Status:     JobStatusFailed,
		Attempts:  1,
		MaxAttempts: 3,
	}
	store.Create(context.Background(), job)

	err := retryer.RetryFailed(context.Background(), "job-001")
	if err != nil {
		t.Fatalf("RetryFailed failed: %v", err)
	}

	updated, _ := store.Get(context.Background(), "job-001")
	if updated.Status != JobStatusQueued {
		t.Errorf("expected queued after retry, got %s", updated.Status)
	}
	if updated.Attempts != 2 {
		t.Errorf("expected attempts=2, got %d", updated.Attempts)
	}
}

func TestRetryerNoRetryIfMaxAttempts(t *testing.T) {
	store, _, cleanup := setupTestStoreWithEvents(t)
	defer cleanup()

	retryer := NewRetryer(store, zap.NewNop(), DefaultRetryPolicy())

	job := &Job{
		ID:         "job-001",
		Type:       "test",
		Status:     JobStatusFailed,
		Attempts:  3,
		MaxAttempts: 3,
	}
	store.Create(context.Background(), job)

	err := retryer.RetryFailed(context.Background(), "job-001")
	if err != nil {
		t.Fatalf("RetryFailed failed: %v", err)
	}

	updated, _ := store.Get(context.Background(), "job-001")
	if updated.Status != JobStatusFailed {
		t.Errorf("expected still failed, got %s", updated.Status)
	}
}

func TestRetryerRetryAll(t *testing.T) {
	store, _, cleanup := setupTestStoreWithEvents(t)
	defer cleanup()

	retryer := NewRetryer(store, zap.NewNop(), DefaultRetryPolicy())

	jobs := []*Job{
		{ID: "job-001", Type: "test", Status: JobStatusFailed, Attempts: 1, MaxAttempts: 3},
		{ID: "job-002", Type: "test", Status: JobStatusFailed, Attempts: 2, MaxAttempts: 3},
		{ID: "job-003", Type: "test", Status: JobStatusFailed, Attempts: 3, MaxAttempts: 3},
		{ID: "job-004", Type: "test", Status: JobStatusSucceeded},
	}

	for _, j := range jobs {
		store.Create(context.Background(), j)
	}

	count, err := retryer.RetryAll(context.Background())
	if err != nil {
		t.Fatalf("RetryAll failed: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 jobs retried, got %d", count)
	}
}
