package app

import (
	"context"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/worker"
	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// BuildWorkerRegistry creates a remote-worker Registry populated with the
// same handlers wired into the in-process Dispatcher. Each handler is
// adapted so that worker.Tools is translated into appjobs.JobTools.
// The returned capability slice is derived from the registry itself — it is
// the single source of truth for what this worker can execute.
func BuildWorkerRegistry(root *ComposeRoot) (*worker.Registry, []string, error) {
	if root == nil || root.Jobs == nil || root.Jobs.Dispatcher == nil {
		return nil, nil, fmt.Errorf("compose root or jobs dispatcher is nil")
	}
	reg := worker.NewRegistry()
	var caps []string
	for jobType, h := range root.Jobs.Dispatcher.AllHandlers() {
		if err := reg.Register(jobType, adaptHandler(h)); err != nil {
			return nil, nil, fmt.Errorf("register handler for %s: %w", jobType, err)
		}
		caps = append(caps, jobType)
	}
	return reg, caps, nil
}

// adaptHandler bridges the in-process HandlerFunc signature (which expects
// *appjobs.JobTools) with the remote worker Handler signature (which expects
// *worker.Tools). Progress, cancellation and events are forwarded via the
// broker-backed Tools implementation.
func adaptHandler(h appjobs.HandlerFunc) worker.Handler {
	return func(ctx context.Context, j *domainjob.Job, tools *worker.Tools) (map[string]any, error) {
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
