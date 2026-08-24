// cmd/admin/zombie_sweep.go — operator-driven zombie job sweeper.
//
// JOBS-T01-ZOMBIE-SWEEP closure (Phase 9 cycle 2, 2026-07-04):
// admin CLI subcommand that scans for zombie jobs (status IN
// (LEASED, RUNNING, FINALIZING) AND lease_expiry < cutoff) and
// marks them as FAILED. Pipeline still runs WITHOUT this surface
// (graceful degradation = P1 per architecture/issues.yaml), but
// operators lose the manual-recovery path until this CLI ships.
//
// Round-2 refactor (2026-07-04): replaced the hand-rolled
// sql.Open("sqlite3", dsn) + DSN-chain with the canonical
// storage.OpenSQLiteDB helper (same pattern used by 24+ other
// cmd/admin/ files: qdrant_readiness.go, dr_qdrant.go,
// reindex_qdrant.go, backfill_media_assets_search_terms.go, etc.).
// The helper applies the canonical pragma chain (WAL +
// foreign_keys=on + busy_timeout=5000) per AGENTS.md §SQLite pool;
// the custom DSN chain in this file was a godlike/07
// minimum-blast-radius violation (duplicate of the canonical
// helper, drift risk on pragma changes).
//
// Round-2b refactor (2026-07-04): replaced the hand-rolled
// cfg.Storage.DataDir + "/media.db.sqlite" concatenation in
// resolveDBPath with the canonical cfg.Storage.PrimaryDBFullPath()
// helper (internal/platform/config/types.go:481) — single source
// of truth for the canonical DB path (per AGENTS.md §Storage
// godlike/06 SSOT one-canonical-owner-per-fact).
//
// godlike/06 SSOT (one canonical owner per fact): the
// (*jobs.SQLiteStore).MarkRunningJobsOlderThanFailed method
// (internal/platform/sqlite/jobs/repository_lifecycle.go)
// is the SOLE canonical writer of this transition. This CLI is a
// thin operator wrapper that opens the canonical DB path via
// storage.OpenSQLiteDB, computes the cutoff, and delegates.
//
// godlike/07 NO-FAKE-AVAILABILITY:
//   * --dry-run is the default (no DB write); --apply is the
//     operator-explicit opt-in
//   * The CLI never silently swallows the MarkRunningJobsOlderThanFailed
//     error — it surfaces verbatim via fmt.Errorf %w
//   * The cutoff is reported (RFC3339) so the operator can verify
//     the threshold BEFORE --apply
//
// godlike/07 minimum-blast-radius:
//   * Zero new tests for the DB-write path (the underlying
//     MarkRunningJobsOlderThanFailed is exercised by
//     internal/application/jobs/service_test.go)
//   * TDD coverage for the cutoff computation + dry-run output via
//     zombie_sweep_test.go (pure-function split: computeCutoff +
//     formatDryRunReport + resolveDBPath)
//
// godlike/07 typed-error contract:
//   * ErrZombieSweepNoDB returned when cfg is nil (composition
//     root failure) OR cfg.Storage.PrimaryDBFullPath returns ""
//   * ErrZombieSweepOpenDB returned as a typed wrapper around the
//     storage-layer error (preserves the failure-mode category for
//     downstream errors.Is probes; the storage error itself is
//     surfaced via %w for diagnostics)
//
// Forward-pointer:
//   * PR-ZOMBIE-SWEEP-INT-DURATION-CONFIG (deadline TBD) — replace
//     --cutoff-duration flag with a config-driven default loaded
//     from cfg.Server.ZombieSweepCutoff (mirrors PR-ORPHAN-SWEEPER-
//     TUNING precedent).

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// Typed sentinel errors (godlike/07 typed-error contract).
var (
	// ErrZombieSweepNoDB is surfaced when the canonical DB path is
	// not configured. Recovery: set VELOX_DATA_DIR env var (default
	// ./data per cfg.Storage.DataDir; the canonical pipelinegen
	// DB lives at <DataDir>/media.db.sqlite per AGENTS.md §Storage).
	ErrZombieSweepNoDB = errors.New("zombie-sweep: canonical DB path not configured (set VELOX_DATA_DIR or check cfg.Storage.PrimaryDBPath)")

	// ErrZombieSweepOpenDB is surfaced as a typed wrapper around
	// the storage-layer error (storage.OpenSQLiteDB wraps
	// sql.Open + ping errors with a typed %w). The sentinel
	// preserves the failure-mode category for downstream
	// errors.Is probes; the underlying storage error is surfaced
	// verbatim via %w for diagnostics.
	ErrZombieSweepOpenDB = errors.New("zombie-sweep: failed to open canonical SQLite DB")
)

// defaultCutoff is the canonical cutoff duration: jobs with
// lease_expiry older than now-1h are considered zombie. Operators
// can override via --cutoff-duration.
const defaultCutoff = 1 * time.Hour

