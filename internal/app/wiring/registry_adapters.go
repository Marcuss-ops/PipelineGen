package wiring

import (
	registrywiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/registry"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
)

// newMutationsDispatcherAdapter is the root compatibility facade. The adapter
// implementation now lives with registry composition ownership.
func newMutationsDispatcherAdapter(disp *outbox.Dispatcher) (mutations.AssetMutationDispatcher, error) {
	return registrywiring.NewMutationsDispatcherAdapter(disp)
}
