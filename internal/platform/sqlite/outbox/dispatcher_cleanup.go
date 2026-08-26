package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// cleanupRequestV1 is the canonical envelope the VoiceoverCleanupHandler
// consumes. Mirrors the indexRequestV1 family: strict schema-version,
// required event_id, voiceover_id, plus the cleanup-specific payload
// (old/new drive file ids + old local paths).
//
// Required fields (handler fails-fast with TerminalError on
// missing-or-malformed):
//
//   - schema_version     (literal VoiceoverCleanupSchemaVersion)
//   - event_id           (RFC4122 UUID or producer-chosen opaque token)
//   - voiceover_id       (canonical voiceovers.id — shared with
//     media_assets.id primary key)
//   - old_drive_file_id  + new_drive_file_id covered below
//
// Producers MUST NOT embed raw file content, base64-encoded blobs, or
// any payload that would make the event row bloat to MBs. The handler
// reaches back into SQLite (voiceover_id lookup) only if it needs to
// re-derive new_drive_file_id from media_assets when the producer
// sent an empty value (test path or canonical projection missing).
type cleanupRequestV1 struct {
	SchemaVersion  string   `json:"schema_version"`
	EventID        string   `json:"event_id"`
	VoiceoverID    string   `json:"voiceover_id"`
	OldDriveFileID string   `json:"old_drive_file_id"`
	NewDriveFileID string   `json:"new_drive_file_id"`
	OldLocalPaths  []string `json:"old_local_paths"`
	IdempotencyKey string   `json:"idempotency_key"`
	RequestedAt    string   `json:"requested_at,omitempty"`
}

// VoiceoverCleanupSchemaVersion is the canonical, EXACT string the
// VoiceoverCleanupHandler accepts. Producers MUST send
// "voiceover.cleanup.requested.v1" literally. Mismatch is TERMINAL
// (godlike/07 — no fake availability; producers upgrade rather than
// retrying into a repair loop).
const VoiceoverCleanupSchemaVersion = "voiceover.cleanup.requested.v1"

// EnqueueCleanupEvent emits the canonical voiceover.cleanup.requested.v1
// envelope INSIDE a caller-owned *sql.Tx. P0.7 Wave 21 Step 10/12
// (June 2026): the Voiceover finalizer surface uses this method
// instead of `go s.cleanupOrphanVoiceover(...)` (fire-and-forget
// goroutine detached via context.Background). The outbox event
// commits atomically with the voiceovers UPSERT + media_assets
// projection UPSERT + asset.index.requested event, so a tx rollback
// discards the entire finalize surface (no half-state orphans).
//
// Atomicity guarantee: caller passes a *sql.Tx already bound to the
// finalizeStage tx. The Insert into outbox_events runs INSIDE that tx
// so the event row commits (or rolls back) together with the
// voiceovers + media_assets + asset.index.requested writes. The
// post-commit pool later sees the new pending event and runs the
// VoiceoverCleanupHandler asynchronously; that handler is detached
// from the request context so handler cancel / client disconnect
// does not abort cleanup.
//
// eventKey is derived deterministically from voiceoverID + the
// latest old/new drive file id pair, so a re-run of finalizeStage
// (e.g. retry-after-recovery) does not double-enqueue when the
// underlying pair is identical. The canonical hash includes the
// canonical id + the post-swap drive_file_id (the most stable
// fingerprint available).
func (d *Dispatcher) EnqueueCleanupEvent(
	ctx context.Context,
	tx *sql.Tx,
	voiceoverID,
	oldDriveFileID,
	newDriveFileID string,
	oldLocalPaths []string,
) error {
	if d == nil {
		return errors.New("Dispatcher is nil")
	}
	if d.outboxEventsRepo == nil {
		return errors.New("Dispatcher: outbox events repo not configured")
	}
	if voiceoverID == "" {
		return errors.New("Dispatcher.EnqueueCleanupEvent: voiceoverID is required")
	}

	eventID := uuid.NewString()
	eventKey := cleanupEventKey(voiceoverID, oldDriveFileID, newDriveFileID)
	payload := cleanupRequestV1{
		SchemaVersion:  VoiceoverCleanupSchemaVersion,
		EventID:        eventID,
		VoiceoverID:    voiceoverID,
		OldDriveFileID: oldDriveFileID,
		NewDriveFileID: newDriveFileID,
		OldLocalPaths:  oldLocalPaths,
		IdempotencyKey: eventKey,
		RequestedAt:    timeutil.FormatRFC3339(time.Now()),
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("Dispatcher.EnqueueCleanupEvent: marshal v1 cleanup payload %s: %w", voiceoverID, err)
	}

	if _, err := d.outboxEventsRepo.Enqueue(
		ctx, tx,
		outboxevents.EventVoiceoverCleanupRequested,
		voiceoverID,
		"voiceover",
		string(payloadJSON),
		eventKey,
	); err != nil {
		return fmt.Errorf("Dispatcher.EnqueueCleanupEvent: enqueue outbox event %s: %w", voiceoverID, err)
	}

	if d.log != nil {
		d.log.Debug("Dispatcher.EnqueueCleanupEvent: enqueued voiceover.cleanup.requested (v1 envelope, caller-owned tx)",
			zap.String("voiceover_id", voiceoverID),
			zap.String("outbox_event_id", eventID),
			zap.String("old_drive_file_id", oldDriveFileID),
			zap.String("new_drive_file_id", newDriveFileID),
			zap.Int("old_local_paths_count", len(oldLocalPaths)),
		)
	}
	return nil
}

// cleanupEventKey is the canonical event_key constructor for the
// voiceover.cleanup.requested.v1 envelope. It uses the replacement
// Drive ID for normal swap cleanup and falls back to the old/target
// Drive ID for a post-publish orphan where no replacement was
// finalized. This keeps retries deterministic without making an
// orphan event collide on an empty suffix.
func cleanupEventKey(voiceoverID, oldDriveFileID, newDriveFileID string) string {
	driveFileID := newDriveFileID
	if driveFileID == "" {
		driveFileID = oldDriveFileID
	}
	return fmt.Sprintf("voiceover_cleanup:%s:%s", voiceoverID, driveFileID)
}
