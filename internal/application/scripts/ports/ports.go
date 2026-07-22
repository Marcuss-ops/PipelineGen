// Package scripts — ports.go canonicalises the cross-application port
// declarations that the script-feature handlers consume.
//
// PR7 (June 2026): removed references to deleted PipelineUseCase and
// GenerationService. Broker and JobEnqueuer ports remain — Broker is
// consumed by GenerateJobHandler.RegisterJobs (PR6), JobEnqueuer by
// generation_enqueue.go (PR6).
package ports

import (
	"context"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type TopicSourceCache interface {
	GetResearchCache(context.Context, string) (string, error)
	SaveResearchCache(context.Context, string, string, string, int, string) error
}

// Broker is consumed by GenerateJobHandler.RegisterJobs (PR6).
//
// Producers (*appjobs.Service) satisfy the interface structurally —
// the handler is passed as `any` and type-asserted to appjobs.HandlerFunc
// internally by the application-layer service.
type Broker interface {
	RegisterHandler(jobType string, handler any) error
}

// Compile-time assertion that *appjobs.Service (the canonical producer)
// implements Broker. Catches signature drift between consumer-side
// port and producer-side implementation at build time rather than at
// first integration test.
var _ Broker = (*appjobs.Service)(nil)

// JobEnqueuer is consumed by EnqueueGenerationJob (generation_enqueue.go, PR6)
// when submitting a script.generate job to the broker.
//
// The canonical producer is *appjobs.Service which implements
// this port directly (Enqueue takes *job.EnqueueRequest).
type JobEnqueuer interface {
	Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
}

// Compile-time assertion: *appjobs.Service (the canonical producer)
// implements JobEnqueuer directly.
var _ JobEnqueuer = (*appjobs.Service)(nil)
