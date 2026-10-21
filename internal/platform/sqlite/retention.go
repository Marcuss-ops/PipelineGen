// Package storage — retention.go (October 2026 disk-retention):
//
// Age-based retention for the SQLite telemetry/history tables.
//
// rotation.go already bounds the legacy observability `api_requests`
// table. The same unbounded-growth problem exists in three more places:
//
//   - jobs DB:          job_registry_metrics, job_registry_events, job_events
//   - observability DB:  run_observability (+ cascaded run_*_observations)
//   - media DB:          resource_observations, api_requests
//
// ApplyRetentionPlan generalises the rotation idea: DELETE the rows
// older than a cutoff, checkpoint the WAL, then VACUUM so the freed
// pages are returned to the filesystem instead of lingering in the
// freelist. Each op carries its own cutoff/format because not every
// table stores timestamps in RFC3339 (`api_requests.ts` uses SQLite's
// `CURRENT_TIMESTAMP` format).
//
// The command that drives this helper is `admin db retention`
// (cmd/admin/internal/database/db_retention.go), mirroring the existing
// `admin db rotate` contract: operators schedule it on a daily cron.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// DefaultRetentionTimeFormat is the canonical timestamp layout used by the
// operational SQLite tables (job_registry_*, run_observability,
// resource_observations). api_requests uses SQLiteTimestampFormat.
const (
	DefaultRetentionTimeFormat = time.RFC3339Nano
	SQLiteTimestampFormat      = "2006-01-02 15:04:05"
)

var retentionIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// RetentionOp describes one table to prune.
type RetentionOp struct {
	// Table is the table name (validated as a SQL identifier).
	Table string `json:"table"`
	// TimeColumn is the timestamp column the cutoff is compared against.
	TimeColumn string `json:"time_column"`
	// Cutoff is the retention boundary: rows strictly older are deleted.
	Cutoff time.Time `json:"-"`
	// TimeFormat is the layout used to render Cutoff as a string. Empty
	// means DefaultRetentionTimeFormat.
	TimeFormat string `json:"-"`
}

// RetentionPlan is the full set of ops to apply to one database file.
type RetentionPlan struct {
	// Name is a short label used in the JSON report (jobs, observability, media).
	Name string `json:"name"`
	// Path is the database file the plan applies to.
	Path string `json:"path"`
	// Ops are applied in order.
	Ops []RetentionOp `json:"-"`
}

// TableRetentionResult reports the outcome for a single table.
type TableRetentionResult struct {
	Table       string `json:"table"`
	TimeColumn  string `json:"time_column"`
	Cutoff      string `json:"cutoff"`
	DeletedRows int64  `json:"deleted_rows"`
}

// RetentionResult reports the outcome for a single database.
type RetentionResult struct {
	Name           string                 `json:"name"`
	Path           string                 `json:"path"`
	DryRun         bool                   `json:"dry_run"`
	Tables         []TableRetentionResult `json:"tables"`
	PreSizeBytes   int64                  `json:"pre_size_bytes"`
	PostSizeBytes  int64                  `json:"post_size_bytes"`
	BytesReclaimed int64                  `json:"bytes_reclaimed"`
	WALBytes       int64                  `json:"wal_bytes"`
	Vacuumed       bool                   `json:"vacuumed"`
	// CheckpointBusy reports that the final wal_checkpoint(TRUNCATE)
	// could not complete because the runtime kept a read transaction
	// open. The -wal file therefore still holds the pages that VACUUM
	// rewrote; a later checkpoint (or the next retention run) folds
	// them back. BytesReclaimed stays accurate for the main DB file,
	// WALBytes reports what is still pending.
	CheckpointBusy bool  `json:"checkpoint_busy"`
	DurationMs     int64 `json:"duration_ms"`
}

