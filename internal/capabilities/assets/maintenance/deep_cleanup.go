package maintenance

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
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
// intentionally left to deletion.DeletionService.CleanupOrphanFiles.
func (s *Service) runDeepCleanup(ctx context.Context, dryRun bool) (map[string]any, error) {
	c := &deepCleanupCounters{}
	if len(s.repos) == 0 {
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

	for dbIdx, repo := range s.repos {
		if repo == nil {
			continue
		}
		s.scanDBOrphans(ctx, dbIdx, repo, batch, driveBatch, dryRun, now, c)
	}
	return c.ToMap(), nil
}

// scanDBOrphans runs both the local and Drive passes for a single database.
func (s *Service) scanDBOrphans(
	ctx context.Context,
	dbIdx int,
	repo assets.MaintenanceRepository,
	batch, driveBatch int,
	dryRun bool,
	now time.Time,
	c *deepCleanupCounters,
) {
	// ── Pass 1: local file existence ──────────────────────────────────
	localCandidates, err := repo.ScanLocalOrphans(ctx, batch)
	if err != nil {
		s.log.Warn("deep_cleanup local query failed", zap.Int("db_index", dbIdx), zap.Error(err))
		return
	}
	s.scanLocalOrphans(ctx, dbIdx, repo, localCandidates, dryRun, now, c)

	// ── Pass 2: Drive file existence (optional) ───────────────────────
	if s.driveFileCheck == nil {
		s.log.Debug("DriveFileChecker not configured, skipping drive orphan pass",
			zap.Int("db_index", dbIdx))
		return
	}
	if err := s.scanDriveOrphans(ctx, dbIdx, repo, driveBatch, dryRun, now, c); err != nil {
		s.log.Warn("deep_cleanup drive pass failed", zap.Int("db_index", dbIdx), zap.Error(err))
	}
}

// scanLocalOrphans iterates the candidates returned by the local-pass query and
// stamps metadata_json with orphan_locale=1 when the file on disk is gone.
func (s *Service) scanLocalOrphans(
	ctx context.Context,
	dbIdx int,
	repo assets.MaintenanceRepository,
	candidates []assets.LocalOrphanCandidate,
	dryRun bool,
	now time.Time,
	c *deepCleanupCounters,
) {
	for _, cand := range candidates {
		if muted, reason := orphanSuppressed(cand.AlreadyOrphan, cand.PrevDetectedAt, now); muted {
			c.SkippedRecent++
			s.log.Debug("deep_cleanup suppressed",
				zap.Int("db_index", dbIdx),
				zap.String("asset_id", cand.ID),
				zap.String("local_path", cand.LocalPath),
				zap.String("reason", reason.String()))
			continue
		}
		if _, statErr := os.Stat(cand.LocalPath); statErr == nil {
			continue // local file present
		}
		c.LocalDetected++
		s.log.Info("orphan_locale detected",
			zap.Int("db_index", dbIdx),
			zap.String("asset_id", cand.ID),
			zap.String("local_path", cand.LocalPath),
			zap.Bool("dry_run", dryRun))
		if dryRun {
			continue
		}
		if err := repo.MarkLocalOrphan(ctx, cand.ID, now); err == nil {
			c.LocalMarked++
		}
	}
}

// scanDriveOrphans runs the Drive existence pass for one DB. Per-row Drive API
// errors are logged and counted as skips; only the initial scan-query failure
// propagates as an error.
func (s *Service) scanDriveOrphans(
	ctx context.Context,
	dbIdx int,
	repo assets.MaintenanceRepository,
	driveBatch int,
	dryRun bool,
	now time.Time,
	c *deepCleanupCounters,
) error {
	driveCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	candidates, err := repo.ScanDriveOrphans(driveCtx, driveBatch)
	if err != nil {
		return fmt.Errorf("drive query: %w", err)
	}

	for _, cand := range candidates {
		if muted, reason := orphanSuppressed(cand.AlreadyOrphan, cand.PrevDetectedAt, now); muted {
			c.SkippedRecent++
			s.log.Debug("deep_cleanup drive pass suppressed",
				zap.Int("db_index", dbIdx),
				zap.String("asset_id", cand.ID),
				zap.String("drive_link", cand.DriveLink),
				zap.String("reason", reason.String()))
			continue
		}
		fileID, ferr := urlutil.FileIDFromDriveLink(cand.DriveLink)
		if ferr != nil {
			s.log.Debug("drive_link unparseable, skipping",
				zap.String("asset_id", cand.ID),
				zap.String("drive_link", cand.DriveLink),
				zap.Error(ferr))
			continue
		}
		if fileID == "" {
			continue
		}
		ok, checkErr := s.driveFileCheck.FileIsNotTrashed(driveCtx, fileID)
		if checkErr != nil {
			s.log.Warn("Drive orphan check errored",
				zap.String("asset_id", cand.ID),
				zap.String("drive_link", cand.DriveLink),
				zap.Error(checkErr))
			continue
		}
		if ok {
			continue // Drive file present
		}
		c.DriveDetected++
		s.log.Info("orphan_drive detected",
			zap.Int("db_index", dbIdx),
			zap.String("asset_id", cand.ID),
			zap.String("drive_link", cand.DriveLink),
			zap.Bool("dry_run", dryRun))
		if dryRun {
			continue
		}
		if err := repo.MarkDriveOrphan(driveCtx, cand.ID, now); err == nil {
			c.DriveMarked++
		}
	}
	return nil
}
