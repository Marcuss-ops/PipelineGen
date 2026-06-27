// Package app — DB setup helpers (PG-006, June 2026).
//
// Extracted from bootstrap.go so the bootstrap.go entry-point file remains
// strictly free of `internal/infrastructure/*` imports. The `databases`
// struct + `initDatabases` + `runAllMigrations` helpers are pure concrete
// wiring (storage.OpenSet, connection pooling + WAL/busy_timeout config,
// schema migration); only the composition root is allowed to keep the
// infra imports.
//
// Context: AGENTS.md §13 — `internal/infrastructure/**` is the only file
// tree allowed to import concrete SDK / driver code; `internal/app/**` is
// the composition root that wires the infra into the application domain
// via typed ports. PG-006 narrows the rule: bootstrap.go specifically
// must stay free of infra imports so the API tree's dependency on app
// remains strictly typed.
package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"

	"go.uber.org/zap"
)

// CleanupFunc is the type returned by initialization functions for teardown.
type CleanupFunc func()

// databases is the composition-root view of `storage.DatabaseSet`.
// Exists only to keep the consumer-facing API of composition.go stable
// (every Build*Bundle() takes `*databases`); the inner state delegates
// to the canonical DatabaseSet opened by `storage.OpenSet` (rule: no
// `sql.Open` outside `internal/infrastructure/database/**`).
//
// `main` and `logs` fields are kept for back-compat with the dozens of
// `dbs.main.<X>` references in `composition.go` / `shutdown.go` /
// `registry.go` / `dependencies.go`. They are populated from the
// DatabaseSet at construction time; the canonical source of truth is
// `dbs.set.Primary` / `dbs.set.Observability`.
//
// PR-Queue-Split-EXPAND / ADR-0003 (June 2026): the `jobs` field is added
// for the EXPAND-on flag shape. When cfg.Jobs.SplitDBEnabled is true,
// `initDatabases` opens a separate jobs.db.sqlite file via
// storage.OpenSQLiteDB and populates this field; composition.go picks
// `dbs.jobs` over `dbs.main` for the JobsBundle's *SQLiteStore when
// `dbs.jobs != nil`. EXPAND boots a fresh jobs.db.sqlite via
// RunMigrations with `migrations/sqlite_jobs/` as the migration directory.
// Default behaviour (SplitDBEnabled=false): `jobs` stays nil — no extra
// DB opens, no extra migrations run, no callsite changes — today's
// production deployments are unaffected.
type databases struct {
	set  *storage.DatabaseSet
	main *storage.SQLiteDB
	logs *storage.SQLiteDB

	// jobs is the EXPAND CANONICAL queue DB. nil when SplitDBEnabled is
	// false (today's default); non-nil when the EXPAND flag is on. Both
	// `jobs` and `main` may be open simultaneously during the EXPAND
	// bench window — the closure path closes jobs first to ensure
	// outbound writes from jobs do not race the main DB's WAL flush.
	jobs *storage.SQLiteDB
}

func (d *databases) Close() {
	// Close jobs BEFORE the DatabaseSet so any in-flight job write tx
	// commits against the jobs DB before its SQLite WAL is checkpointed.
	// The Media DB and jobs DB share no locks today (different files),
	// but the order is documented as a future-proof invariant for when
	// PR-B (multi-node pgbroker.Store) replaces the SQLite pair with a
	// single PG backend — the close order will mirror the dependency
	// graph there. Today, closing jobs first is a no-op-vs-the-other-order
	// but documents the invariant for maintainers reading the code.
	if d.jobs != nil {
		_ = d.jobs.Close()
	}
	if d.set != nil {
		_ = d.set.Close()
	}
}

