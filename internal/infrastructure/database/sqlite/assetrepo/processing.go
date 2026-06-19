package assetrepo

import (
	"context"
	"database/sql"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// ProcessingRepository implements asset.ProcessingRepository.

func (r *Repository) StartProcessing(ctx context.Context, assetID, step string) error {
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO asset_processing (asset_id, step, status, started_at, attempt_count, created_at, updated_at)
		VALUES (?, ?, 'running', ?, 1, ?, ?)
		ON CONFLICT(asset_id, step) DO UPDATE SET
			status = 'running',
			started_at = COALESCE(asset_processing.started_at, excluded.started_at),
			attempt_count = asset_processing.attempt_count + 1,
			error_message = '', completed_at = NULL, updated_at = excluded.updated_at
	`, assetID, step, now, now, now)
	return err
}

func (r *Repository) CompleteProcessing(ctx context.Context, assetID, step string) error {
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx, `
		UPDATE asset_processing SET status = 'completed', completed_at = ?, error_message = '', updated_at = ?
		WHERE asset_id = ? AND step = ?
	`, now, now, assetID, step)
	return err
}

func (r *Repository) FailProcessing(ctx context.Context, assetID, step, errMsg string) error {
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx, `
		UPDATE asset_processing SET status = 'failed', completed_at = ?, error_message = ?, updated_at = ?
		WHERE asset_id = ? AND step = ?
	`, now, errMsg, now, assetID, step)
	return err
}

func (r *Repository) TransitionProcessing(ctx context.Context, assetID, step string, from, to asset.ProcessingStatus) error {
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx, `
		UPDATE asset_processing SET status = ?, updated_at = ?,
		    completed_at = CASE WHEN ? IN ('completed', 'failed') THEN ? ELSE completed_at END,
		    error_message = CASE WHEN ? = 'failed' THEN error_message ELSE '' END
		WHERE asset_id = ? AND step = ? AND status = ?
	`, string(to), now, string(to), now, string(to), assetID, step, string(from))
	return err
}

func (r *Repository) GetProcessingRecord(ctx context.Context, assetID, step string) (*asset.ProcessingRecord, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json
		FROM asset_processing WHERE asset_id = ? AND step = ?
	`, assetID, step)
	return scanProcessingRecord(row)
}

func (r *Repository) GetProcessingRecordsByAssetID(ctx context.Context, assetID string) ([]asset.ProcessingRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json
		FROM asset_processing WHERE asset_id = ?
	`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []asset.ProcessingRecord
	for rows.Next() {
		rec, err := scanProcessingRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

func (r *Repository) GetFailedProcessingRecords(ctx context.Context) ([]asset.ProcessingRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json
		FROM asset_processing WHERE status = 'failed'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []asset.ProcessingRecord
	for rows.Next() {
		rec, err := scanProcessingRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

func (r *Repository) DeleteProcessingRecord(ctx context.Context, assetID, step string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM asset_processing WHERE asset_id = ? AND step = ?`, assetID, step)
	return err
}

func (r *Repository) DeleteAllProcessingRecords(ctx context.Context, assetID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM asset_processing WHERE asset_id = ?`, assetID)
	return err
}

func scanProcessingRecord(s scanner) (*asset.ProcessingRecord, error) {
	var rec asset.ProcessingRecord
	var statusStr, startedAtStr, completedAtStr, errStr, metaStr sql.NullString
	err := s.Scan(
		&rec.AssetID, &rec.Step, &statusStr, &startedAtStr, &completedAtStr, &errStr, &rec.AttemptCount, &metaStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rec.Status = asset.ProcessingStatus(statusStr.String)
	if startedAtStr.Valid {
		t := timeutil.ParseRFC3339(startedAtStr.String)
		rec.StartedAt = &t
	}
	if completedAtStr.Valid {
		t := timeutil.ParseRFC3339(completedAtStr.String)
		rec.CompletedAt = &t
	}
	rec.ErrorMessage = errStr.String
	rec.MetadataJSON = metaStr.String
	return &rec, nil
}
