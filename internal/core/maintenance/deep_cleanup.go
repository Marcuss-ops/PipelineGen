package maintenance

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// validateCheckpointMode ensures the env-supplied SQLite WAL checkpoint mode
// is one of the four documented values (PASSIVE, FULL, RESTART, TRUNCATE).
// Returns "PASSIVE" as the safe default for any unrecognised or empty input
// so that untrusted env-var values can never reach SQLite.
func validateCheckpointMode(mode string) string {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case "PASSIVE", "FULL", "RESTART", "TRUNCATE":
		return strings.ToUpper(mode)
	default:
		return "PASSIVE"
	}
}

// deepCleanupSuppressWindow prevents re-running the same orphan detection on
// rows that were already stamped within this window. Keeps the metadata
// churn (and the log noise) bounded across repeated maintenance ticks.
const deepCleanupSuppressWindow = 7 * 24 * time.Hour

// deepCleanupDriveBatchCap is the per-pass cap on the Drive row scan.
// The Drive Files.Get endpoint is rate-limited at ~10 req/s per user, so we
// keep each maintenance tick under that ceiling.
const deepCleanupDriveBatchCap = 200

// deepCleanupCounters is a typed counter struct used inside runDeepCleanup
// so the result map can be populated without panicking on type assertions
// every time we increment a field.
type deepCleanupCounters struct {
	LocalDetected int
	LocalMarked   int
	DriveDetected int
	DriveMarked   int
	SkippedRecent int
}

func (c *deepCleanupCounters) ToMap() map[string]any {
	return map[string]any{
		"orphan_locale_detected": c.LocalDetected,
		"orphan_locale_marked":   c.LocalMarked,
		"orphan_drive_detected":  c.DriveDetected,
		"orphan_drive_marked":    c.DriveMarked,
		"orphan_skipped_recent":  c.SkippedRecent,
	}
}

// suppressionReason describes why orphanSuppressed wants to skip a row so
// callers can log a context-rich trace.
type suppressionReason int

const (
	suppressNone      suppressionReason = iota
	suppressMarkerSet                   // orphan_locale=1 / orphan_drive=1 still on the row
	suppressRecent                      // orphan_detected_at within the suppression window
)

// String renders the reason as a stable token so zap fields show
// suppression_reason=marker_set / recent instead of an opaque int.
func (r suppressionReason) String() string {
	switch r {
	case suppressMarkerSet:
		return "marker_set"
	case suppressRecent:
		return "recent"
	default:
		return "none"
	}
}

// orphanSuppressed returns true plus the reason if metadata_json indicates
// we recently stamped this row as orphan and should not rescan it yet.
func orphanSuppressed(alreadyStr, prev string, now time.Time) (bool, suppressionReason) {
	if alreadyStr == "1" {
		return true, suppressMarkerSet
	}
	if prev == "" {
		return false, suppressNone
	}
	t, perr := time.Parse(time.RFC3339, prev)
	if perr != nil {
		return false, suppressNone
	}
	if now.Sub(t) < deepCleanupSuppressWindow {
		return true, suppressRecent
	}
	return false, suppressNone
}

// runDeepCleanup scans media_assets for two orphan categories:
//
//  1. Local orphan: row has a local_path that no longer exists on disk.
//  2. Drive orphan: row has a drive_link whose target Drive file is missing
//     or in the trash (per DriveFileChecker.FileIsNotTrashed).
//
// When dryRun is true the function only counts and logs. Otherwise it stamps
// metadata_json with orphan_locale / orphan_drive so the indexer + dedup
// sweepers can react without an immediate row delete. Hard deletion is
// intentionally left to media.DeletionService.CleanupOrphanFiles.
func (s *Service) runDeepCleanup(ctx context.Context, dryRun bool) (map[string]any, error) {
	c := &deepCleanupCounters{}
	if len(s.dbs) == 0 {
		return c.ToMap(), nil
	}
	batch := s.deepCleanupBatch
	if batch <= 0 {
		batch = 1000
	}
	driveBatch := batch
	if driveBatch > deepCleanupDriveBatchCap {
		driveBatch = deepCleanupDriveBatchCap
	}
	now := time.Now().UTC()

	for dbIdx, db := range s.dbs {
		if db == nil {
			continue
		}
		s.scanDBOrphans(ctx, dbIdx, db, batch, driveBatch, dryRun, now, c)
	}
	return c.ToMap(), nil
}

// scanDBOrphans runs both the local and Drive passes for a single database.
// Embedding defer rows.Close() inside this method avoids the defer-in-loop
// footgun that runDeepCleanup would have if it owned the rows handle.
func (s *Service) scanDBOrphans(
	ctx context.Context,
	dbIdx int,
	db *sql.DB,
	batch, driveBatch int,
	dryRun bool,
	now time.Time,
	c *deepCleanupCounters,
) {
	// ── Pass 1: local file existence ──────────────────────────────────
	// local_path is a canonical column (migration 059). orphan_locale /
	// orphan_detected_at stay in JSON because they are deep_cleanup's
	// state-machine markers, not columns on the schema.
	localRows, err := db.QueryContext(ctx, `
		SELECT id, COALESCE(local_path, '') AS lp,
		       COALESCE(json_extract(metadata_json, '$.orphan_locale'), 0) AS already,
		       COALESCE(json_extract(metadata_json, '$.orphan_detected_at'), '') AS prev
		FROM media_assets
		WHERE deleted_at IS NULL
		  AND COALESCE(local_path, '') != ''
		LIMIT ?`, batch)
	if err != nil {
		s.log.Warn("deep_cleanup local query failed", zap.Int("db_index", dbIdx), zap.Error(err))
		return
	}
	defer localRows.Close()
	s.scanLocalOrphans(ctx, dbIdx, db, localRows, dryRun, now, c)

	// ── Pass 2: Drive file existence (optional) ───────────────────────
	if s.driveFileCheck == nil {
		s.log.Debug("DriveFileChecker not configured, skipping drive orphan pass",
			zap.Int("db_index", dbIdx))
		return
	}
	if err := s.scanDriveOrphans(ctx, dbIdx, db, driveBatch, dryRun, now, c); err != nil {
		s.log.Warn("deep_cleanup drive pass failed", zap.Int("db_index", dbIdx), zap.Error(err))
	}
}

