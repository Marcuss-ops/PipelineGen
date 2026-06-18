// Package assetprocessing provides the read/write layer for the
// asset_processing table, which tracks the lifecycle of each processing
// step (download, normalize, transcription, embedding, etc.) for a media
// asset. This table answers "what processing has been done on this asset,
// and what failed?" without conflating processing state with the asset's
// own lifecycle state.
package assetprocessing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"velox/go-master/pkg/timeutil"
)

// ProcessingStatus represents the status of a processing step.
type ProcessingStatus string

const (
	StatusPending   ProcessingStatus = "pending"
	StatusRunning   ProcessingStatus = "running"
	StatusCompleted ProcessingStatus = "completed"
	StatusFailed    ProcessingStatus = "failed"
)

// ProcessingRecord represents a single asset_processing row.
type ProcessingRecord struct {
	AssetID       string
	Step          string
	Status        ProcessingStatus
	StartedAt     *time.Time
	CompletedAt   *time.Time
	ErrorMessage  string
	AttemptCount  int
	MetadataJSON  string
}

// Repository wraps SQL access to the asset_processing table.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a Repository backed by db.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Upsert inserts or updates a processing record. On conflict (asset_id, step),
// non-status fields are updated but status remains unchanged unless the caller
// explicitly uses Transition, Start, Complete, or Fail.
func (r *Repository) Upsert(ctx context.Context, record ProcessingRecord) error {
	if record.MetadataJSON != "" && record.MetadataJSON != "{}" && !json.Valid([]byte(record.MetadataJSON)) {
		return fmt.Errorf("assetprocessing.Upsert(%s, %s): metadata_json is not valid JSON", record.AssetID, record.Step)
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO asset_processing
			(asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_id, step) DO UPDATE SET
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at
	`, record.AssetID, record.Step, string(record.Status),
		nullTime(record.StartedAt), nullTime(record.CompletedAt),
		record.ErrorMessage, record.AttemptCount, record.MetadataJSON,
		now, now)
	if err != nil {
		return fmt.Errorf("assetprocessing.Upsert(%s, %s): %w", record.AssetID, record.Step, err)
	}
	return nil
}

// Start transitions a processing step to running. Creates the record if
// it doesn't exist (idempotent start). The first start sets attempt_count=1;
// subsequent starts increment it.
func (r *Repository) Start(ctx context.Context, assetID, step string) error {
	now := timeutil.FormatRFC3339(time.Now())
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO asset_processing (asset_id, step, status, started_at, attempt_count, created_at, updated_at)
		VALUES (?, ?, 'running', ?, 1, ?, ?)
		ON CONFLICT(asset_id, step) DO UPDATE SET
			status = 'running',
			started_at = COALESCE(asset_processing.started_at, excluded.started_at),
			attempt_count = asset_processing.attempt_count + 1,
			error_message = '',
			completed_at = NULL,
			updated_at = excluded.updated_at
	`, assetID, step, now, now, now)
	if err != nil {
		return fmt.Errorf("assetprocessing.Start(%s, %s): %w", assetID, step, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("assetprocessing.Start(%s, %s): no rows affected", assetID, step)
	}
	return nil
}

// Transition atomically transitions a processing step from one status to
// another. Returns error if no row matches (wrong expected status).
//
// Allowed transitions:
//
//	pending   → running
//	running   → completed, failed
//	failed    → running (retry)
//	completed → running (reprocessing)
func (r *Repository) Transition(ctx context.Context, assetID, step string, from, to ProcessingStatus) error {
	if err := validateTransition(from, to); err != nil {
		return fmt.Errorf("assetprocessing.Transition(%s, %s): %w", assetID, step, err)
	}
	now := timeutil.FormatRFC3339(time.Now())
	result, err := r.db.ExecContext(ctx, `
		UPDATE asset_processing
		SET status = ?, updated_at = ?,
		    completed_at = CASE WHEN ? IN ('completed', 'failed') THEN ? ELSE completed_at END,
		    error_message = CASE WHEN ? = 'failed' THEN ? ELSE error_message END
		WHERE asset_id = ? AND step = ? AND status = ?
	`, string(to), now, string(to), now, string(to), "", assetID, step, string(from))
	if err != nil {
		return fmt.Errorf("assetprocessing.Transition(%s, %s, %s→%s): %w", assetID, step, from, to, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		existing, getErr := r.Get(ctx, assetID, step)
		if getErr != nil || existing == nil {
			return fmt.Errorf("assetprocessing.Transition(%s, %s): record not found", assetID, step)
		}
		return fmt.Errorf("assetprocessing.Transition(%s, %s): invalid transition %s→%s (current=%s)",
			assetID, step, from, to, existing.Status)
	}
	return nil
}

// validateTransition checks whether the state transition is valid per the
// canonical state machine.
func validateTransition(from, to ProcessingStatus) error {
	switch from {
	case StatusPending:
		if to == StatusRunning {
			return nil
		}
	case StatusRunning:
		if to == StatusCompleted || to == StatusFailed {
			return nil
		}
	case StatusFailed:
		if to == StatusRunning {
			return nil
		}
	case StatusCompleted:
		if to == StatusRunning {
			return nil
		}
	}
	return fmt.Errorf("invalid transition: %s → %s", from, to)
}

// Complete marks a processing step as completed. Only succeeds if the
// current status is 'running'. Returns error if no row matches.
func (r *Repository) Complete(ctx context.Context, assetID, step string) error {
	now := timeutil.FormatRFC3339(time.Now())
	result, err := r.db.ExecContext(ctx, `
		UPDATE asset_processing
		SET status = 'completed', completed_at = ?, updated_at = ?
		WHERE asset_id = ? AND step = ? AND status = 'running'
	`, now, now, assetID, step)
	if err != nil {
		return fmt.Errorf("assetprocessing.Complete(%s, %s): %w", assetID, step, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("assetprocessing.Complete(%s, %s): step is not in 'running' state or does not exist", assetID, step)
	}
	return nil
}

// Fail marks a running processing step as failed with an error message.
// Only succeeds if the current status is 'running'. Returns error if no row matches.
func (r *Repository) Fail(ctx context.Context, assetID, step, errMsg string) error {
	now := timeutil.FormatRFC3339(time.Now())
	result, err := r.db.ExecContext(ctx, `
		UPDATE asset_processing
		SET status = 'failed', completed_at = ?, error_message = ?, updated_at = ?
		WHERE asset_id = ? AND step = ? AND status = 'running'
	`, now, errMsg, now, assetID, step)
	if err != nil {
		return fmt.Errorf("assetprocessing.Fail(%s, %s): %w", assetID, step, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("assetprocessing.Fail(%s, %s): step is not in 'running' state or does not exist", assetID, step)
	}
	return nil
}

// Get returns a single processing record for an asset + step.
func (r *Repository) Get(ctx context.Context, assetID, step string) (*ProcessingRecord, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json
		FROM asset_processing
		WHERE asset_id = ? AND step = ?
	`, assetID, step)
	return scanProcessing(row)
}

// GetByAssetID returns all processing records for an asset.
func (r *Repository) GetByAssetID(ctx context.Context, assetID string) ([]ProcessingRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json
		FROM asset_processing
		WHERE asset_id = ?
		ORDER BY step
	`, assetID)
	if err != nil {
		return nil, fmt.Errorf("assetprocessing.GetByAssetID(%s): %w", assetID, err)
	}
	defer rows.Close()
	return scanProcessings(rows)
}

// GetFailed returns all failed processing records across all assets.
func (r *Repository) GetFailed(ctx context.Context) ([]ProcessingRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json
		FROM asset_processing
		WHERE status = 'failed'
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("assetprocessing.GetFailed: %w", err)
	}
	defer rows.Close()
	return scanProcessings(rows)
}

// Delete removes a single processing record.
func (r *Repository) Delete(ctx context.Context, assetID, step string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM asset_processing WHERE asset_id = ? AND step = ?
	`, assetID, step)
	if err != nil {
		return fmt.Errorf("assetprocessing.Delete(%s, %s): %w", assetID, step, err)
	}
	return nil
}

// DeleteAll removes all processing records for an asset.
func (r *Repository) DeleteAll(ctx context.Context, assetID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM asset_processing WHERE asset_id = ?`, assetID)
	if err != nil {
		return fmt.Errorf("assetprocessing.DeleteAll(%s): %w", assetID, err)
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────

func scanProcessing(s interface{ Scan(dest ...any) error }) (*ProcessingRecord, error) {
	r := &ProcessingRecord{}
	var startedAtStr, completedAtStr sql.NullString
	err := s.Scan(&r.AssetID, &r.Step, (*string)(&r.Status),
		&startedAtStr, &completedAtStr, &r.ErrorMessage, &r.AttemptCount, &r.MetadataJSON)
	if err != nil {
		return nil, err
	}
	if startedAtStr.Valid {
		t := timeutil.ParseRFC3339(startedAtStr.String)
		if !t.IsZero() {
			r.StartedAt = &t
		}
	}
	if completedAtStr.Valid {
		t := timeutil.ParseRFC3339(completedAtStr.String)
		if !t.IsZero() {
			r.CompletedAt = &t
		}
	}
	return r, nil
}

func scanProcessings(rows *sql.Rows) ([]ProcessingRecord, error) {
	var out []ProcessingRecord
	for rows.Next() {
		r, err := scanProcessing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func nullTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := timeutil.FormatRFC3339(*t)
	return &s
}
