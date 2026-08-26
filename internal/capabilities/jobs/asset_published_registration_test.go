package jobs

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"go.uber.org/zap"
)

func TestRegisterOptionalHandlers_RegistersInformationalAssetPublished(t *testing.T) {
	registry := outboxevents.NewHandlerRegistry()
	if err := RegisterOptionalHandlers(registry, zap.NewNop(), nil, nil); err != nil {
		t.Fatalf("RegisterOptionalHandlers() error = %v", err)
	}
	h, ok := registry.Get(outboxevents.EventAssetPublished)
	if !ok {
		t.Fatal("asset.published informational handler was not registered")
	}
	if got := h.IdempotencyKey(); got != outboxevents.EventAssetPublished+"."+outboxevents.SchemaVersionAssetPublished {
		t.Fatalf("handler idempotency key = %q", got)
	}
}