// initDatabases opens BOTH the primary + observability DBs via the
// canonical `storage.OpenSet` (codex/db-set-and-paths). No `sql.Open`
// remains outside `internal/infrastructure/database/**`.
//
// PR-Queue-Split-EXPAND / ADR-0003 (June 2026): when cfg.Jobs.SplitDBEnabled
// is true, also opens jobs.db.sqlite via storage.OpenSQLiteDB and
// populates dbs.jobs. The path resolution rules: cfg.Jobs.JobsDBPath
// (explicit operator override) wins over the canonical derivation
// (jobsDBPathFromPrimary — strip "media.db.sqlite" from the primary
// path's basename). Fail-closed: opening the jobs DB is logged +
// returned as an error so a misconfigured layout surfaces at boot, not
// at first-claim-time.
func initDatabases(cfg *config.Config, log *zap.Logger) (*databases, error) {
	setCfg := storage.StorageConfig{
		DataDir:             cfg.Storage.DataDir,
		PrimaryDBPath:       cfg.Storage.PrimaryDBFullPath(),
		ObservabilityDBPath: cfg.Storage.ObservabilityDBFullPath(),
		WorkspaceDir:        cfg.Storage.WorkspaceDir,
		CacheDir:            cfg.Storage.CacheDir,
		ExportDir:           cfg.Storage.ExportDir,
	}
	set, err := storage.OpenSet(setCfg, log)
	if err != nil {
		return nil, fmt.Errorf("init databases: %w", err)
	}
	dbs := &databases{
		set:  set,
		main: set.Primary,
		logs: set.Observability,
	}

	// PR-Queue-Split-EXPAND: gate at initDatabases so the JobsBundle can
	// pick the right DB at composition-root call time (BuildJobsBundle's
	// signature does not need to change — it accepts a *storage.SQLiteDB).
	if cfg != nil && cfg.Jobs.SplitDBEnabled {
		jobsPath := jobsDBPathFromPrimary(cfg.Storage.PrimaryDBFullPath())
		if cfg.Jobs.JobsDBPath != "" {
			jobsPath = cfg.Jobs.JobsDBPath
		}
		jobsDB, jobsOpenErr := storage.OpenSQLiteDB(jobsPath, log)
		if jobsOpenErr != nil {
			// Fail-closed: closing any partial state (main DB) so a
			// half-open pair does not leak. The operator either retries
			// with a fixed path OR flips SplitDBEnabled=false to fall
			// back to the canonical single-DB shape.
			dbs.Close()
			return nil, fmt.Errorf("init jobs DB %s: %w", jobsPath, jobsOpenErr)
		}
		dbs.jobs = jobsDB
		log.Info("PR-Queue-Split-EXPAND: jobs DB opened alongside media DB",
			zap.String("main_db", dbs.main.Path()),
			zap.String("jobs_db", jobsDB.Path()),
			zap.Bool("legacy_alias_enabled", cfg.Jobs.LegacyAliasEnabled),
		)
	}

	return dbs, nil
}

// jobsMigrationsDir is the EXPAND-PR-Queue-Split migration directory
// scanned at boot time WHEN dbs.jobs is open (SplitDBEnabled=true). The
// directory mirrors the canonical migrations/sqlite/ but scoped to the
// three jobs-domain tables — media-side migrations stay on media.db.sqlite.
//
// Why a peer directory instead of re-using migrations/sqlite/:
//   - the migrations runner's ledger table (schema_migrations) is
//     per-database: media.db.sqlite has its own ledger, jobs.db.sqlite
//     has its own. They cannot share a directory because the ledger
//     entry `INSERT INTO schema_migrations (...)` writes to whichever
//     DB the runner is connected to at the time.
//   - peer directory filenames use the standard NNN_<descriptive>.sql
//     convention so the runner discovers them identically to media's
//     migrations (no caller-side change to migrateAll required).
//   - media.db.sqlite's existing migrations (001_velox_core, 022, 053, …)
//     stay on media's ledger untouched — a split DB never replays the
//     media-side history because EXPAND assumes a fresh jobs DB starting
//     from the FINAL canonical shape (see 000_initial_jobs_schema.sql).
const jobsMigrationsDir = "migrations/sqlite_jobs"

// jobsDBPathFromPrimary derives the default jobs.db.sqlite path by
// stripping "media.db.sqlite" from the primary path's basename and
// substituting "jobs.db.sqlite" — the canonical pair lives side-by-side.
// Returns the primary path verbatim if it does not end in
// media.db.sqlite (operator-set custom layout); the caller (initDatabases)
// decides whether to error or pass through.
func jobsDBPathFromPrimary(primaryPath string) string {
	dir := filepath.Dir(primaryPath)
	base := filepath.Base(primaryPath)
	if !strings.HasSuffix(base, "media.db.sqlite") {
		return primaryPath
	}
	return filepath.Join(dir, strings.Replace(base, "media.db.sqlite", "jobs.db.sqlite", 1))
}

func runAllMigrations(dbs *databases, log *zap.Logger) error {
	if err := dbs.set.Migrate(log); err != nil {
		return err
	}
	// EXPAND / ADR-0003: when the jobs DB is open, run its peer-ledger
	// migrations from migrations/sqlite_jobs/ . Each DB has its own
	// schema_migrations table; the runner's per-tx recording is
	// independent. Fail-closed: any jobs-DB migration error aborts the
	// boot sequence (per godlike/07 §"Migration sequence" EXPAND phase
	// invariant). This blocks the case where an operator flips
	// SplitDBEnabled=true on a deployment with a no-op or half-decoded
	// jobs.db.sqlite file but expects prod to boot regardless — the
	// runner must surface the gap, not silently no-op.
	if dbs.jobs != nil {
		if err := dbs.jobs.RunMigrations(log, jobsMigrationsDir); err != nil {
			return fmt.Errorf("jobs-db migrations: %w", err)
		}
	}
	return nil
}
