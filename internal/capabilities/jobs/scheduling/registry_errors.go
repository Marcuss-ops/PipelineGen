package scheduling

import jobqueue "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"

// The registry lives in the scheduling policy layer now. Keep the sentinel
// identities tied to queue so errors.Is remains stable across the root facade.
var (
	ErrRegistryRequired  = jobqueue.ErrRegistryRequired
	ErrMaxRetriesUnknown = jobqueue.ErrMaxRetriesUnknown
)
