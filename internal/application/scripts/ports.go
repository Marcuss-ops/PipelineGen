// Package scripts — ports.go canonicalises the cross-application port
// declarations that the script-feature handlers consume.
//
// AGENT-2 (June 2026): replaces the structural `jobRegistrar` that lived
// inline in pipeline_usecase.go with the typed Broker port below. The
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
// Producers (`*jobs.Service`) satisfy the interface structurally — the
// shape is identical to `*jobs.Service.RegisterHandler(jobType string,
// handler appjobs.HandlerFunc) error`. Tests may use a stub that mimics
// the same signature via the lightweight interface below.
//
// Cross-package coupling: scripts → application/jobs for the canonical
// HandlerFunc type alias. AGENTS.md permits application-sibling imports
// for typed shims; the alternative (duplicating the handler signature)
// defeated static typing and degraded diff quality across cycles.
type Broker interface {
	RegisterHandler(jobType string, handler appjobs.HandlerFunc) error
}

// Compile-time assertion that *jobs.Service (the canonical producer)
// implements Broker. Catches signature drift between consumer-side
// port and producer-side implementation at build time rather than at
// first integration test.
var _ Broker = (*appjobs.Service)(nil)

// JobEnqueuer is the canonical port that GenerationService consumes
// when translating an HTTP /api/script/generate-from-clips request into
// a queued background job.
//
// IMPORTANT (Phase 2 activation, June 2026): the canonical wiring is
// `root.Jobs.Facade`, NOT `root.Jobs.Service`.
//
//   - root.Jobs.Facade  = *job.Service (domain facade,
//     internal/domain/job) — satisfies JobEnqueuer because its
//     `Enqueue(ctx, *job.EnqueueRequest) (*job.Job, error)` method
//     matches the port signature exactly. Internally the facade
//     converts *job.EnqueueRequest → *appjobs.EnqueueRequest via its
//     installed EnqueueFn closure (see internal/app/module_jobs.go).
//   - root.Jobs.Service = *appjobs.Service (concrete impl,
//     internal/application/jobs) — does NOT satisfy this port because
//     its `Enqueue` signature takes *appjobs.EnqueueRequest, a
//     distinct type even though the field set is identical to
//     *job.EnqueueRequest. To use the concrete impl directly the port
//     would have to switch to *appjobs.EnqueueRequest, which defeats
//     the goal of decoupling the scripts package from
//     internal/application/jobs.
//
// The two previous stub constructors accepted `jobsFacade interface{}` —
// a structural widening that broke at first integration because the
// caller had no type to assert against. JobEnqueuer restores a typed
// contract: signature drift is caught at build time; a nil port
// produces an explicit "generation service not initialized" error at
// first HTTP call rather than a silent no-op.
type JobEnqueuer interface {
	Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
}

// Compile-time assertion: *job.Service (the canonical producer that
// composition hands as root.Jobs.Facade) implements JobEnqueuer.
// Catches signature drift at build time rather than at first
// integration test.
var _ JobEnqueuer = (*job.Service)(nil)
