// Package app — DB setup helpers (PG-006 + Cut 6.2 sibling, June/July 2026).
//
// Extracted from bootstrap.go so the bootstrap.go entry-point file remains
// strictly free of `internal/infrastructure/*` imports. The `databases`
// struct + `InitDatabases` + `RunAllMigrations` helpers are pure concrete
// wiring (storage.OpenSet, connection pooling + WAL/busy_timeout config,
// schema migration); only the composition root is allowed to keep the
// infra imports.
//
// PG-006 (June 2026): `internal/infrastructure/**` is the only file tree
// allowed to import concrete SDK / driver code; `internal/app/**` is the
// composition root that wires the infra into the application domain.
// PG-006 narrows the rule: bootstrap.go specifically must stay free of
// infra imports so the API tree's dependency on app remains strictly
// typed.
//
// FASE 6 Cut 6.2 sibling (July 2026): the `dualPool *sqlite.DualPool`
// field is ADDED alongside the existing `main *storage.SQLiteDB` field.
// `dbs.DualPool.Writer` is the canonical write-side *sql.DB handle for
// repository construction (Cut 6.2 A3 verdict: every repo gets Writer
// by default; Reader migration is a forward-pointer to a future cut).
// `dbs.Main` (the storage.SQLiteDB wrapper) is RETAINED for health /
// observability consumers that don't decompose into writer/reader
// (infrahealth.NewSQLiteChecker, NewDriveRootsValidator). The two
// pools share the same on-disk file via WAL-mode concurrent-reader +
// single-writer semantics (see sqlite.go::NewDualPool rationale and
// sqlite/pool.go / pool_test.go for the canonical Cut 6.2 surface).
package wiring

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"

	"go.uber.org/zap"
)

// CleanupFunc is the type returned by initialization functions for teardown.
type CleanupFunc func()

// databases is the composition-root view of `storage.DatabaseSet`.
// Exists only to keep the consumer-facing API of composition.go stable
// (every Build*Bundle() takes `*Databases`); the inner state delegates
// to the canonical DatabaseSet opened by `storage.OpenSet` (rule: no
// `sql.Open` outside `internal/platform/sqlite/**`).
//
// `main` and `logs` fields are kept for back-compat with the dozens of
// `dbs.Main.<X>` references in `composition.go` / `shutdown.go` /
// `registry.go` / `dependencies.go`. They are populated from the
// DatabaseSet at construction time; the canonical source of truth is
// `dbs.Set.Primary` / `dbs.Set.Observability`.
//
// PR-Queue-Split-EXPAND (June 2026): the `jobs` field is added for the
// EXPAND-on flag shape. When cfg.Jobs.SplitDBEnabled is true,
// `InitDatabases` opens a separate jobs.db.sqlite file via
// storage.OpenSQLiteDB and populates this field; composition.go picks
// `dbs.Jobs` over `dbs.Main` for the JobsBundle's *SQLiteStore when
// `dbs.Jobs != nil`. Default behaviour: `jobs` stays nil.
//
// Cut 6.2 sibling (July 2026): the `dualPool` field is added for the
// WAL-mode reader/writer split. Repositories throughout the composition
// tree thread Writer and Reader from the dualPool; health/observability
// consumers keep using storage.SQLiteDB (`dbs.Main.DB`) because the
// `infrahealth.NewSQLiteChecker(db)` constructor takes the storage
// wrapper as its argument. The two pools share the on-disk file via
// WAL-mode SQLite concurrency.
//
// Field population rules (godlike/06 SSOT):
//   - dbs.Set: always non-nil after a successful OpenSet.
//   - dbs.Main: storage.SQLiteDB wrapper around dbs.Set.Primary.DB.
//   - dbs.Logs: storage.SQLiteDB wrapper around dbs.Set.Observability.DB.
//   - dbs.DualPool: nil in tests that bypass NewDualPool (legacy TestDB).
//     Production callers (InitDatabases) MUST construct a non-nil
//     DualPool so the canonical instrumentation surface (Cut 6.2
//     metrics + EXPLAIN) fires at boot.
//   - dbs.Jobs: non-nil only when cfg.Jobs.SplitDBEnabled=true.
type Databases struct {
	Set      *storage.DatabaseSet
	Main     *storage.SQLiteDB
	Logs     *storage.SQLiteDB
	DualPool *sqlite.DualPool

	// jobs is the EXPAND CANONICAL queue DB. nil when SplitDBEnabled is
	// false (today's default); non-nil when the EXPAND flag is on. Both
	// `jobs` and `main` may be open simultaneously during the EXPAND
	// bench window — the closure path closes jobs first to ensure
	// outbound writes from jobs do not race the main DB's WAL flush.
	Jobs *storage.SQLiteDB
}

