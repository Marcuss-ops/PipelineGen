// Package job — kernel canonical Service interface + EnqueueRequest.
//
// Phase A.2 (June 2026): migrated from internal/kernel/job/. The
// domain package re-exports as `type Service = kerneljob.Service`
// and `type EnqueueRequest = kerneljob.EnqueueRequest` (aliases,
// transparent at the call-site).
//
// Per godlike/02 kernel rules: interface-signature references (Job,
// Event, Filter, Status) are intra-package — kernel does NOT import
// internal/kernel/job/ to satisfy this interface.
package job

import "context"

// Service is the canonical job-system contract presented to every
// consumer in PipelineGen. It is a Go interface — consumers declare
// their dependency as `job.Service` (interface value, not pointer-to-
// interface) and the composition root injects the concrete
// *application/jobs.Service, which satisfies this interface directly.
//
// Pre-June-2026 this package held a concrete struct facade with
// delegate function pointers. That facade has been eliminated in
// favour of a plain interface + compile-time assertion in the
// application layer (`var _ job.Service = (*appjobs.Service)(nil)`).
//
// Phase A.2 (June 2026): canonical home is internal/kernel/job/.
// The interface method-set signatures reference kernel-internal types
// (Job, Event, Filter, Status) so the kernel package requires zero
// cross-zone imports to satisfy its own surface.
type Service interface {
	Enqueue(ctx context.Context, req *EnqueueRequest) (*Job, error)
	Get(ctx context.Context, id string) (*Job, error)
	Cancel(ctx context.Context, id string) error
	List(ctx context.Context, filter Filter) ([]Job, error)
	IsTerminal(status Status) bool
	RegisterHandler(jobType string, handler any) error
	ListEvents(ctx context.Context, jobID string) ([]Event, error)
	// Retry re-enqueues a failed or retry-waiting job, returning the
	// fresh Job entity (new leasing cycle, retry_count reset).
	// PR-0 (June 2026): promoted from concrete-only helper to
	// canonical service surface — api layer's JobsHandler.Retry calls
	// this through the interface so the api package doesn't leak
	// *appjobs.Service concrete.
	Retry(ctx context.Context, id string) (*Job, error)
}

// EnqueueRequest is the typed payload handed to Service.Enqueue.
//
// The fields map 1-1 to the Job columns written by the SQLite enqueue.
//
// Phase A.2 (June 2026): migrated to kernel canonical home. Reference
// types are stdlib (string/int — no other intra-package deps needed).
type EnqueueRequest struct {
	Type          string
	Payload       any
	CorrelationID string
	MaxRetries    int
	Priority      int
	Project       string
	ActiveKey     string
	VideoName     string
	// ClientID is the M2M client identifier resolved from the Bearer
	// VELOX_M2M_SECRET by JobClientAuthMiddleware. Empty for
	// admin/internal enqueues. Persisted on the Job row so the
	// (client_id, idempotency_key) UNIQUE constraint can dedup M2M
	// retries (PG-M2M, Aug 2026).
	ClientID string
	// IdempotencyKey is the caller-controlled dedup key for the M2M
	// surface. Empty for admin/internal enqueues. Together with
	// ClientID it forms the canonical dedup pair; a retry with the
	// same pair returns the SAME job_id instead of a duplicate
	// (PG-M2M, Aug 2026). Distinct from CorrelationID (request-context
	// derived, not per-client controlled).
	IdempotencyKey string
}
