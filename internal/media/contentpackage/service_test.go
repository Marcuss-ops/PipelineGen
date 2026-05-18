package contentpackage

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/zap"
	"velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
)

func TestHandleJob(t *testing.T) {
	log := zap.NewNop()
	svc := NewService(log, nil, nil)

	// Create a test job
	job := &models.Job{
		ID:   "test-job-1",
		Type: models.JobTypeContentPackage,
		Payload: mustMarshal(map[string]any{
			"title":  "Test Title",
			"style":  "cinematic",
			"assets": "test-assets",
			"output": "test-output",
		}),
	}

	// Create job tools
	tools := &jobs.JobTools{
		Progress: func(progress int, message string) {
			t.Logf("Progress: %d - %s", progress, message)
		},
		Event: func(eventType string, message string, data map[string]any) {
			t.Logf("Event: %s - %s", eventType, message)
		},
		IsCancelled: func() bool { return false },
	}

	// Handle the job
	result, err := svc.HandleJob(context.Background(), job, tools)
	if err != nil {
		t.Fatalf("HandleJob failed: %v", err)
	}

	// Verify result
	if result["title"] != "Test Title" {
		t.Errorf("expected title 'Test Title', got %v", result["title"])
	}
	if result["status"] != "completed" {
		t.Errorf("expected status 'completed', got %v", result["status"])
	}
}

func mustMarshal(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
