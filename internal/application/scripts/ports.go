// Package scripts — ports.go canonicalises the cross-application port
// declarations that the script-feature handlers consume.
//
// Replaces the structural `jobRegistrar` that lived inline in
// pipeline_usecase.go with the typed Broker port below. The
// port uses the canonical `appjobs.HandlerFunc` shape from
// `internal/application/jobs` so consumer and producer share a single
// typed handler contract; the structural widening of `RegisterJobs'
// parameter was a temporary workaround to bridge the cross-package
// types, lifted permanently here.
//
// JobEnqueuer (Phase 2 activation, June 2026) — companion port for
// the GenerationService async-enqueue path. Introduced because the
// two previous stub constructors accepted `jobsFacade interface{}`
// (a structural widening), which duplicated the production signature
// without giving the consumer a typed failure mode at first call.
// Mirrors the lessons/generate_usecase.go + books/process_usecase.go
// convention to keep application-sibling ports consistent.
package scripts

import (
	"context"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// Broker is the canonical port that PipelineUseCase.RegisterJobs consumes.
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

// JobEnqueuer is the canonical port that GenerationService consumes
// when translating an HTTP /api/script/generate-from-clips request into
// a queued background job.
//
// The canonical producer is *appjobs.Service which now implements
// this port directly (Enqueue takes *job.EnqueueRequest).
type JobEnqueuer interface {
	Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
}

// Compile-time assertion: *appjobs.Service (the canonical producer)
// implements JobEnqueuer directly.
var _ JobEnqueuer = (*appjobs.Service)(nil)