// scanLocalOrphans iterates the rows returned by the local-pass query and
// stamps metadata_json with orphan_locale=1 when the file on disk is gone.
// Caller is responsible for closing the rows handle.
func (s *Service) scanLocalOrphans(
	ctx context.Context,
	dbIdx int,
	db *sql.DB,
	rows *sql.Rows,
	dryRun bool,
	now time.Time,
	c *deepCleanupCounters,
) {
	for rows.Next() {
		var id, lp, alreadyStr, prev string
		if err := rows.Scan(&id, &lp, &alreadyStr, &prev); err != nil {
			continue
		}
		if muted, reason := orphanSuppressed(alreadyStr, prev, now); muted {
			c.SkippedRecent++
			s.log.Debug("deep_cleanup suppressed",
				zap.Int("db_index", dbIdx),
				zap.String("asset_id", id),
				zap.String("local_path", lp),
				zap.String("reason", reason.String()))
			continue
		}
		if _, statErr := os.Stat(lp); statErr == nil {
			continue // local file present
		}
		c.LocalDetected++
		s.log.Info("orphan_locale detected",
			zap.Int("db_index", dbIdx),
			zap.String("asset_id", id),
			zap.String("local_path", lp),
			zap.Bool("dry_run", dryRun))
		if dryRun {
			continue
		}
		if _, uerr := db.ExecContext(ctx,
			`UPDATE media_assets SET metadata_json = json_set(json_set(json_set(COALESCE(metadata_json,'{}'), '$.orphan_locale', 1), '$.orphan_reason', 'local_missing'), '$.orphan_detected_at', ?) WHERE id = ?`,
			now.Format(time.RFC3339), id); uerr == nil {
			c.LocalMarked++
		}
	}
	if err := rows.Err(); err != nil {
		s.log.Warn("deep_cleanup local rows iteration errored",
			zap.Int("db_index", dbIdx), zap.Error(err))
	}
}

// scanDriveOrphans runs the Drive existence pass for one DB. Per-row Drive API
// errors are logged and counted as skips; only the initial scan-query failure
// propagates as an error.
func (s *Service) scanDriveOrphans(
	ctx context.Context,
	dbIdx int,
	db *sql.DB,
	driveBatch int,
	dryRun bool,
	now time.Time,
	c *deepCleanupCounters,
) error {
	driveCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	rows, err := db.QueryContext(driveCtx, `
		SELECT id, COALESCE(drive_link, '') AS dl,
		       COALESCE(json_extract(metadata_json, '$.orphan_drive'), 0) AS already,
		       COALESCE(json_extract(metadata_json, '$.orphan_detected_at'), '') AS prev
		FROM media_assets
		WHERE deleted_at IS NULL
		  AND COALESCE(drive_link, '') != ''
		LIMIT ?`, driveBatch)
	if err != nil {
		return fmt.Errorf("drive query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, dl, alreadyStr, prev string
		if err := rows.Scan(&id, &dl, &alreadyStr, &prev); err != nil {
			continue
		}
		if muted, reason := orphanSuppressed(alreadyStr, prev, now); muted {
			c.SkippedRecent++
			s.log.Debug("deep_cleanup drive pass suppressed",
				zap.Int("db_index", dbIdx),
				zap.String("asset_id", id),
				zap.String("drive_link", dl),
				zap.String("reason", reason.String()))
			continue
		}
		fileID, ferr := urlutil.FileIDFromDriveLink(dl)
		if ferr != nil {
			s.log.Debug("drive_link unparseable, skipping",
				zap.String("asset_id", id),
				zap.String("drive_link", dl),
				zap.Error(ferr))
			continue
		}
		if fileID == "" {
			continue
		}
		ok, checkErr := s.driveFileCheck.FileIsNotTrashed(driveCtx, fileID)
		if checkErr != nil {
			s.log.Warn("Drive orphan check errored",
				zap.String("asset_id", id),
				zap.String("drive_link", dl),
				zap.Error(checkErr))
			continue
		}
		if ok {
			continue // Drive file present
		}
		c.DriveDetected++
		s.log.Info("orphan_drive detected",
			zap.Int("db_index", dbIdx),
			zap.String("asset_id", id),
			zap.String("drive_link", dl),
			zap.Bool("dry_run", dryRun))
		if dryRun {
			continue
		}
		if _, uerr := db.ExecContext(driveCtx,
			`UPDATE media_assets SET metadata_json = json_set(json_set(json_set(COALESCE(metadata_json,'{}'), '$.orphan_drive', 1), '$.orphan_reason', 'drive_trashed'), '$.orphan_detected_at', ?) WHERE id = ?`,
			now.Format(time.RFC3339), id); uerr == nil {
			c.DriveMarked++
		}
	}
	if err := rows.Err(); err != nil {
		s.log.Warn("deep_cleanup drive rows iteration errored",
			zap.Int("db_index", dbIdx), zap.Error(err))
	}
	return nil
}
