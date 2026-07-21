// Package stockbatches — SQLite-backed stock batch / group / artifact store.
//
// Implements the stockpipeline.StockBatchRepository port defined in
// internal/application/assets/providers/stock/stockpipeline/batch_repository.go.
// The adapter is thin: it owns SQL and row mapping, no business logic.
//
// Table DDL: migrations/sqlite/162_stock_batches.sql
package stockbatches

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
)

// Repository is the SQLite-backed stock batch state store.
type Repository struct {
	db *sql.DB
}

// Compile-time structural conformance pin.
var _ stockpipeline.StockBatchRepository = (*Repository)(nil)

// NewRepository constructs the repository. Panics on nil db (fail-fast).
func NewRepository(db *sql.DB) *Repository {
	if db == nil {
		panic("stockbatches.NewRepository: nil *sql.DB")
	}
	return &Repository{db: db}
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, _ = time.Parse("2006-01-02 15:04:05", s)
	}
	return t
}

func scanTime(dest *time.Time, src string) {
	*dest = parseTime(src)
}

// CreateBatch creates a stock_batches row if it does not already exist.
// Existing rows are left untouched so that retry/resume never loses
// accumulated state (status, attempts, last_error, etc.).
func (r *Repository) CreateBatch(ctx context.Context, batch *stockpipeline.StockBatch) error {
	const q = `INSERT OR IGNORE INTO stock_batches
		(id, fingerprint, source_url, source_cache_key, root_folder_id, root_folder_name,
		 status, expected_groups, expected_clips, verified_clips, policy_version, last_error,
		 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`
	_, err := r.db.ExecContext(ctx, q,
		batch.ID, batch.Fingerprint, batch.SourceURL, batch.SourceCacheKey,
		batch.RootFolderID, batch.RootFolderName, string(batch.Status),
		batch.ExpectedGroups, batch.ExpectedClips, batch.VerifiedClips,
		batch.PolicyVersion, batch.LastError,
	)
	if err != nil {
		return fmt.Errorf("stockbatches.CreateBatch: %w", err)
	}
	return nil
}

// GetBatch returns a stock batch by id.
func (r *Repository) GetBatch(ctx context.Context, id string) (*stockpipeline.StockBatch, error) {
	const q = `SELECT id, fingerprint, source_url, source_cache_key, root_folder_id, root_folder_name,
		status, expected_groups, expected_clips, verified_clips, policy_version,
		created_at, updated_at, last_error
		FROM stock_batches WHERE id = ?`
	row := r.db.QueryRowContext(ctx, q, id)
	b := &stockpipeline.StockBatch{}
	var status, createdAt, updatedAt string
	err := row.Scan(&b.ID, &b.Fingerprint, &b.SourceURL, &b.SourceCacheKey,
		&b.RootFolderID, &b.RootFolderName, &status, &b.ExpectedGroups,
		&b.ExpectedClips, &b.VerifiedClips, &b.PolicyVersion, &createdAt,
		&updatedAt, &b.LastError,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stockbatches.GetBatch: %w", err)
	}
	b.Status = stockpipeline.BatchState(status)
	scanTime(&b.CreatedAt, createdAt)
	scanTime(&b.UpdatedAt, updatedAt)
	return b, nil
}

// UpdateBatchStatus updates the status, last_error and updated_at of a batch.
func (r *Repository) UpdateBatchStatus(ctx context.Context, id string, status stockpipeline.BatchState, lastError string) error {
	const q = `UPDATE stock_batches SET status = ?, last_error = ?, updated_at = datetime('now') WHERE id = ?`
	_, err := r.db.ExecContext(ctx, q, string(status), lastError, id)
	if err != nil {
		return fmt.Errorf("stockbatches.UpdateBatchStatus: %w", err)
	}
	return nil
}

// CreateGroup creates a stock_batch_groups row if it does not already exist.
// Existing rows are left untouched so that retry/resume never loses
// accumulated state.
func (r *Repository) CreateGroup(ctx context.Context, group *stockpipeline.StockBatchGroup) error {
	const q = `INSERT OR IGNORE INTO stock_batch_groups
		(id, batch_id, group_key, title, folder_name, drive_folder_id, start_sec, end_sec,
		 expected_clips, verified_clips, status, child_job_id, attempts, last_error,
		 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`
	_, err := r.db.ExecContext(ctx, q,
		group.ID, group.BatchID, group.GroupKey, group.Title, group.FolderName,
		group.DriveFolderID, group.StartSec, group.EndSec, group.ExpectedClips,
		group.VerifiedClips, string(group.Status), group.ChildJobID, group.Attempts,
		group.LastError,
	)
	if err != nil {
		return fmt.Errorf("stockbatches.CreateGroup: %w", err)
	}
	return nil
}