func (d *Databases) Close() {
	// Close the dualPool BEFORE storage.DatabaseSet so the
	// Cut 6.2-instrumented txs (connection_wait_seconds,
	// tx_duration_seconds, sqlite_busy_total) finish their
	// observation windows before the underlying *sql.DB handles
	// disappear. The Media DB and jobs DB share no locks today
	// (different files), but the order is documented as a
	// future-proof invariant for when a multi-node pgbroker.Store
	// replaces the SQLite pair with a single PG backend.
	if d.DualPool != nil {
		_ = d.DualPool.Close()
	}
	if d.Jobs != nil {
		_ = d.Jobs.Close()
	}
	if d.Set != nil {
		_ = d.Set.Close()
	}
}

// InitDatabases opens BOTH the primary + observability DBs via the
// canonical `storage.OpenSet` (codex/db-set-and-paths). No `sql.Open`
// remains outside `internal/platform/sqlite/**`.
//
// Cut 6.2 sibling (July 2026): after the storage set opens, the
// composition root constructs an additional DualPool via
// sqlite.NewDualPool on the SAME primary file. The dual pool holds the
// writer (MaxOpenConns=1) + reader (MaxOpenConns=runtime.NumCPU())
// per the canonical Cut 6.2 design. Repositories consumed by Build*
// Bundle()s migrate to dbs.DualPool.Writer (default canonical writer
// path per Cut 6.2 A3 verdict); read-only observation paths may
// migrate to dbs.DualPool.Reader in a follow-up cut.
//
// godlike/07 fail-closed: a NewDualPool error aborts the boot sequence
// rather than silently regressing back to dbs.Main.DB. Migration
// failure surfaces as a typed error from CleanupStack rather than as
// a deadlocked writer tx at first write.
//
// PR-Queue-Split-EXPAND (June 2026): when cfg.Jobs.SplitDBEnabled is
// true, also opens jobs.db.sqlite via storage.OpenSQLiteDB and
// populates dbs.Jobs.
func InitDatabases(ctx context.Context, cfg *config.Config, log *zap.Logger) (*Databases, error) {
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
	dbs := &Databases{
		Set:  set,
		Main: set.Primary,
		Logs: set.Observability,
	}

	// Cut 6.2 sibling: build the canonical WAL-mode DualPool on the
	// same primary file. The dual pool is the canonical connection
	// surface for code that wants the connection_wait_seconds +
	// tx_duration_seconds + sqlite_busy_total instrumentation; legacy
	// dbs.Main.DB remains for health-check consumers that need the
	// storage.SQLiteDB wrapper (infrahealth.NewSQLiteChecker).
	dualPool, dErr := sqlite.NewDualPool(ctx, setCfg.PrimaryDBPath, runtime.NumCPU())
	if dErr != nil {
		dbs.Close()
		return nil, fmt.Errorf("init databases: NewDualPool: %w", dErr)
	}
	dbs.DualPool = dualPool
	log.Info("Cut 6.2 dualPool wired (WAL-mode; writer=1, readers=NumCPU)",
		zap.Int("num_readers", runtime.NumCPU()),
		zap.String("primary_path", setCfg.PrimaryDBPath),
	)

	// PR-Queue-Split-EXPAND: gate at InitDatabases so the JobsBundle can
	// pick the right DB at composition-root call time (BuildJobsBundle's
	// signature does not need to change — it accepts a *storage.SQLiteDB).
	if cfg != nil && cfg.Jobs.SplitDBEnabled {
		jobsPath := jobsDBPathFromPrimary(cfg.Storage.PrimaryDBFullPath())
		if cfg.Jobs.JobsDBPath != "" {
			jobsPath = cfg.Jobs.JobsDBPath
		}
		jobsDB, jobsOpenErr := storage.OpenSQLiteDB(jobsPath, log)
		if jobsOpenErr != nil {
			// Fail-closed: closing any partial state (dualPool +
			// main DB) so a half-open triple does not leak. The
			// operator either retries with a fixed path OR flips
			// SplitDBEnabled=false to fall back to the canonical
			// single-DB shape.
			dbs.Close()
			return nil, fmt.Errorf("init jobs DB %s: %w", jobsPath, jobsOpenErr)
		}
		dbs.Jobs = jobsDB
		log.Info("PR-Queue-Split-EXPAND: Jobs DB opened alongside media DB",
			zap.String("main_db", dbs.Main.Path()),
			zap.String("jobs_db", jobsDB.Path()),
		)
	}

	return dbs, nil
}

