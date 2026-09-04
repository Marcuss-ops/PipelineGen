package wiring

import registrywiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/registry"

// c3ValidateRuntimeGraph is the root compatibility facade. The canonical
// runtime-graph validation now lives in wiring/registry.
func c3ValidateRuntimeGraph() error {
	return registrywiring.ValidateRuntimeGraph()
}
