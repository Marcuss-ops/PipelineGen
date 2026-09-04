package jobs

import (
	"errors"

	jobqueue "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Root errors remain compatibility names for callers while queue/kernel own
// the lower-level admission and persistence classifications.
var ErrRegistryRequired = jobqueue.ErrRegistryRequired
var ErrRepoRequired = errors.New("appjobs.Service: repo is required")
var ErrLogRequired = errors.New("appjobs.Service: log is required")
var ErrMaxRetriesUnknown = jobqueue.ErrMaxRetriesUnknown

// Deprecated: use kerneljob.ErrDuplicate. Kept so existing errors.Is callers
// remain source-compatible during the queue extraction.
var ErrUniqueConstraintViolation = kerneljob.ErrDuplicate

// Deprecated: queue owns consumer admission. Kept as a root compatibility
// sentinel because API and wiring callers historically referenced this name.
var ErrNoHandlerForJobType = jobqueue.ErrNoConsumer

var ErrJobsSvcRequiredAtRegistration = errors.New("appjobs: JobsSvc is required at handler-registration time")
var ErrMissingDeps = kerneljob.ErrMissingDeps
