package app

import (
	"context"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/worker"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// BuildWorkerRegistry creates a remote-worker Registry populated with the
// same handlers wired into the in-process Dispatcher. Each handler is
// adapted so that worker.Tools is translated into appjobs.JobTools.
// The returned capability slice is derived from the registry itself — it is
// the single source of truth for what this worker can execute.
//
// Returns worker.ErrNoHandlers if the Dispatcher has zero registered
// handlers, preventing the remote worker from starting with an empty
// registry that would silently claim every job.
func BuildWorkerRegistry(root *ComposeRoot) (*worker.Registry, []string, error) {
	if root == nil || root.Jobs == nil || root.Jobs.Dispatcher == nil {
		return nil, nil, fmt.Errorf("compose root or jobs dispatcher is nil")
	}
	reg := worker.NewRegistry()
	for jobType, h := range root.Jobs.Dispatcher.AllHandlers() {
		if err := reg.Register(jobType, adaptHandler(h)); err != nil {
			return nil, nil, fmt.Errorf("register handler for %s: %w", jobType, err)
		}
	}
	if reg.Len() == 0 {
		return nil, nil, worker.ErrNoHandlers
	}
	caps := reg.JobTypes()
	return reg, caps, nil
}

// adaptHandler bridges the in-process HandlerFunc signature (which expects
// *appjobs.JobTools) with the remote worker Handler signature (which expects
// *worker.Tools). Progress, cancellation and events are forwarded via the
// broker-backed Tools implementation.
func adaptHandler(h appjobs.HandlerFunc) worker.Handler {
	return func(ctx context.Context, j *job.Job, tools *worker.Tools) (map[string]any, error) {
		jobTools := &appjobs.JobTools{
			Progress: func(p int, msg string) {
				_ = tools.Progress(ctx, p, msg)
			},
			Event: func(eventType, msg string, data map[string]any) {
				// worker broker does not support events yet; silently drop.
			},
			IsCancelled: func() bool {
				ok, _ := tools.IsCancelled(ctx)
				return ok
			},
		}
		return h(ctx, j, jobTools)
	}
}
