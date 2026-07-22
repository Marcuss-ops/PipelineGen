// Package assets — processing SQL queries (Wave C: moved from
// internal/domain/asset/processor.go).
//
// The ProcessingRecord/ProcessingStatus/ProcessingStage types AND
// the ProcessingRepository/Processor interfaces stay in domain
// (canonical orchestration contracts). The SQL receivers + adapter
// factory + adapter struct migrate to this infra file.
package assets

import (
	"context"
	"database/sql"

	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── SQL receivers (migrated from processor.go) ───────────────────────

// StartProcessing transitions a processing step to running.
func (s *AssetStoreSQLite) StartProcessing(ctx context.Context, assetID, step string) error {
	now := timeutil.FormatRFC3339(time.Now())
	_, err := s.db.ExecContext(ctx, `
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
	return err
}

// CompleteProcessing transitions a processing step to completed.
func (s *AssetStoreSQLite) CompleteProcessing(ctx context.Context, assetID, step string) error {
	now := timeutil.FormatRFC3339(time.Now())
	_, err := s.db.ExecContext(ctx, `
		UPDATE asset_processing
		SET status = 'completed', completed_at = ?, error_message = '', updated_at = ?
		WHERE asset_id = ? AND step = ?
	`, now, now, assetID, step)
	return err
}

// FailProcessing transitions a processing step to failed.
func (s *AssetStoreSQLite) FailProcessing(ctx context.Context, assetID, step, errMsg string) error {
	now := timeutil.FormatRFC3339(time.Now())
	_, err := s.db.ExecContext(ctx, `
		UPDATE asset_processing
		SET status = 'failed', completed_at = ?, error_message = ?, updated_at = ?
		WHERE asset_id = ? AND step = ?
	`, now, errMsg, now, assetID, step)
	return err
}

// TransitionProcessing atomically transitions a processing step status.
func (s *AssetStoreSQLite) TransitionProcessing(ctx context.Context, assetID, step string, from, to asset.ProcessingStatus) error {
	now := timeutil.FormatRFC3339(time.Now())
	_, err := s.db.ExecContext(ctx, `
		UPDATE asset_processing
		SET status = ?, updated_at = ?,
		    completed_at = CASE WHEN ? IN ('completed', 'failed') THEN ? ELSE completed_at END,
		    error_message = CASE WHEN ? = 'failed' THEN error_message ELSE '' END
		WHERE asset_id = ? AND step = ? AND status = ?
	`, string(to), now, string(to), now, string(to), assetID, step, string(from))
	return err
}

// GetProcessingRecord retrieves a specific processing record.
func (s *AssetStoreSQLite) GetProcessingRecord(ctx context.Context, assetID, step string) (*asset.ProcessingRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json
		FROM asset_processing
		WHERE asset_id = ? AND step = ?
	`, assetID, step)
	return scanProcessingRecord(row)
}

// GetProcessingRecordsByAssetID retrieves all processing records for
// an asset.
func (s *AssetStoreSQLite) GetProcessingRecordsByAssetID(ctx context.Context, assetID string) ([]asset.ProcessingRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json
		FROM asset_processing
		WHERE asset_id = ?
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

// GetFailedProcessingRecords retrieves all failed processing records.
func (s *AssetStoreSQLite) GetFailedProcessingRecords(ctx context.Context) ([]asset.ProcessingRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT asset_id, step, status, started_at, completed_at, error_message, attempt_count, metadata_json
		FROM asset_processing
		WHERE status = 'failed'
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

// DeleteProcessingRecord removes a processing record.
func (s *AssetStoreSQLite) DeleteProcessingRecord(ctx context.Context, assetID, step string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM asset_processing WHERE asset_id = ? AND step = ?
	`, assetID, step)
	return err
}

// DeleteAllProcessingRecords removes all processing records for an
// asset.
func (s *AssetStoreSQLite) DeleteAllProcessingRecords(ctx context.Context, assetID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM asset_processing WHERE asset_id = ?`, assetID)
	return err
}

// scanProcessingRecord scans a single asset_processing row.
func scanProcessingRecord(scanner interface{ Scan(dest ...any) error }) (*asset.ProcessingRecord, error) {
	var rec asset.ProcessingRecord
	var statusStr, startedAtStr, completedAtStr, errStr, metaStr sql.NullString
	err := scanner.Scan(
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

// ── ProcessingRepository adapter (canonical Wave C surface) ──────────

type processingRepositoryAdapter struct {
	store *AssetStoreSQLite
}

func (a *processingRepositoryAdapter) Start(ctx context.Context, assetID, step string) error {
	return a.store.StartProcessing(ctx, assetID, step)
}

func (a *processingRepositoryAdapter) Complete(ctx context.Context, assetID, step string) error {
	return a.store.CompleteProcessing(ctx, assetID, step)
}

func (a *processingRepositoryAdapter) Fail(ctx context.Context, assetID, step, errMsg string) error {
	return a.store.FailProcessing(ctx, assetID, step, errMsg)
}

func (a *processingRepositoryAdapter) Transition(ctx context.Context, assetID, step string, from, to asset.ProcessingStatus) error {
	return a.store.TransitionProcessing(ctx, assetID, step, from, to)
}

func (a *processingRepositoryAdapter) Get(ctx context.Context, assetID, step string) (*asset.ProcessingRecord, error) {
	return a.store.GetProcessingRecord(ctx, assetID, step)
}

func (a *processingRepositoryAdapter) GetByAssetID(ctx context.Context, assetID string) ([]asset.ProcessingRecord, error) {
	return a.store.GetProcessingRecordsByAssetID(ctx, assetID)
}

func (a *processingRepositoryAdapter) GetFailed(ctx context.Context) ([]asset.ProcessingRecord, error) {
	return a.store.GetFailedProcessingRecords(ctx)
}

func (a *processingRepositoryAdapter) Delete(ctx context.Context, assetID, step string) error {
	return a.store.DeleteProcessingRecord(ctx, assetID, step)
}

func (a *processingRepositoryAdapter) DeleteAll(ctx context.Context, assetID string) error {
	return a.store.DeleteAllProcessingRecords(ctx, assetID)
}

// ProcessingRepository returns the ProcessingRepository adapter for
// the LOCAL AssetStoreSQLite.
func (s *AssetStoreSQLite) ProcessingRepository() asset.ProcessingRepository {
	return &processingRepositoryAdapter{store: s}
}
