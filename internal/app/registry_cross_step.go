package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/gin-gonic/gin"
)

// registryCrossStepState carries dependencies produced by the internal-module
// phase and consumed by later registration phases. It is intentionally a
// short-lived value, not part of RegistryWiring: RegistryWiring is the final
// graph result returned to the server, while this value represents the
// dependency edges used during graph construction.
type registryCrossStepState struct {
	SearchFanOut       search.SearchFanOut
	SearchBackends     *search.BackendRegistry
	SearchAggregator   *search.Aggregator
	IdempotencyHandler gin.HandlerFunc
}
