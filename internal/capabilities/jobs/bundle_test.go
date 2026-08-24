package jobs

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	"go.uber.org/zap"
)

func TestBundleBuildDelegatesCanonicalModuleValidation(t *testing.T) {
	bundle := NewBundle(nil, nil, nil, zap.NewNop())
	if bundle.Name() != "jobs" {
		t.Fatalf("Name() = %q, want jobs", bundle.Name())
	}
	if _, err := bundle.Build(api.BuildContext{}); err == nil {
		t.Fatal("Build with nil dependencies returned nil error")
	}
}

func TestBundleBuildPreservesCanonicalModuleDescriptor(t *testing.T) {
	service := &bundleTestService{}
	stats := bundleTestStats{}
	deps := Dependencies{
		Service:     service,
		Stats:       stats,
		EnabledFunc: func() bool { return true },
		Logger:      zap.NewNop(),
	}

	canonical, err := NewModule(deps).Build(api.BuildContext{})
	if err != nil {
		t.Fatalf("canonical module Build: %v", err)
	}
	got, err := NewBundle(service, stats, deps.EnabledFunc, deps.Logger).Build(api.BuildContext{})
	if err != nil {
		t.Fatalf("bundle Build: %v", err)
	}

	if got.Name != canonical.Name {
		t.Fatalf("runtime module name = %q, want %q", got.Name, canonical.Name)
	}
	if len(got.Routes) != len(canonical.Routes) {
		t.Fatalf("route count = %d, want %d", len(got.Routes), len(canonical.Routes))
	}
	for i := range canonical.Routes {
		wantRoute, gotRoute := canonical.Routes[i], got.Routes[i]
		if gotRoute.Capability != wantRoute.Capability {
			t.Errorf("route[%d] capability = %q, want %q", i, gotRoute.Capability, wantRoute.Capability)
		}
		if gotRoute.RegistrationPoint != wantRoute.RegistrationPoint {
			t.Errorf("route[%d] registration point = %q, want %q", i, gotRoute.RegistrationPoint, wantRoute.RegistrationPoint)
		}
		if gotRoute.Module == nil || wantRoute.Module == nil {
			t.Fatalf("route[%d] module nil: got=%v want=%v", i, gotRoute.Module, wantRoute.Module)
		}
		if gotRoute.Module.Name() != wantRoute.Module.Name() {
			t.Errorf("route[%d] module name = %q, want %q", i, gotRoute.Module.Name(), wantRoute.Module.Name())
		}
		if gotRoute.Module.Enabled() != wantRoute.Module.Enabled() {
			t.Errorf("route[%d] enabled = %v, want %v", i, gotRoute.Module.Enabled(), wantRoute.Module.Enabled())
		}
	}
}

// bundleTestService is deliberately small in behavior but complete in shape:
// the bundle test exercises composition with valid ports without depending on
// the concrete application service or SQLite.
type bundleTestService struct{}

func (*bundleTestService) Enqueue(context.Context, *job.EnqueueRequest) (*job.Job, error) {
	return nil, nil
}
func (*bundleTestService) Get(context.Context, string) (*job.Job, error) { return nil, nil }
func (*bundleTestService) Cancel(context.Context, string) error          { return nil }
func (*bundleTestService) List(context.Context, job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (*bundleTestService) IsTerminal(job.Status) bool { return false }
func (*bundleTestService) RegisterHandler(string, any) error {
	return nil
}
func (*bundleTestService) ListEvents(context.Context, string) ([]job.Event, error) {
	return nil, nil
}
func (*bundleTestService) Retry(context.Context, string) (*job.Job, error) { return nil, nil }

var _ job.Service = (*bundleTestService)(nil)

type bundleTestStats struct{}

func (bundleTestStats) GetStats(context.Context) (*sqljobs.JobStats, error) {
	return &sqljobs.JobStats{}, nil
}

var _ interface {
	GetStats(context.Context) (*sqljobs.JobStats, error)
} = bundleTestStats{}