// ApplyRetentionPlan executes plan against db.
//
// dryRun counts the matching rows without deleting anything (and never
// vacuums). vacuum=false skips the final VACUUM, which means the DB file
// keeps its size but the pages land on the freelist and are reused by
// later writes.
//
// The caller must open the DB with foreign keys enabled (OpenWritable
// does): deleting from run_observability relies on ON DELETE CASCADE for
// run_operation_observations/run_stage_observations.
func ApplyRetentionPlan(ctx context.Context, db *sql.DB, plan RetentionPlan, dryRun, vacuum bool) (*RetentionResult, error) {
	if len(plan.Ops) == 0 {
		return nil, fmt.Errorf("retention: plan %q has no ops", plan.Name)
	}
	start := time.Now()
	result := &RetentionResult{Name: plan.Name, Path: plan.Path, DryRun: dryRun}
	result.PreSizeBytes = sqliteFootprint(plan.Path)

	for _, op := range plan.Ops {
		if err := validateRetentionOp(op); err != nil {
			return nil, fmt.Errorf("retention: %s: %w", plan.Name, err)
		}
		cutoff := retentionCutoffLiteral(op)
		table := TableRetentionResult{Table: op.Table, TimeColumn: op.TimeColumn, Cutoff: cutoff}

		if dryRun {
			n, err := countRetentionRows(ctx, db, op, cutoff)
			if err != nil {
				return nil, fmt.Errorf("retention: count %s.%s: %w", plan.Name, op.Table, err)
			}
			table.DeletedRows = n
			result.Tables = append(result.Tables, table)
			continue
		}

		res, err := execRetentionWithRetry(ctx, db,
			fmt.Sprintf(`DELETE FROM "%s" WHERE "%s" < ?`, op.Table, op.TimeColumn),
			cutoff,
		)
		if err != nil {
			return nil, fmt.Errorf("retention: delete %s.%s: %w", plan.Name, op.Table, err)
		}
		deleted, _ := res.RowsAffected()
		table.DeletedRows = deleted
		result.Tables = append(result.Tables, table)
	}

	if !dryRun && vacuum {
		if _, err := checkpointWALWithRetry(ctx, db); err != nil {
			return nil, fmt.Errorf("retention: checkpoint %s: %w", plan.Name, err)
		}
		if _, err := execRetentionWithRetry(ctx, db, "VACUUM"); err != nil {
			return nil, fmt.Errorf("retention: vacuum %s: %w", plan.Name, err)
		}
		busy, err := checkpointWALWithRetry(ctx, db)
		if err != nil {
			return nil, fmt.Errorf("retention: post-vacuum checkpoint %s: %w", plan.Name, err)
		}
		result.CheckpointBusy = busy
		result.Vacuumed = true
	}

	result.PostSizeBytes = sqliteFootprint(plan.Path)
	result.WALBytes = walSize(plan.Path)
	result.BytesReclaimed = result.PreSizeBytes - result.PostSizeBytes
	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

func validateRetentionOp(op RetentionOp) error {
	for _, ident := range []string{op.Table, op.TimeColumn} {
		if !retentionIdentifierPattern.MatchString(ident) {
			return fmt.Errorf("invalid SQL identifier %q", ident)
		}
	}
	if op.Cutoff.IsZero() {
		return fmt.Errorf("cutoff for %s.%s must not be zero", op.Table, op.TimeColumn)
	}
	return nil
}

func retentionCutoffLiteral(op RetentionOp) string {
	format := op.TimeFormat
	if format == "" {
		format = DefaultRetentionTimeFormat
	}
	return op.Cutoff.UTC().Format(format)
}

func countRetentionRows(ctx context.Context, db *sql.DB, op RetentionOp, cutoff string) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM "%s" WHERE "%s" < ?`, op.Table, op.TimeColumn),
		cutoff,
	).Scan(&n)
	return n, err
}

// execRetentionWithRetry retries on SQLITE_BUSY: the runtime keeps these
// databases open, so a maintenance DELETE/VACUUM can lose the lock race.
func execRetentionWithRetry(ctx context.Context, db *sql.DB, query string, args ...any) (sql.Result, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		res, err := db.ExecContext(ctx, query, args...)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !isRetentionBusy(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
		}
	}
	return nil, lastErr
}

func isRetentionBusy(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "sqlite_busy")
}

// checkpointWAL truncates the WAL and reports whether SQLite refused
// because another connection held a read transaction (busy != 0).
func checkpointWAL(ctx context.Context, db *sql.DB) (bool, error) {
	var busy, logFrames, checkpointed int
	err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed)
	return busy != 0, err
}

// checkpointWALWithRetry gives the runtime's short-lived readers a few
// chances to release the DB; VACUUM itself needs the write lock, so a
// busy checkpoint right after it is common on a live service.
func checkpointWALWithRetry(ctx context.Context, db *sql.DB) (bool, error) {
	const attempts = 5
	for attempt := 0; attempt < attempts; attempt++ {
		busy, err := checkpointWAL(ctx, db)
		if err != nil {
			return false, err
		}
		if !busy {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return true, nil
}

// sqliteFootprint is the on-disk footprint of a SQLite database,
// WAL included. A plain stat on the main file misses rows still sitting
// in the -wal file (exactly the state right after a large DELETE).
func sqliteFootprint(path string) int64 {
	return fileBytes(path) + walSize(path)
}

func walSize(path string) int64 { return fileBytes(path + "-wal") }

func fileBytes(path string) int64 {
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return info.Size()
	}
	return 0
}
