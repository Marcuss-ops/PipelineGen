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
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TopicSourceCache is the canonical port for reading and writing the
// research_cache. Callers compute the cache key via
// scriptpkg.ComputeResearchCacheKey and pass a ResearchCacheRecord to
// SaveResearchCache.
type TopicSourceCache interface {
	// GetResearchCache looks up a non-expired research_cache row by its
	// canonical key. On hit it returns the cached source text and bumps
	// hit_count/last_used atomically. On miss or expiry it returns
	// ("", nil).
	GetResearchCache(ctx context.Context, key string) (string, error)

	// SaveResearchCache persists a ResearchCacheRecord. The record Key
	// must already be computed via scriptpkg.ComputeResearchCacheKey.
	SaveResearchCache(ctx context.Context, rec scriptpkg.ResearchCacheRecord) error
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