// GetGroup returns a stock batch group by id.
func (r *Repository) GetGroup(ctx context.Context, id string) (*stockpipeline.StockBatchGroup, error) {
	const q = `SELECT id, batch_id, group_key, title, folder_name, drive_folder_id,
		start_sec, end_sec, expected_clips, verified_clips, status, child_job_id, attempts,
		created_at, updated_at, last_error
		FROM stock_batch_groups WHERE id = ?`
	row := r.db.QueryRowContext(ctx, q, id)
	g := &stockpipeline.StockBatchGroup{}
	var status, createdAt, updatedAt string
	err := row.Scan(&g.ID, &g.BatchID, &g.GroupKey, &g.Title, &g.FolderName,
		&g.DriveFolderID, &g.StartSec, &g.EndSec, &g.ExpectedClips, &g.VerifiedClips,
		&status, &g.ChildJobID, &g.Attempts, &createdAt, &updatedAt, &g.LastError,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stockbatches.GetGroup: %w", err)
	}
	g.Status = stockpipeline.GroupState(status)
	scanTime(&g.CreatedAt, createdAt)
	scanTime(&g.UpdatedAt, updatedAt)
	return g, nil
}

// UpdateGroupStatus updates the status, last_error and updated_at of a group.
func (r *Repository) UpdateGroupStatus(ctx context.Context, id string, status stockpipeline.GroupState, lastError string) error {
	const q = `UPDATE stock_batch_groups SET status = ?, last_error = ?, updated_at = datetime('now') WHERE id = ?`
	_, err := r.db.ExecContext(ctx, q, string(status), lastError, id)
	if err != nil {
		return fmt.Errorf("stockbatches.UpdateGroupStatus: %w", err)
	}
	return nil
}

// ListGroups returns all groups belonging to a batch, ordered by group_key.
func (r *Repository) ListGroups(ctx context.Context, batchID string) ([]stockpipeline.StockBatchGroup, error) {
	const q = `SELECT id, batch_id, group_key, title, folder_name, drive_folder_id,
		start_sec, end_sec, expected_clips, verified_clips, status, child_job_id, attempts,
		created_at, updated_at, last_error
		FROM stock_batch_groups WHERE batch_id = ? ORDER BY group_key`
	rows, err := r.db.QueryContext(ctx, q, batchID)
	if err != nil {
		return nil, fmt.Errorf("stockbatches.ListGroups: %w", err)
	}
	defer rows.Close()

	var out []stockpipeline.StockBatchGroup
	for rows.Next() {
		var g stockpipeline.StockBatchGroup
		var status, createdAt, updatedAt string
		err := rows.Scan(&g.ID, &g.BatchID, &g.GroupKey, &g.Title, &g.FolderName,
			&g.DriveFolderID, &g.StartSec, &g.EndSec, &g.ExpectedClips, &g.VerifiedClips,
			&status, &g.ChildJobID, &g.Attempts, &createdAt, &updatedAt, &g.LastError,
		)
		if err != nil {
			return nil, fmt.Errorf("stockbatches.ListGroups: scan: %w", err)
		}
		g.Status = stockpipeline.GroupState(status)
		scanTime(&g.CreatedAt, createdAt)
		scanTime(&g.UpdatedAt, updatedAt)
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stockbatches.ListGroups: rows: %w", err)
	}
	return out, nil
}