// ValidateControlPlaneIdentity verifies the durable identity of the primary
// control plane and the configured single-writer topology. The observability
// database is intentionally excluded: it is an operational log store, not a
// control-plane writer. A split jobs database is a second writable control
// plane and therefore fails closed until it is assigned a non-writer role by
// an explicit migration/cutover.
func (d *Databases) ValidateControlPlaneIdentity(ctx context.Context) error {
	if d == nil || d.Main == nil || d.Main.DB == nil {
		return fmt.Errorf("control plane identity: primary database is not configured")
	}
	meta, err := storage.ReadControlPlaneMeta(ctx, d.Main.DB)
	if err != nil {
		return err
	}
	if meta.InstanceRole != storage.ControlPlaneRoleCanonical {
		return fmt.Errorf("control plane identity: primary database_id=%q has role %q, want %q", meta.DatabaseID, meta.InstanceRole, storage.ControlPlaneRoleCanonical)
	}

	databases := []storage.ConfiguredDatabase{{
		Name:         "primary",
		Path:         d.Main.Path(),
		Role:         meta.InstanceRole,
		Writable:     true,
		ControlPlane: true,
	}}
	if d.Logs != nil {
		// Observability is a separately writable operational log store,
		// not a control-plane writer. Keeping it in the inventory makes
		// that boundary explicit instead of silently omitting a known DB.
		databases = append(databases, storage.ConfiguredDatabase{
			Name:         "observability",
			Path:         d.Logs.Path(),
			Role:         storage.ControlPlaneRoleReadOnly,
			Writable:     true,
			ControlPlane: false,
		})
	}
	if d.Jobs != nil {
		databases = append(databases, storage.ConfiguredDatabase{
			Name:         "jobs",
			Path:         d.Jobs.Path(),
			Role:         storage.ControlPlaneRoleCanonical,
			Writable:     true,
			ControlPlane: true,
		})
	}
	if err := storage.ValidateConfiguredControlPlaneWriters(databases); err != nil {
		return err
	}
	return nil
}

// jobsMigrationsDir is the EXPAND-PR-Queue-Split migration directory
// scanned at boot time WHEN dbs.Jobs is open (SplitDBEnabled=true). The
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
// media.db.sqlite (operator-set custom layout); the caller (InitDatabases)
// decides whether to error or pass through.
func jobsDBPathFromPrimary(primaryPath string) string {
	dir := filepath.Dir(primaryPath)
	base := filepath.Base(primaryPath)
	if !strings.HasSuffix(base, "media.db.sqlite") {
		return primaryPath
	}
	return filepath.Join(dir, strings.Replace(base, "media.db.sqlite", "jobs.db.sqlite", 1))
}

func RunAllMigrations(dbs *Databases, log *zap.Logger) error {
	if err := dbs.Set.Migrate(log); err != nil {
		return err
	}
	// The identity gate runs immediately after the primary migration pass,
	// before any optional split-database migration can start. This makes a
	// second writable control-plane database fail closed at boot rather than
	// allowing it to begin serving writes.
	if err := dbs.ValidateControlPlaneIdentity(context.Background()); err != nil {
		return fmt.Errorf("validate control plane identity: %w", err)
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
	if dbs.Jobs != nil {
		// TargetDB="primary" — the EXPAND-OBSERVABILITY
		// jobs DB is a split-shard of the canonical media DB and shares
		// the same domain shape. Its peer migrations dir
		// (migrations/sqlite_jobs/) is disjoint from migrations/sqlite/,
		// so the scope check is a defensive no-op for the jobs DB
		// (nothing inside jobsMigrationsDir carries a `-- database:`
		// directive today).
		if err := dbs.Jobs.RunMigrations(log, jobsMigrationsDir, "primary"); err != nil {
			return fmt.Errorf("jobs-db migrations: %w", err)
		}
	}
	return nil
}
