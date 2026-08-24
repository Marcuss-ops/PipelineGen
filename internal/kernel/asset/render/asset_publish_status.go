// Package asset — asset_publish_status.go is the canonical 5-state enum
// for asset Drive publishing status (P0.2, July 2026).
//
// Per the Drive Cutover Verdict §P0.2: YouTube (and any capability that
// uploads to Drive) must surface an explicit delivery status so callers
// can distinguish "Drive succeeded" from "Drive failed but asset is
// registered locally". The pre-P0.2 ambiguous OK=true for both cases
// is eliminated.
//
// This enum is DISTINCT from delivery.DeliveryStatus in
// internal/domain/delivery/status.go (which tracks
// outbox delivery-attempt lifecycle: PENDING/LEASED/RUNNING/...).
// AssetPublishStatus tracks the per-asset publishing outcome:
//   - LOCAL_ONLY → PUBLISH_PENDING → PUBLISHING → PUBLISHED
//   - PUBLISH_FAILED (terminal failure, retry scheduled)
//
// Relationship to LifecycleState: LifecycleState tracks the high-level
// asset lifecycle (STAGING/PROCESSING/ACTIVE/DELETED). AssetPublishStatus
// tracks the Drive-publishing progress narrowly. They are orthogonal:
//   - LifecycleState = "ACTIVE" + AssetPublishStatus = "PUBLISHED"
//     → asset is live and has a Drive file.
//   - LifecycleState = "ACTIVE" + AssetPublishStatus = "PUBLISH_FAILED"
//     → asset is registered locally but Drive upload failed; a retry
//     job is scheduled for the next outbox pass.
package render

// AssetPublishStatus is the canonical per-asset Drive publishing progress.
// Stored as a first-class field on the API response DTO.
type AssetPublishStatus string

const (
	// AssetPublishLocalOnly — asset is registered locally with no
	// Drive file and no pending publication intent. Typical for assets
	// registered before Publisher existed or for test fixtures.
	AssetPublishLocalOnly AssetPublishStatus = "LOCAL_ONLY"

	// AssetPublishPending — asset is committed to SQLite and an outbox
	// event for publication has been enqueued, but the worker has not
	// yet claimed the slot.
	AssetPublishPending AssetPublishStatus = "PUBLISH_PENDING"

	// AssetPublishPublishing — worker is actively uploading the file to
	// Drive. Transient state; on success transitions to PUBLISHED, on
	// failure transitions to PUBLISH_FAILED.
	AssetPublishPublishing AssetPublishStatus = "PUBLISHING"

	// AssetPublishPublished — terminal success. Drive file exists,
	// DriveFileID and DriveLink are populated.
	AssetPublishPublished AssetPublishStatus = "PUBLISHED"

	// AssetPublishFailed — terminal failure. Drive upload failed but
	// the asset is registered in SQLite. A retry job is scheduled
	// (RetryScheduled=true).
	AssetPublishFailed AssetPublishStatus = "PUBLISH_FAILED"
)

// Valid returns true if s is one of the canonical AssetPublishStatus values.
func (s AssetPublishStatus) Valid() bool {
	switch s {
	case AssetPublishLocalOnly,
		AssetPublishPending,
		AssetPublishPublishing,
		AssetPublishPublished,
		AssetPublishFailed:
		return true
	}
	return false
}

// IsTerminal returns true if the state is terminal (no further automatic
// transitions expected without an explicit retry or operator action).
func (s AssetPublishStatus) IsTerminal() bool {
	return s == AssetPublishPublished || s == AssetPublishFailed || s == AssetPublishLocalOnly
}

// IsPublished returns true if the asset has a Drive file and is ready
// for downstream consumption.
func (s AssetPublishStatus) IsPublished() bool {
	return s == AssetPublishPublished
}