// CreateArtifact creates a stock_artifacts row if it does not already exist.
// Existing rows are left untouched so that retry/resume never loses
// accumulated state (status, attempts, local_path, sha256, etc.).
// We use ON CONFLICT(id) DO NOTHING so that a missing parent batch/group
// still raises a foreign-key error instead of being silently ignored.
func (r *Repository) CreateArtifact(ctx context.Context, artifact *stockpipeline.StockArtifact) error {
	const q = `INSERT INTO stock_artifacts
		(id, batch_id, group_id, ordinal, artifact_key, source_url, start_sec, end_sec,
		 expected_duration_ms, actual_duration_ms, local_path, sha256, status,
		 drive_file_id, drive_folder_id, drive_link, attempts, last_error,
		 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		ON CONFLICT(id) DO NOTHING`
	_, err := r.db.ExecContext(ctx, q,
		artifact.ID, artifact.BatchID, artifact.GroupID, artifact.Ordinal, artifact.ArtifactKey,
		artifact.SourceURL, artifact.StartSec, artifact.EndSec, artifact.ExpectedDurationMs,
		artifact.ActualDurationMs, artifact.LocalPath, artifact.SHA256, string(artifact.Status),
		artifact.DriveFileID, artifact.DriveFolderID, artifact.DriveLink, artifact.Attempts,
		artifact.LastError,
	)
	if err != nil {
		return fmt.Errorf("stockbatches.CreateArtifact: %w", err)
	}
	return nil
}

// GetArtifact returns a stock artifact by id.
func (r *Repository) GetArtifact(ctx context.Context, id string) (*stockpipeline.StockArtifact, error) {
	const q = `SELECT id, batch_id, group_id, ordinal, artifact_key, source_url, start_sec, end_sec,
		expected_duration_ms, actual_duration_ms, local_path, sha256, status,
		drive_file_id, drive_folder_id, drive_link, attempts, created_at, updated_at, last_error
		FROM stock_artifacts WHERE id = ?`
	row := r.db.QueryRowContext(ctx, q, id)
	a := &stockpipeline.StockArtifact{}
	var status, createdAt, updatedAt string
	err := row.Scan(&a.ID, &a.BatchID, &a.GroupID, &a.Ordinal, &a.ArtifactKey,
		&a.SourceURL, &a.StartSec, &a.EndSec, &a.ExpectedDurationMs, &a.ActualDurationMs,
		&a.LocalPath, &a.SHA256, &status, &a.DriveFileID, &a.DriveFolderID, &a.DriveLink,
		&a.Attempts, &createdAt, &updatedAt, &a.LastError,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stockbatches.GetArtifact: %w", err)
	}
	a.Status = stockpipeline.ArtifactState(status)
	scanTime(&a.CreatedAt, createdAt)
	scanTime(&a.UpdatedAt, updatedAt)
	return a, nil
}

func checkAffected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("stockbatches: state transition failed: no matching row or race condition")
	}
	return nil
}

// MarkArtifactExtracting transitions an artifact from PLANNED/RETRY_WAIT to
// EXTRACTING and bumps attempts. It is race-safe: if another worker already
// took the artifact the update affects zero rows and returns an error.
func (r *Repository) MarkArtifactExtracting(ctx context.Context, id string) error {
	const q = `UPDATE stock_artifacts
		SET status = 'EXTRACTING', attempts = attempts + 1, updated_at = datetime('now')
		WHERE id = ? AND status IN ('PLANNED','RETRY_WAIT')`
	return checkAffected(r.db.ExecContext(ctx, q, id))
}

// MarkArtifactExtracted transitions an artifact from EXTRACTING to EXTRACTED
// and persists the produced file path, SHA-256 and actual duration.
func (r *Repository) MarkArtifactExtracted(ctx context.Context, id, localPath, sha256 string, actualDurationMs int) error {
	const q = `UPDATE stock_artifacts
		SET status = 'EXTRACTED', local_path = ?, sha256 = ?, actual_duration_ms = ?,
		    updated_at = datetime('now'), last_error = ''
		WHERE id = ? AND status IN ('EXTRACTING', 'EXTRACTED')`
	return checkAffected(r.db.ExecContext(ctx, q, localPath, sha256, actualDurationMs, id))
}

// MarkArtifactPublished transitions an artifact from EXTRACTED to PUBLISHED
// and persists the Drive file id, folder id and web link.
func (r *Repository) MarkArtifactPublished(ctx context.Context, id, driveFileID, driveFolderID, driveLink string) error {
	const q = `UPDATE stock_artifacts
		SET status = 'PUBLISHED', drive_file_id = ?, drive_folder_id = ?, drive_link = ?,
		    updated_at = datetime('now'), last_error = ''
		WHERE id = ? AND status IN ('EXTRACTED', 'PUBLISHED')`
	return checkAffected(r.db.ExecContext(ctx, q, driveFileID, driveFolderID, driveLink, id))
}

