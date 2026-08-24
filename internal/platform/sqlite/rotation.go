// Package storage — rotation.go (June 2026 codex/db-sql-ownership-gate):
//
// Retention helper for the observability DB. The observability DB
// (`api_requests` table) is log-shaped telemetry with an append-only
// workload; without retention it grows unbounded. The retention
// strategy chosen for this codebase (see ARCHITECTURE.md §12) is:
//
//	Disposable + cron retention. Operators schedule
//
// `pipelinegen-admin db rotate` on a daily cron. The command copies
// rows older than cfg.Storage.ObservabilityMaxAgeDays (default 7)
// to a date-stamped backup file under <DataDir>/backups/, then
// DELETEs them from the live DB. This keeps the live observability
// DB small (bounded by retention window + ingest rate) AND keeps the
// full primary backup manifest tiny (observability rows are not
// included in canonical backups — see Check 16's registered list
// which deliberately excludes observability from the backup manifest).
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RotateOptions is the parameter set for RotateObservability.
type RotateOptions struct {
	// MaxAgeDays is the retention cutoff: rows with ts older than
	// MaxAgeDays are offloaded + purged. 0 means rotation is a no-op.
	MaxAgeDays int
	// BackupDir is the directory to write offload files to. The
	// caller resolves it from cfg (default <DataDir>/backups). Files
	// are named `observability-<YYYYMMDD>.db.sqlite`.
	BackupDir string
	// Now overrides time.Now() for testing. nil => wall clock.
	Now *time.Time
}

// RotateResult is the structured outcome of RotateObservability.
type RotateResult struct {
	// BackupDir echoes the directory offload files were written to
	// (mirrors RotateOptions.BackupDir for audit logs).
	BackupDir      string
	Cutoff         time.Time
	OffloadedTo    string
	OffloadedRows  int
	PurgedRows     int
	PreSizeBytes   int64
	PostPurgeSize  int64
	DurationMs     int64
	BytesReclaimed int64
}

// RotateObservability performs the full retention cycle:
//
//  1. Compute cutoff = now - MaxAgeDays
//  2. ATTACH <BackupDir>/observability-<YYYYMMDD>.db.sqlite as offload
//  3. CREATE TABLE offload.api_requests IF NOT EXISTS (id, ts, request_id,
//     method, path, status, duration_ms, client_ip, user_id, bytes_in,
//     bytes_out, user_agent, error)
//  4. INSERT INTO offload.api_requests SELECT * FROM main.api_requests
//     WHERE ts < cutoff
//  5. DETACH offload
//  6. DELETE FROM main.api_requests WHERE ts < cutoff
//  7. VACUUM main.api_requests  (free SQLite page slots within table)
//
// The offload file is a self-contained sqlite DB that an operator can
// open later for forensics; the live DB is now bounded by the
// retention window.
func RotateObservability(ctx context.Context, src *sql.DB, opts RotateOptions) (*RotateResult, error) {
	if opts.MaxAgeDays <= 0 {
		return nil, fmt.Errorf("rotation: MaxAgeDays must be > 0 (got %d)", opts.MaxAgeDays)
	}
	if opts.BackupDir == "" {
		return nil, fmt.Errorf("rotation: BackupDir is required")
	}
	if _, err := os.Stat(opts.BackupDir); os.IsNotExist(err) {
		if err := os.MkdirAll(opts.BackupDir, 0755); err != nil {
			return nil, fmt.Errorf("rotation: mkdir %s: %w", opts.BackupDir, err)
		}
	}

	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now.UTC()
	}
	cutoff := now.AddDate(0, 0, -opts.MaxAgeDays)
	start := time.Now()

	r := &RotateResult{Cutoff: cutoff, BackupDir: opts.BackupDir}

	// Build the offload file path.
	offloadPath := filepath.Join(opts.BackupDir,
		fmt.Sprintf("observability-%s.db.sqlite", now.Format("20060102")))
	r.OffloadedTo = offloadPath

	// Capture pre-size by stat-ing the live db (caller passes main
	// db path via context; for simplicity we use the journal_mode
	// tablespace-size pragma).
	if sizeBefore, err := pageCountBytes(ctx, src); err == nil {
		r.PreSizeBytes = sizeBefore
	}

	// ATTACH offload DB.
	if _, err := src.ExecContext(ctx,
		fmt.Sprintf("ATTACH %q AS offload", offloadPath)); err != nil {
		return r, fmt.Errorf("rotation: ATTACH %s: %w", offloadPath, err)
	}
	defer func() {
		_, _ = src.ExecContext(ctx, "DETACH offload")
	}()

	// Create the api_requests table in the offload if it doesn't exist.
	// (We mirror the live schema verbatim; see migrations/sqlite/008_create_api_requests.sql.)
	const apiRequestsSchema = `
CREATE TABLE IF NOT EXISTS offload.api_requests (
    id          TEXT PRIMARY KEY,
    ts          TEXT    NOT NULL,
    request_id  TEXT    NOT NULL,
    method      TEXT    NOT NULL,
    path        TEXT    NOT NULL,
    status      INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    client_ip   TEXT,
    user_id     TEXT,
    bytes_in    INTEGER,
    bytes_out   INTEGER,
    user_agent  TEXT,
    error       TEXT
)`
	if _, err := src.ExecContext(ctx, apiRequestsSchema); err != nil {
		return r, fmt.Errorf("rotation: create offload.api_requests: %w", err)
	}

	// Offload rows older than cutoff.
	cutoffStr := cutoff.Format(time.RFC3339)
	insRes, err := src.ExecContext(ctx,
		"INSERT INTO offload.api_requests SELECT * FROM main.api_requests WHERE ts < ?",
		cutoffStr,
	)
	if err != nil {
		return r, fmt.Errorf("rotation: INSERT INTO offload: %w", err)
	}
	offloaded, _ := insRes.RowsAffected()
	r.OffloadedRows = int(offloaded)

	// Purge from live DB.
	delRes, err := src.ExecContext(ctx,
		"DELETE FROM main.api_requests WHERE ts < ?",
		cutoffStr,
	)
	if err != nil {
		return r, fmt.Errorf("rotation: DELETE FROM main: %w", err)
	}
	purged, _ := delRes.RowsAffected()
	r.PurgedRows = int(purged)

	// VACUUM the database to reclaim disk space within the live DB.
	// SQLite VACUUM does NOT support table-qualified syntax
	// ("VACUUM main.api_requests"); it operates on the entire database.
	// After the DELETE above, VACUUM rebuilds the db file and reclaims
	// freed pages.
	if _, err := src.ExecContext(ctx, "VACUUM"); err != nil {
		return r, fmt.Errorf("rotation: VACUUM: %w", err)
	}

	// Post-size for delta reporting.
	if sizeAfter, err := pageCountBytes(ctx, src); err == nil {
		r.PostPurgeSize = sizeAfter
		r.BytesReclaimed = r.PreSizeBytes - r.PostPurgeSize
	}

	r.DurationMs = time.Since(start).Milliseconds()

	return r, nil
}

// pageCountBytes reads two sqlite pragmas and multiplies them:
// page_count * page_size. This is the canonical way to measure the
// on-disk footprint of an open sqlite DB.
func pageCountBytes(ctx context.Context, db *sql.DB) (int64, error) {
	var pages int64
	if err := db.QueryRowContext(ctx, "PRAGMA main.page_count").Scan(&pages); err != nil {
		return 0, fmt.Errorf("page_count: %w", err)
	}
	var pageSize int64
	if err := db.QueryRowContext(ctx, "PRAGMA main.page_size").Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("page_size: %w", err)
	}
	return pages * pageSize, nil
}
