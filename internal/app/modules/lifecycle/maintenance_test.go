package lifecycle

import (
	"context"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

type maintenanceJobServiceStub struct{ calls int }

func (s *maintenanceJobServiceStub) Enqueue(context.Context, *job.EnqueueRequest) (*job.Job, error) {
	s.calls++
	return &job.Job{ID: "scheduled"}, nil
}
func (s *maintenanceJobServiceStub) Get(context.Context, string) (*job.Job, error) {
	return nil, nil
}
func (s *maintenanceJobServiceStub) List(context.Context, job.Filter) ([]job.Job, error) {
	return nil, nil
}

func TestBuildMaintenanceStepsKeepsSchedulersDeferred(t *testing.T) {
	enqueuer := &maintenanceJobServiceStub{}
	steps := BuildMaintenanceSteps(MaintenanceDeps{
		Config:   &config.Config{},
		Services: MaintenanceServices{Jobs: enqueuer},
		Log:      zap.NewNop(),
	})
	if len(steps) != 2 {
		t.Fatalf("maintenance steps = %d, want 2 scheduled maintenance steps", len(steps))
	}
	if steps[0].Name != "maintenance-scheduler-maintenance" || steps[1].Name != "maintenance-scheduler-backup" {
		t.Fatalf("scheduler order = %q, %q", steps[0].Name, steps[1].Name)
	}
	if enqueuer.calls != 0 {
		t.Fatalf("enqueued %d jobs while building startup plan; work must wait for Start", enqueuer.calls)
	}
}
