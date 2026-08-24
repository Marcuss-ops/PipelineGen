package clips

import "errors"

// ── Typed error sentinels for the fix-hash flow (S1d, June 2026) ───────
//
// Pair with errors.Is for branching on caller-side. The handler layer
// (api/assets/clips/clip_ops.go::HandleFixHash) translates each sentinel
// into the canonical HTTP status code:
//
//	ErrFixHashVoiceoverUnsupported   → 400 (unsupported source)
//	ErrFixHashMissingDriveLink       → 409 (clip has no Drive mirror)
//	ErrFixHashDispatcherUnavailable  → 503 (dispatcher not wired)
//
// Wave 22 PR-5 polish (June 2026) adds:
//
//	ErrJobsUnavailable               → 503 (jobs service not wired)
//
// Other errors (decode / repo read / Drive API) fall through as
// 500 Internal Error with a typed wrap.
var (
	ErrFixHashVoiceoverUnsupported  = errors.New("fix-hash not supported for voiceover source")
	ErrFixHashMissingDriveLink      = errors.New("fix-hash: clip has no drive_link / download_link to query")
	ErrFixHashDispatcherUnavailable = errors.New("fix-hash: asset-mutation dispatcher not wired")

	// ErrJobsUnavailable is the typed sentinel returned by Cleanup
	// when s.jobs (JobsServicePort) is nil — test fixtures, partial
	// deployments, or composition roots that assemble the service
	// without wiring jobs. Callers (the api handler) surface this
	// as 503 Service Unavailable.
	ErrJobsUnavailable = errors.New("cleanup requires jobs service (no sync pagination fallback — use POST /:source/cleanup)")

	// ErrInvalidSource is the typed sentinel returned by Cleanup
	// when the source parameter is not a canonical cleanup source.
	ErrInvalidSource = errors.New("invalid cleanup source")

	// ErrReconcileQueueUnavailable is the typed sentinel returned by
	// Reconcile when s.jobs is nil OR the broker-side enqueue fails.
	// PR-3 (June 2026): the previous log-only stub has been removed;
	// every Reconcile call must enqueue a durable catalog.sync job.
	ErrReconcileQueueUnavailable = errors.New("RECONCILE_QUEUE_UNAVAILABLE: reconcile requires jobs service (catalog.sync broker missing)")
)
