package assets

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// MaintenanceRepositorySQLite implements the application-layer
// assets.MaintenanceRepository port for SQLite.
type MaintenanceRepositorySQLite struct {
	db  *sql.DB
	log *zap.Logger
}

// Compile-time assertion that the concrete adapter implements the port.
var _ assets.MaintenanceRepository = (*MaintenanceRepositorySQLite)(nil)

// NewMaintenanceRepository creates a new SQLite maintenance repository.
func NewMaintenanceRepository(db *sql.DB, log *zap.Logger) *MaintenanceRepositorySQLite {
	return &MaintenanceRepositorySQLite{db: db, log: log}
}

// DeleteOldAPIRequests removes api_requests rows older than the configured
// retention window and returns the number of rows deleted.
func (r *MaintenanceRepositorySQLite) DeleteOldAPIRequests(ctx context.Context, retentionDays int) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		"DELETE FROM api_requests WHERE ts < datetime('now', ?)",
		fmt.Sprintf("-%d days", retentionDays))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// WALCheckpoint executes a SQLite WAL checkpoint in the given mode.
// WALCheckpoint executes a SQLite WAL checkpoint in the given mode.
// The caller is responsible for validating mode (PASSIVE, FULL, RESTART, TRUNCATE).
func (r *MaintenanceRepositorySQLite) WALCheckpoint(ctx context.Context, mode string) error {
	_, err := r.db.ExecContext(ctx, fmt.Sprintf("PRAGMA wal_checkpoint(%s)", mode))
	return err
}

// IncrementalVacuum runs PRAGMA incremental_vacuum(pages).
func (r *MaintenanceRepositorySQLite) IncrementalVacuum(ctx context.Context, pages int) error {
	_, err := r.db.ExecContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", pages))
	return err
}

// FullVacuum runs a full VACUUM.
func (r *MaintenanceRepositorySQLite) FullVacuum(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "VACUUM")
	return err
}

// ScanLocalOrphans returns up to batch rows that have a local_path and may
// be missing on disk.
func (r *MaintenanceRepositorySQLite) ScanLocalOrphans(ctx context.Context, batch int) ([]assets.LocalOrphanCandidate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(local_path, '') AS lp,
		       COALESCE(json_extract(metadata_json, '$.orphan_locale'), 0) AS already,
		       COALESCE(json_extract(metadata_json, '$.orphan_detected_at'), '') AS prev
		FROM media_assets
		WHERE deleted_at IS NULL
		  AND COALESCE(local_path, '') != ''
		LIMIT ?`, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []assets.LocalOrphanCandidate
	for rows.Next() {
		var c assets.LocalOrphanCandidate
		if err := rows.Scan(&c.ID, &c.LocalPath, &c.AlreadyOrphan, &c.PrevDetectedAt); err != nil {
			r.log.Warn("failed to scan local orphan candidate", zap.Error(err))
			continue
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return candidates, err
	}
	return candidates, nil
}

// ScanDriveOrphans returns up to batch rows that have a drive_link and may
// point to a trashed or missing Drive file.
func (r *MaintenanceRepositorySQLite) ScanDriveOrphans(ctx context.Context, batch int) ([]assets.DriveOrphanCandidate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(drive_link, '') AS dl,
		       COALESCE(json_extract(metadata_json, '$.orphan_drive'), 0) AS already,
		       COALESCE(json_extract(metadata_json, '$.orphan_detected_at'), '') AS prev
		FROM media_assets
		WHERE deleted_at IS NULL
		  AND COALESCE(drive_link, '') != ''
		LIMIT ?`, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []assets.DriveOrphanCandidate
	for rows.Next() {
		var c assets.DriveOrphanCandidate
		if err := rows.Scan(&c.ID, &c.DriveLink, &c.AlreadyOrphan, &c.PrevDetectedAt); err != nil {
			r.log.Warn("failed to scan drive orphan candidate", zap.Error(err))
			continue
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return candidates, err
	}
	return candidates, nil
}

// MarkLocalOrphan stamps metadata_json with orphan_locale=1.
func (r *MaintenanceRepositorySQLite) MarkLocalOrphan(ctx context.Context, id string, detectedAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets SET metadata_json = json_set(json_set(json_set(COALESCE(metadata_json,'{}'), '$.orphan_locale', 1), '$.orphan_reason', 'local_missing'), '$.orphan_detected_at', ?) WHERE id = ?`,
		detectedAt.Format(time.RFC3339), id)
	return err
}

// MarkDriveOrphan stamps metadata_json with orphan_drive=1.
func (r *MaintenanceRepositorySQLite) MarkDriveOrphan(ctx context.Context, id string, detectedAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets SET metadata_json = json_set(json_set(json_set(COALESCE(metadata_json,'{}'), '$.orphan_drive', 1), '$.orphan_reason', 'drive_trashed'), '$.orphan_detected_at', ?) WHERE id = ?`,
		detectedAt.Format(time.RFC3339), id)
	return err
}
