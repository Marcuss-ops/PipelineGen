package jobs

import (
	"context"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
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
	service := &Service{}
	stats := bundleTestStats{}
	deps := Dependencies{
		Service:     service,
		Stats:       stats,
		EnabledFunc: func() bool { return true },
		Logger:      zap.NewNop(),
	}

	canonical, err := Build(deps)
	if err != nil {
		t.Fatalf("canonical module Build: %v", err)
	}
	gotDesc, err := NewBundle(service, stats, deps.EnabledFunc, deps.Logger).Build(api.BuildContext{})
	if err != nil {
		t.Fatalf("bundle Build: %v", err)
	}

	if gotDesc.Name != canonical.Name() {
		t.Fatalf("runtime module name = %q, want %q", gotDesc.Name, canonical.Name())
	}

}

type bundleTestStats struct{}

func (bundleTestStats) GetStats(context.Context) (*sqljobs.JobStats, error) {
	return &sqljobs.JobStats{}, nil
}

var _ interface {
	GetStats(context.Context) (*sqljobs.JobStats, error)
} = bundleTestStats{}
