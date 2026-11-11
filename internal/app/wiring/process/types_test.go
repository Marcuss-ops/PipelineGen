package process

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant"
)

type testPointDeleter struct{}

func (testPointDeleter) DeleteAssetPoints(context.Context, []string) error { return nil }

func TestBuildQdrantSubsystemsPreservesDeleterWhenRuntimeDisabled(t *testing.T) {
	deleter := testPointDeleter{}
	got := BuildQdrantSubsystems(&QdrantDeps{QdrantDeleter: deleter}, &config.Config{}, nil)
	if got.QdrantDeleter != deleter {
		t.Fatal("Qdrant deleter was not preserved when the optional runtime is disabled")
	}
	if got.QdrantRuntime != nil || got.CollectionManager != nil || got.QdrantSearcher != nil {
		t.Fatalf("disabled runtime projected unexpected components: %+v", got)
	}
}

func TestBuildQdrantSubsystemsProjectsSingleRuntime(t *testing.T) {
	runtime := &qdrant.QdrantRuntime{}
	got := BuildQdrantSubsystems(&QdrantDeps{Runtime: runtime}, &config.Config{}, nil)
	if got.QdrantRuntime != runtime {
		t.Fatal("runtime facade was not preserved")
	}
	if got.QdrantDeleter != nil {
		t.Fatal("unexpected deleter when none was configured")
	}
}
