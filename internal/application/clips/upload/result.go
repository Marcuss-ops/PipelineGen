// Package upload — UploadClipResult + typed errors.
//
// Wave 14 step 1 (June 2026): the typed output of UseCase.Execute.
// Mirrors the legacy UploadVideoClipResponse field-for-field so the
// HTTP handler can map cmd-result → JSON without losing keys. Future
// response-shape evolution happens in UploadVideoClipResponse (transport)
// without forcing the use-case signature to drift.
package upload

import "errors"

// UploadClipResult is the typed output of UseCase.Execute.
// Maps 1:1 onto the legacy UploadVideoClipResponse so the handler can
// build the JSON response with a single field copy.
//
// Indexed is false when either:
//
//	(a) the dispatcher was unavailable (503 path),
//	(b) the enrich deps were wired but jobsSvc was nil (deliberate drift
//	    signal — S1a, June 2026).
//
// Duration is in milliseconds (matches the legacy `int(clip.Duration.Milliseconds())`).
type UploadClipResult struct {
	OK          bool
	ClipID      string
	Name        string
	Filename    string
	DriveLink   string
	DriveFileID string
	FileHash    string
	Source      string
	Category    string
	Tags        []string
	LocalPath   string
	Indexed     bool
	Duration    int
}

// ── Typed sentinel errors ───────────────────────────────────────────────────

// ErrArtifactServiceUnavailable is returned when the artifact service
// port was not wired. Maps to HTTP 500 in the handler.
var ErrArtifactServiceUnavailable = errors.New("upload: artifact service not configured")

// ErrDriveUploaderPortMissing is returned only when the Drive uploader
// port is nil at wiring time — a hard production-fault. Transient
// "Drive upload failed, continuing" paths are NOT errors: the use case
// logs+continues and returns UploadClipResult with empty DriveLink,
// matching the legacy handler's manual-upload best-effort contract.
var ErrDriveUploaderPortMissing = errors.New("upload: drive uploader port not configured")

// ErrDispatcherUnavailable mirrors the canonical QDRANT-002 fail-closed
// sentinel. When AssetMutationDispatcher is not wired at composition
// time, the use case refuses to silently fall back to a raw repo.Upsert.
// Maps to HTTP 503 in the handler — same semantics as clip_create.go.
var ErrDispatcherUnavailable = errors.New("upload: AssetMutationDispatcher not wired (QDRANT-asset-mutation isolation; production composition root must wire *outbox.Dispatcher via clipsDispatcherAdapter)")

// NOTE: there is intentionally no ErrJobsServiceUnavailable. The
// legacy handler logs+continues with Indexed=false when jobsSvc is
// nil (S1a, June 2026 — "truthful signal not misleading fallback"
// contract from clip_create.go). The use case mirrors this: a nil
// JobsSvc emits a WARN log and sets Indexed=false on the result,
// never an error. Reactive re-index via
// POST /api/assets/operator/assets/:id/reindex is the operator-facing
// recovery path.
