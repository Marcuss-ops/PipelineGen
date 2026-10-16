// decode.go owns the JSONB decode helpers shared by the read paths: input
// normalization, schema-version mapping and corrupted-JSONB surfacing.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// normalizeJSON returns the raw JSON, defaulting empty input to "{}".
func normalizeJSON(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []byte("{}"), nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid json")
	}
	return raw, nil
}

// schemaVersion maps a nullable job_schema_version column to an int,
// defaulting to the v1 envelope version when the column is NULL (legacy rows).
func schemaVersion(v sql.NullInt64) int {
	if !v.Valid {
		return model.JobSchemaVersionV1
	}
	return int(v.Int64)
}

// decodeFrameRange extracts the frame range from a frame_range JSONB.
// Corrupt JSONB is fail-closed: the caller must surface the error so the job
// is poisoned rather than silently rendered as a full clip.
func decodeFrameRange(raw []byte) (*model.FrameRange, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var result model.FrameRange
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("postgres: corrupt frame_range jsonb (%d bytes): %w", len(raw), err)
	}
	return &result, nil
}

func decodeAssets(raw []byte) ([]model.AssetRef, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m inputManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("postgres: corrupt input_manifest jsonb (%d bytes): %w", len(raw), err)
	}
	return m.Assets, nil
}

// poisonCorruptJob marks a job failed due to corrupt JSONB so it never
// renders as full-plan and never drops SHA256 verification. It is called
// inside the Claim/Get transaction while the row is still locked.
//
// The state transition IS the poison, so its failure is returned: a swallowed
// error here leaves the row claimable, and every subsequent claim would poison
// it again without ever recording why. The attempt/event bookkeeping below
// stays best-effort by design (it is history, not the poison itself).
func poisonCorruptJob(ctx context.Context, tx *sql.Tx, jobID string, reason string) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = `+stateLiteral(model.StateFailed)+`, failed_at = now(), error_message = $2,
		    current_worker_id = NULL, lease_until = NULL
		WHERE id = $1`, jobID, reason); err != nil {
		return fmt.Errorf("poison job %s: %w", jobID, err)
	}
	// Best-effort attempt/event — poison must not fail because attempt bookkeeping failed.
	if attempt, err := runningAttemptID(ctx, tx, jobID); err == nil && attempt != "" {
		_ = finishAttempt(ctx, tx, attempt, attemptStatusFailed, "", reason)
		_ = recordEvent(ctx, tx, eventJobFailed, jobID, attempt, "", map[string]any{"reason": reason})
	} else {
		_ = recordEvent(ctx, tx, eventJobFailed, jobID, "", "", map[string]any{"reason": reason})
	}
	return nil
}

// poisonCorruptClaim is the fail-closed handling of a corrupt JSONB column
// found while a job row is locked in a claim transaction: poison the job,
// persist the poison, and report the decode error.
//
// Both failure modes are surfaced instead of dropped. A poison that was not
// written (or was rolled back by a failed commit) would leave the job
// claimable, so the queue would hand the same corrupt row to a worker again
// and again, failing identically every time with no durable explanation.
func poisonCorruptClaim(ctx context.Context, tx *sql.Tx, jobID string, decodeErr error) error {
	if err := poisonCorruptJob(ctx, tx, jobID, decodeErr.Error()); err != nil {
		return fmt.Errorf("postgres: corrupt job %s could not be poisoned: %w (decode: %v)", jobID, err, decodeErr)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres: corrupt job %s: poison not persisted: %w (decode: %v)", jobID, err, decodeErr)
	}
	return decodeErr
}

// poisonCorruptRead is the Get-side counterpart of poisonCorruptClaim: the row
// is read outside a transaction, so the poison is one statement. Its failure is
// reported rather than dropped — a row that could not be poisoned would keep
// being reported as corrupt on every read with nothing saying why.
func (r *Repository) poisonCorruptRead(ctx context.Context, jobID string, decodeErr error) error {
	if _, err := r.db.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = `+stateLiteral(model.StateFailed)+`, failed_at = now(), error_message = $2,
		    current_worker_id = NULL, lease_until = NULL
		WHERE id = $1`, jobID, decodeErr.Error()); err != nil {
		return fmt.Errorf("postgres: corrupt job %s could not be poisoned: %w (decode: %v)", jobID, err, decodeErr)
	}
	return decodeErr
}

// nullIfEmpty converts an empty string to SQL NULL.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
