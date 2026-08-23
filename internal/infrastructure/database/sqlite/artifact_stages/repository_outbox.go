// internal/infrastructure/database/sqlite/artifact_stages/repository_outbox.go —
// atomic stage-row + outbox-event co-emission (InsertWithOutbox).
// Extracted from repository.go; no behavior change.
package artifactstages

import (
	"context"
	"fmt"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── InsertWithOutbox ───────────────────────────────────────────────────

// InsertWithOutbox atomically writes a new artifact_stages row AND
// co-emits a corresponding outbox_events row in a SINGLE SQLite
// transaction. The event_key convention is
// `stage:<jobID>:<stageID>` so consumers can dedupe re-deliveries
// via the ux_outbox_events_event_key unique index.
//
// godlike/07 atomicity: BOTH inserts commit together or NEITHER
// commits. A partial commit would orphan an event without its
// stage row (or vice versa); the TX wrapper + defer-rollback
// prevents this. Returns the canonical event_key on success so
// application-layer services can log it for observability.
//
// The deferred Rollback() is a no-op if the TX has already been
// committed (Commit() detaches the deferred allocation) —
// idiomatic Go pattern. A nil-driver error path (e.g.
// sql.ErrConnDone after ctx cancel) surfaces via the wrapped
// ExecContext error, which preserves errors.Is(err, ctx.Err())
// chains for the caller.
func (r *Repository) InsertWithOutbox(ctx context.Context, stage *artifact.ArtifactStage, eventType string, payload []byte) (string, error) {
	if err := validateForWrite(stage); err != nil {
		return "", err
	}
	if eventType == "" {
		return "", fmt.Errorf("%w: eventType is required (cannot be empty)", artifact.ErrOutboxEmit)
	}
	if len(payload) == 0 {
		return "", fmt.Errorf("%w: payload is required (cannot be empty bytes)", artifact.ErrOutboxEmit)
	}

	now := r.now()
	if stage.CreatedAt.IsZero() {
		stage.CreatedAt = now
	}
	stage.UpdatedAt = now

	eventKey := fmt.Sprintf("stage:%s:%s", stage.JobID, stage.ID)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("artifact_stages.InsertWithOutbox: begin tx (id=%s): %w", stage.ID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// 1. INSERT INTO artifact_stages (canonical column set).
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO artifact_stages (`+selectColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stage.ID, stage.JobID, stage.LocalPath, stage.Hash, stage.Size, stage.Mime,
		string(stage.Requirement), stage.Destination, string(stage.State),
		stage.AttemptCount, stage.LastError, stage.PublishedLocation,
		timeutil.FormatPtrRFC3339Nano(stage.PublishedAt),
		timeutil.FormatRFC3339Nano(stage.CreatedAt),
		timeutil.FormatRFC3339Nano(stage.UpdatedAt),
	); err != nil {
		return "", fmt.Errorf("artifact_stages.InsertWithOutbox: insert stage (id=%s): %w", stage.ID, err)
	}

	// 2. INSERT INTO outbox_events (event_type + payload + event_key).
	//    aggregate_type='artifact_stage' so consumers can filter by
	//    stage aggregate. created_at + updated_at populated for
	//    canonical observability.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox_events (event_type, aggregate_id, aggregate_type, payload_json, event_key, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		eventType, stage.ID, "artifact_stage", string(payload),
		eventKey, "pending",
		timeutil.FormatRFC3339Nano(now), timeutil.FormatRFC3339Nano(now),
	); err != nil {
		// godlike/07 typed wrap: callers errors.Is-probe ErrOutboxEmit.
		return "", fmt.Errorf("%w: insert outbox event (event_key=%q event_type=%q): %v", artifact.ErrOutboxEmit, eventKey, eventType, err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("artifact_stages.InsertWithOutbox: commit (event_key=%q): %w", eventKey, err)
	}
	committed = true
	return eventKey, nil
}
