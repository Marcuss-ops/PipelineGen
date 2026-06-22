package app

import (
	"context"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/worker"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

func TestBuildWorkerRegistry_EmptyDispatcher(t *testing.T) {
	root := &ComposeRoot{
		Jobs: &JobsBundle{
			Dispatcher: appjobs.NewDispatcher(),
		},
	}
	reg, caps, err := BuildWorkerRegistry(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.Len() != 0 {
		t.Fatalf("expected empty registry, got %d handlers", reg.Len())
	}
	if len(caps) != 0 {
		t.Fatalf("expected empty caps, got %v", caps)
	}
}

func TestBuildWorkerRegistry_WithHandlers(t *testing.T) {
	dispatcher := appjobs.NewDispatcher()
	_ = dispatcher.Register("test.job", func(_ context.Context, _ *job.Job, _ *appjobs.JobTools) (map[string]any, error) {
		return nil, nil
	})

	root := &ComposeRoot{
		Jobs: &JobsBundle{
			Dispatcher: dispatcher,
		},
	}
	reg, caps, err := BuildWorkerRegistry(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.Len() != 1 {
		t.Fatalf("expected 1 handler, got %d", reg.Len())
	}
	if len(caps) != 1 || caps[0] != "test.job" {
		t.Fatalf("expected caps [test.job], got %v", caps)
	}
}

func TestBuildWorkerRegistry_NilRoot(t *testing.T) {
	_, _, err := BuildWorkerRegistry(nil)
	if err == nil {
		t.Fatal("expected error for nil root")
	}
}

func TestBuildWorkerRegistry_NilJobs(t *testing.T) {
	_, _, err := BuildWorkerRegistry(&ComposeRoot{})
	if err == nil {
		t.Fatal("expected error for nil jobs")
	}
}

func TestWorkerRegistryLen(t *testing.T) {
	reg := worker.NewRegistry()
	if reg.Len() != 0 {
		t.Fatalf("expected 0, got %d", reg.Len())
	}
	_ = reg.Register("x", func(_ context.Context, _ *job.Job, _ *worker.Tools) (map[string]any, error) {
		return nil, nil
	})
	if reg.Len() != 1 {
		t.Fatalf("expected 1, got %d", reg.Len())
	}
}