// defaultReason is the canonical reason string recorded in jobs.error
// when a zombie is marked FAILED. Operators can override via
// --reason (mirrors the AGENTS.md §Active Concerns audit-pin
// discipline — leave a breadcrumb).
const defaultReason = "swept by admin zombie-sweep CLI (Phase 9 JOBS-T01-ZOMBIE-SWEEP closure)"

// computeCutoff is the pure-function seam for the cutoff
// computation. TDD-pinned in zombie_sweep_test.go (no DB, no time
// injection today — operator passes an explicit duration).
func computeCutoff(now time.Time, dur time.Duration) time.Time {
	return now.UTC().Add(-dur)
}

// formatDryRunReport is the pure-function seam for the dry-run
// output. TDD-pinned in zombie_sweep_test.go. Stays byte-stable
// across refactors so operators can rely on the format.
func formatDryRunReport(cutoff time.Time, reason string) string {
	return fmt.Sprintf(
		"zombie-sweep: DRY-RUN — would mark jobs with lease_expiry < %s as FAILED (reason: %q)\n"+
			"zombie-sweep: DRY-RUN — pass --apply to execute the sweep",
		cutoff.Format(time.RFC3339), reason)
}

// resolveDBPath determines the canonical SQLite DB path with the
// following precedence (per AGENTS.md §Storage godlike/06 SSOT
// one-canonical-owner-per-fact):
//  1. --db-path flag (operator-explicit override)
//  2. cfg.Storage.PrimaryDBFullPath() (canonical helper, returns
//     the cfg-supplied PrimaryDBPath OR the default
//     <DataDir>/media.db.sqlite)
//
// Returns "" if cfg is nil AND the --db-path flag is empty
// (caller surfaces ErrZombieSweepNoDB). Otherwise returns the
// resolved canonical path.
//
// Round-2b refactor (2026-07-04): replaced the hand-rolled
// cfg.Storage.DataDir + "/media.db.sqlite" concatenation with the
// canonical cfg.Storage.PrimaryDBFullPath() helper. The helper
// is the single source of truth for the canonical DB path
// (internal/platform/config/types.go:481) and is shared by 24+
// callers (qdrant_readiness.go, reindex_qdrant.go,
// backfill_media_assets_search_terms.go, etc.). A drift in the
// canonical helper now propagates to zombie_sweep automatically;
// before the refactor, the hand-rolled concat would silently
// drift if cfg adds a new override knob.
func resolveDBPath(cfg *config.Config, dbPathFlag string) string {
	if dbPathFlag != "" {
		return dbPathFlag
	}
	if cfg == nil {
		return ""
	}
	return cfg.Storage.PrimaryDBFullPath()
}

// runZombieSweep is the cmd/admin/main.go switch arm entry point.
// godlike/06 SSOT: the registered `case "zombie-sweep":` arm is
// the SOLE entry surface; no other caller in the codebase invokes
// this function (operator CLI only).
func runZombieSweep(args []string) error {
	fs := flag.NewFlagSet("zombie-sweep", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cutoffDur := fs.Duration("cutoff-duration", defaultCutoff, "How far back to consider a job 'zombie' (default 1h). Jobs with lease_expiry < now-<-cutoff-duration> are swept.")
	reason := fs.String("reason", defaultReason, "Reason string recorded in jobs.error when a zombie is marked FAILED.")
	apply := fs.Bool("apply", false, "Actually mark zombie jobs as FAILED (default: dry-run only).")
	dbPath := fs.String("db-path", "", "Canonical SQLite DB path (default: $VELOX_DATA_DIR/media.db.sqlite per cfg.Storage.PrimaryDBFullPath).")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Compute cutoff at the canonical seam. TDD-pinned pure function.
	cutoff := computeCutoff(time.Now(), *cutoffDur)

	// Dry-run is the default — operator-explicit --apply opt-in.
	if !*apply {
		fmt.Println(formatDryRunReport(cutoff, *reason))
		return nil
	}

	// --apply path: open the canonical DB, build the store, sweep.
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	path := resolveDBPath(cfg, *dbPath)
	if path == "" {
		return ErrZombieSweepNoDB
	}

	sqliteDB, openErr := storage.OpenSQLiteDB(path, log)
	if openErr != nil {
		return fmt.Errorf("%w: %w", ErrZombieSweepOpenDB, openErr)
	}
	defer sqliteDB.Close()

	store := jobs.NewSQLiteStore(sqliteDB.DB, log)
	ctx := cmdContext()
	n, sweepErr := store.MarkRunningJobsOlderThanFailed(ctx, cutoff, *reason)
	if sweepErr != nil {
		return fmt.Errorf("zombie-sweep: MarkRunningJobsOlderThanFailed: %w", sweepErr)
	}

	log.Info("zombie-sweep: applied",
		zap.Int("marked_failed", n),
		zap.Time("cutoff", cutoff),
		zap.String("reason", *reason),
	)
	fmt.Printf("zombie-sweep: marked %d zombie jobs as FAILED (cutoff: %s, reason: %q)\n",
		n, cutoff.Format(time.RFC3339), *reason)
	return nil
}
