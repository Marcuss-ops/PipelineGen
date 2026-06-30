package clips

import "context"

// ── Voiceover / Images / Jobs / Deletion port surface for cleanup ────

// CleanupServicePort is the narrowed surface of *deletion.DeletionService
// consumed by clip_ops. The full DeletionService has many other
// methods; we expose only what Reconcile/Cleanup/VerifyClip need.
type CleanupServicePort interface {
	CleanupOrphanFiles(ctx context.Context, path string, dryRun bool) (int, error)
	DeleteClip(ctx context.Context, source, clipID string, hardDelete bool) error
}

// JobsServicePort is the narrowed surface of `jobservice.Service`
// for enqueuing "system.cleanup" jobs in deep mode. Repurposes the
// existing port `domain/job` to avoid a re-import in this file.
type JobsServicePort interface {
	Enqueue(ctx context.Context, req JobsEnqueueRequest) (*JobsEnqueueResponse, error)
}

// JobsEnqueueRequest is a small DTO that mirrors the relevant fields
// of the canonical `*job.EnqueueRequest` so this file avoids importing
// domain/job (kept minimal — the adapter at the composition root
// builds the canonical request).
type JobsEnqueueRequest struct {
	Type      string
	Payload   map[string]any
	Priority  int
	ActiveKey string
}

// JobsEnqueueResponse mirrors the relevant fields of the canonical
// `*job.Job` so handlers can render {job_id: ...} without importing
// the canonical domain type. Adapter: minimal projection at the
// composition root.
type JobsEnqueueResponse struct {
	ID string
}