// MarkArtifactVerified transitions an artifact from PUBLISHED to VERIFIED.
func (r *Repository) MarkArtifactVerified(ctx context.Context, id string) error {
	const q = `UPDATE stock_artifacts
		SET status = 'VERIFIED', updated_at = datetime('now'), last_error = ''
		WHERE id = ? AND status IN ('PUBLISHED', 'VERIFIED')`
	return checkAffected(r.db.ExecContext(ctx, q, id))
}

// MarkGroupSucceeded transitions a group to SUCCEEDED and records the
// number of verified clips.
func (r *Repository) MarkGroupSucceeded(ctx context.Context, id string, verifiedClips int) error {
	const q = `UPDATE stock_batch_groups
		SET status = 'SUCCEEDED', verified_clips = ?, updated_at = datetime('now'), last_error = ''
		WHERE id = ?`
	_, err := r.db.ExecContext(ctx, q, verifiedClips, id)
	if err != nil {
		return fmt.Errorf("stockbatches.MarkGroupSucceeded: %w", err)
	}
	return nil
}

// MarkBatchSucceeded transitions a batch to SUCCEEDED and records the
// number of verified clips.
func (r *Repository) MarkBatchSucceeded(ctx context.Context, id string, verifiedClips int) error {
	const q = `UPDATE stock_batches
		SET status = 'SUCCEEDED', verified_clips = ?, updated_at = datetime('now'), last_error = ''
		WHERE id = ?`
	_, err := r.db.ExecContext(ctx, q, verifiedClips, id)
	if err != nil {
		return fmt.Errorf("stockbatches.MarkBatchSucceeded: %w", err)
	}
	return nil
}

// MarkArtifactFailed transitions an artifact from EXTRACTING to the given
// error state (RETRY_WAIT, FAILED_PERMANENT or QUARANTINED) and records the
// last error.
func (r *Repository) MarkArtifactFailed(ctx context.Context, id string, status stockpipeline.ArtifactState, lastError string) error {
	const q = `UPDATE stock_artifacts
		SET status = ?, last_error = ?, updated_at = datetime('now')
		WHERE id = ? AND status = 'EXTRACTING'`
	return checkAffected(r.db.ExecContext(ctx, q, string(status), lastError, id))
}

// FindIncompleteArtifacts returns artifacts of a group that are not yet
// terminal (VERIFIED / FAILED_PERMANENT / QUARANTINED), ordered by ordinal.
// Only artifacts with attempts < maxAttempts are returned.
func (r *Repository) FindIncompleteArtifacts(ctx context.Context, groupID string, maxAttempts int) ([]stockpipeline.StockArtifact, error) {
	const q = `SELECT id, batch_id, group_id, ordinal, artifact_key, source_url, start_sec, end_sec,
		expected_duration_ms, actual_duration_ms, local_path, sha256, status,
		drive_file_id, drive_folder_id, drive_link, attempts, created_at, updated_at, last_error
		FROM stock_artifacts
		WHERE group_id = ? AND status NOT IN ('VERIFIED','FAILED_PERMANENT','QUARANTINED')
		  AND attempts < ?
		ORDER BY ordinal`
	rows, err := r.db.QueryContext(ctx, q, groupID, maxAttempts)
	if err != nil {
		return nil, fmt.Errorf("stockbatches.FindIncompleteArtifacts: %w", err)
	}
	defer rows.Close()

	var out []stockpipeline.StockArtifact
	for rows.Next() {
		var a stockpipeline.StockArtifact
		var status, createdAt, updatedAt string
		err := rows.Scan(&a.ID, &a.BatchID, &a.GroupID, &a.Ordinal, &a.ArtifactKey,
			&a.SourceURL, &a.StartSec, &a.EndSec, &a.ExpectedDurationMs, &a.ActualDurationMs,
			&a.LocalPath, &a.SHA256, &status, &a.DriveFileID, &a.DriveFolderID, &a.DriveLink,
			&a.Attempts, &createdAt, &updatedAt, &a.LastError,
		)
		if err != nil {
			return nil, fmt.Errorf("stockbatches.FindIncompleteArtifacts: scan: %w", err)
		}
		a.Status = stockpipeline.ArtifactState(status)
		scanTime(&a.CreatedAt, createdAt)
		scanTime(&a.UpdatedAt, updatedAt)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stockbatches.FindIncompleteArtifacts: rows: %w", err)
	}
	return out, nil
}
