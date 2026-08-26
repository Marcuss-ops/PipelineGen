// Package outbox — drive_delete_envelope.go
//
// Producer-side mirror of application/jobs/outbox.DriveDeleteHandler.
// v1 wire envelope for the Drive Trash (or hard-Delete) step of the
// deletion state machine (Blocco 3.1, June 2026):
//
//	ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING → DELETED
//
// Convention parallels delete_envelope.go: this envelope is emitted by
// outbox.Dispatcher.EnqueueDriveDelete (tx-bound). The schema_version
// literal MUST match the consumer's DriveDeleteRequestSchemaVersion
// constant (declared in application/jobs/outbox/drive_delete.go when
// Blocco 3.1 lands) — mismatch is classified as TERMINAL by the
// consumer (QDRANT-002 PR4 invariant I, June 2026).
//
// Permanently is the only Blocco 3.1 addition over delete_envelope.go:
// true routes Drive.Delete (permanent, Files.Delete SDK call); false
// routes Drive.Trash (recoverable, Files.Update{Trashed:true}). The
// consumer-side decision lives in DriveDeleteHandler in
// application/jobs/outbox/drive_delete.go.
package outbox

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"fmt"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"
)

// DriveDeleteRequestSchemaVersion is the canonical, EXACT string the
// handler accepts (mirrors DeleteRequestSchemaVersion in delete_envelope.go
// and the consumer-side counterparts in
// application/jobs/outbox/index_delete.go::DeleteRequestSchemaVersion +
// .../drive_delete.go::DriveDeleteRequestSchemaVersion which the
// consumer declares at its own scope).
//
// Producers MUST send "asset.drive.delete_requested.v1" literally.
// Mismatch is TERMINAL — no retry — so producers upgrade instead of
// silently retrying on what looks like a routine failure.
const DriveDeleteRequestSchemaVersion = "asset.drive.delete_requested.v1"

// driveDeleteRequestV1 is the canonical envelope for asset.drive.delete_requested.v1
// events.
type driveDeleteRequestV1 struct {
	SchemaVersion  string `json:"schema_version"`
	EventID        string `json:"event_id"`
	AssetID        string `json:"asset_id"`
	Permanently    bool   `json:"permanently,omitempty"`
	RequestedAt    string `json:"requested_at,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

// buildDriveDeleteRequestV1 constructs the canonical producer-side
// envelope for an asset.drive.delete_requested.v1 event.
//
// id is the canonical media_assets.id; permanently propagates from
// DeletionService.DeleteClip(ctx, source, clipID, permanently) — true
// selects Files.Delete (permanent), false selects Files.Trash.
// idempotency_key is the dedup token shared with the
// outbox_events.event_key column; v1 conflation invariant is enforced
// by the dispatcher.
func buildDriveDeleteRequestV1(id string, permanently bool) driveDeleteRequestV1 {
	return driveDeleteRequestV1{
		SchemaVersion:  DriveDeleteRequestSchemaVersion,
		EventID:        uuid.NewString(),
		AssetID:        id,
		Permanently:    permanently,
		RequestedAt:    timeutil.FormatRFC3339(time.Now()),
		IdempotencyKey: driveDeleteEventKey(id, permanently),
	}
}

// driveDeleteEventKey computes the canonical event_key for a drive
// delete event. Shape is `drive_delete:<permanently?>:<asset_id>` —
// the permanently prefix matters because a Trash → Delete upgrade
// (or vice versa) is a distinct intent: a re-enqueue with a flipped
// flag must NOT collapse to the prior event. ON CONFLICT(event_key)
// DO NOTHING at the outbox layer absorbs repeated POSTs but emits
// exactly one row per unique (asset_id, permanently) pair, so an
// operator rapidly toggling `permanently` between requests sees both
// events delivered as separate hops.
func driveDeleteEventKey(assetID string, permanently bool) string {
	return fmt.Sprintf("drive_delete:%t:%s", permanently, assetID)
}
