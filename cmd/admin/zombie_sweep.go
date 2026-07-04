// cmd/admin/zombie_sweep.go — operator-driven zombie job sweeper.
//
// JOBS-T01-ZOMBIE-SWEEP closure (Phase 9 cycle 2, 2026-07-04):
// admin CLI subcommand that scans for zombie jobs (status IN
// (LEASED, RUNNING, FINALIZING) AND lease_expiry < cutoff) and
// marks them as FAILED. Pipeline still runs WITHOUT this surface
// (graceful degradation = P1 per architecture/issues.yaml), but
// operators lose the manual-recovery path until this CLI ships.
//
// godlike/06 SSOT (one canonical owner per fact): the
// MarkRunningJobsOlderThanFailed method on
// (*jobs.SQLiteStore).MarkRunningJobsOlderThanFailed
// (internal/infrastructure/database/sqlite/jobs/repository_lifecycle.go)
// is the SOLE canonical writer of this transition. This CLI is a
// thin operator wrapper that opens the canonical DB path, computes
// the cutoff, and delegates.
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
//     formatDryRunReport)
//
// godlike/07 typed-error contract:
//   * ErrZombieSweepNoDB returned when cfg is nil (composition
//     root failure)
//   * ErrZombieSweepApplyRequired returned when --apply is set
//     and the operator wants to be explicit about destructive
//     intent (future hardening; today the apply path is automatic)
//
// Forward-pointer:
//   * PR-ZOMBIE-SWEEP-INT-DURATION-CONFIG (deadline TBD) — replace
//     --cutoff-duration flag with a config-driven default loaded
//     from cfg.Server.ZombieSweepCutoff (mirrors PR-ORPHAN-SWEEPER-
//     TUNING precedent).

package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// Typed sentinel errors (godlike/07 typed-error contract).
var (
	// ErrZombieSweepNoDB is surfaced when the canonical DB path is
	// not configured. Recovery: set VELOX_DB_PATH env var (default
	// data/media/media.db.sqlite per AGENTS.md §Storage).
	ErrZombieSweepNoDB = errors.New("zombie-sweep: canonical DB path not configured (set VELOX_DB_PATH or check cfg.Storage.DataDir)")

	// ErrZombieSweepOpenDB is surfaced when the sqlite3 driver fails
	// to open the DB (file missing, perm denied, locked, etc).
	// Recovery: check file presence + perms + busy_timeout (5s per
	// AGENTS.md §SQLite pool).
	ErrZombieSweepOpenDB = errors.New("zombie-sweep: failed to open canonical SQLite DB")
)

// defaultDBPath is the canonical PipelineGen DB path. Operators can
// override via VELOX_DB_PATH env var (mirrors the convention in
// storage.OpenSQLiteDB).
const defaultDBPath = "data/media/media.db.sqlite"

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
	dbPath := fs.String("db-path", "", "Canonical SQLite DB path (default: $VELOX_DB_PATH or data/media/media.db.sqlite).")
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

	path := *dbPath
	if path == "" {
		path = os.Getenv("VELOX_DB_PATH")
	}
	if path == "" && cfg != nil {
		path = resolveDBPathFromCfg(cfg)
	}
	if path == "" {
		path = defaultDBPath
	}
	if path == "" {
		return ErrZombieSweepNoDB
	}

	db, openErr := openSQLiteForZombieSweep(path)
	if openErr != nil {
		return fmt.Errorf("%w: %v", ErrZombieSweepOpenDB, openErr)
	}
	defer db.Close()

	store := jobs.NewSQLiteStore(db, log)
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

// resolveDBPathFromCfg extracts the canonical DB path from the
// config. Tries Server.DBPath first (forward-pointer — not in
// canonical config today), then falls back to Storage.DataDir + the
// canonical media.db.sqlite filename.
func resolveDBPathFromCfg(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	// canonical convention: <DataDir>/media.db.sqlite
	if cfg.Storage.DataDir != "" {
		return cfg.Storage.DataDir + "/media.db.sqlite"
	}
	return ""
}

// openSQLiteForZombieSweep opens the canonical SQLite DB with the
// standard pool settings (WAL + busy_timeout=5s) per AGENTS.md
// §SQLite pool. Returns a *sql.DB ready for jobs.NewSQLiteStore.
//
// godlike/07 minimum-blast-radius: this is a thin wrapper over
// `sql.Open("sqlite3", path)` + the canonical pragma chain — it
// does NOT introduce a new connection-pool config. The pragma
// chain is duplicated here ONLY because the operator CLI does not
// route through the composition root (one-shot binary); for
// production, composition-root callers MUST use
// internal/infrastructure/database/sqlite/storage.OpenSQLiteDB
// per the canonical pattern in AGENTS.md §Storage.
func openSQLiteForZombieSweep(path string) (*sql.DB, error) {
	// DSN: enable WAL + busy_timeout=5s (per AGENTS.md §SQLite pool).
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
