// Package asset — delivery_status.go is the canonical 5-state enum
// for asset delivery/publishing status (P0.2, July 2026).
//
// Per the Drive Cutover Verdict §P0.2: YouTube (and any capability that
// uploads to Drive) must surface an explicit delivery status so callers
// can distinguish "Drive succeeded" from "Drive failed but asset is
// registered locally". The pre-P0.2 ambiguous OK=true for both cases
// is eliminated.
//
// Relationship to LifecycleState: LifecycleState tracks the high-level
// asset lifecycle (STAGING/PROCESSING/ACTIVE/DELETED). DeliveryStatus
// tracks the Drive-publishing progress narrowly. They are orthogonal:
//   - LifecycleState = "ACTIVE" + DeliveryStatus = "PUBLISHED"
//     → asset is live and has a Drive file.
//   - LifecycleState = "ACTIVE" + DeliveryStatus = "PUBLISH_FAILED"
//     → asset is registered locally but Drive upload failed; a retry
//     job is scheduled for the next outbox pass.
//   - LifecycleState = "STAGING" + DeliveryStatus = "PUBLISH_PENDING"
//     → asset is awaiting Drive upload.
//
// Do NOT add a sub-state "PUBLISH_RETRYING" — the canonical states
// reflect stable state, not transient worker activity. A separate
// retry_scheduled boolean flags the retry intent.
package asset

// DeliveryStatus is the canonical per-asset Drive publishing progress.
// Stored as a first-class field on the API response DTO; may be promoted
// to a media_assets column in a future migration.
//
// Mirrors the P0.2 Verdict-required enumeration:
//   LOCAL_ONLY → PUBLISH_PENDING → PUBLISHING → PUBLISHED
//   PUBLISH_FAILED (terminal failure, retry scheduled)
type DeliveryStatus string

const (
	// DeliveryStatusLocalOnly — asset is registered locally with no
	// Drive file and no pending publication intent. Typical for assets
	// registered before Publisher existed or for test fixtures.
	DeliveryStatusLocalOnly DeliveryStatus = "LOCAL_ONLY"

	// DeliveryStatusPublishPending — asset is committed to SQLite and
	// an outbox event for publication has been enqueued, but the worker
	// has not yet claimed the slot.
	DeliveryStatusPublishPending DeliveryStatus = "PUBLISH_PENDING"

	// DeliveryStatusPublishing — worker is actively uploading the file
	// to Drive. Transient state; on success transitions to PUBLISHED,
	// on failure transitions to PUBLISH_FAILED.
	DeliveryStatusPublishing DeliveryStatus = "PUBLISHING"

	// DeliveryStatusPublished — terminal success. Drive file exists,
	// DriveFileID and DriveLink are populated, and the asset is
	// ready for downstream consumers (Qdrant indexing, search).
	DeliveryStatusPublished DeliveryStatus = "PUBLISHED"

	// DeliveryStatusPublishFailed — terminal failure. Drive upload
	// failed but the asset is registered in SQLite. A retry job is
	// scheduled (RetryScheduled=true). Operator can trigger a manual
	// republish via the admin CLI or wait for the next retry cycle.
	DeliveryStatusPublishFailed DeliveryStatus = "PUBLISH_FAILED"
)

// Valid returns true if s is one of the canonical DeliveryStatus values.
func (s DeliveryStatus) Valid() bool {
	switch s {
	case DeliveryStatusLocalOnly,
		DeliveryStatusPublishPending,
		DeliveryStatusPublishing,
		DeliveryStatusPublished,
		DeliveryStatusPublishFailed:
		return true
	}
	return false
}

// IsTerminal returns true if the state is terminal (no further automatic
// transitions expected without an explicit retry or operator action).
func (s DeliveryStatus) IsTerminal() bool {
	return s == DeliveryStatusPublished || s == DeliveryStatusPublishFailed || s == DeliveryStatusLocalOnly
}

// IsPublished returns true if the asset has a Drive file and is ready
// for downstream consumption.
func (s DeliveryStatus) IsPublished() bool {
	return s == DeliveryStatusPublished
}
