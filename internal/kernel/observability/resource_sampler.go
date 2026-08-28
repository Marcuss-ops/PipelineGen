package observability

import (
	"context"

	"go.uber.org/zap"
)

// ResourceSampleIdentity is the canonical identity stamped on every resource
// observation: the run (attempt) the sample belongs to plus the worker and
// host that produced it. One attempt owns its samples; the worker/host pair
// places them on a physical machine.
type ResourceSampleIdentity struct {
	RunID     string
	JobID     string
	AttemptID string
	WorkerID  string
	Host      string
}

// RunResourceSampler is the run-scoped resource telemetry port consumed by
// the worker. Implementations sample host/process resources every 500ms for
// the given identity and persist each observation; the returned stop
// function halts the loop and is safe to call more than once. Sampling is
// best-effort and must never change the run's outcome: collection or
// persistence failures are logged by the loop, not returned to the caller.
// The concrete lives in internal/platform (procmetrics provider + SQLite
// store), wired at the composition root.
type RunResourceSampler interface {
	SampleLoop(context.Context, ResourceSampleIdentity, *zap.Logger) (stop func())
}
