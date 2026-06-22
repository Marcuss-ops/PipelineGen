package app

import (
	"context"
	"errors"
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
	_, _, err := BuildWorkerRegistry(root)
	if !errors.Is(err, worker.ErrNoHandlers) {
		t.Fatalf("expected ErrNoHandlers, got %v", err)
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

func TestBuildWorkerRegistry_DerivedCapsSorted(t *testing.T) {
	dispatcher := appjobs.NewDispatcher()
	for _, jt := range []string{"z.job", "a.job", "m.job"} {
		jt := jt
		_ = dispatcher.Register(jt, func(_ context.Context, _ *job.Job, _ *appjobs.JobTools) (map[string]any, error) {
			return nil, nil
		})
	}
	root := &ComposeRoot{
		Jobs: &JobsBundle{
			Dispatcher: dispatcher,
		},
	}
	_, caps, err := BuildWorkerRegistry(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(caps) != 3 {
		t.Fatalf("expected 3 caps, got %d", len(caps))
	}
	for i := 1; i < len(caps); i++ {
		if caps[i-1] > caps[i] {
			t.Fatalf("caps not sorted: %v", caps)
		}
	}
}
