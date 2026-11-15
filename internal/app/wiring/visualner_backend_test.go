package wiring

import (
	"context"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestBuildVisualNERBackendUsesBrainRust(t *testing.T) {
	backend, err := buildVisualNERBackend(&config.Config{}, nil)
	if err != nil || backend == nil {
		t.Fatalf("build rust backend = %v, %v", backend, err)
	}
	var _ interface {
		Extract(context.Context, string, string, int) ([]scriptgen.VisualEntity, error)
	} = backend
}
