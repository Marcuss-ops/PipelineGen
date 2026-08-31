// Package app — DB setup helpers (PG-006 + Cut 6.2 sibling, June/July 2026).
//
// Extracted from bootstrap.go so the bootstrap.go entry-point file remains
// strictly free of `internal/platform/*` imports. The `databases`
// struct + `InitDatabases` + `RunAllMigrations` helpers are pure concrete
// wiring (storage.OpenSet, connection pooling + WAL/busy_timeout config,
// schema migration); only the composition root is allowed to keep the
// infra imports.
//
// PG-006 (June 2026): `internal/platform/**` is the only file tree
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
// A second operational SQLite database is intentionally not supported;
// the unified runtime uses only Main for business state and Logs for the
// separate observability axis.
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
//   - dbs.Jobs: always nil in the unified single-primary runtime shape.
type Databases struct {
	Set      *storage.DatabaseSet
	Main     *storage.SQLiteDB
	Logs     *storage.SQLiteDB
	DualPool *storage.DualPool

	// Jobs is retained as a nil-only compatibility field while callers
	// migrate away from the retired split-database shape.
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
// storage.NewDualPool on the SAME primary file. The dual pool holds the
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
// The retired jobs.db.sqlite split is rejected fail-closed; no second
// operational database is opened.
func InitDatabases(ctx context.Context, cfg *config.Config, log *zap.Logger) (*Databases, error) {
	if cfg == nil {
		return nil, fmt.Errorf("init databases: config is nil")
	}
	if err := cfg.Storage.ValidatePrimaryDBPath(); err != nil {
		return nil, fmt.Errorf("init databases: %w", err)
	}

	// The unified data-layer contract permits exactly one operational
	// primary SQLite file. Reject the former split jobs configuration
	// before opening any database handle.
	if cfg.Jobs.SplitDBEnabled || strings.TrimSpace(cfg.Jobs.JobsDBPath) != "" {
		return nil, fmt.Errorf("init databases: split jobs SQLite is disabled; use the canonical primary media/media.db.sqlite")
	}

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
	dualPool, dErr := storage.NewDualPool(ctx, setCfg.PrimaryDBPath, runtime.NumCPU())
	if dErr != nil {
		dbs.Close()
		return nil, fmt.Errorf("init databases: NewDualPool: %w", dErr)
	}
	dbs.DualPool = dualPool
	log.Info("Cut 6.2 dualPool wired (WAL-mode; writer=1, readers=NumCPU)",
		zap.Int("num_readers", runtime.NumCPU()),
		zap.String("primary_path", setCfg.PrimaryDBPath),
	)

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

func RunAllMigrations(dbs *Databases, log *zap.Logger) error {
	if err := dbs.Set.Migrate(log); err != nil {
		return err
	}
	if err := dbs.ValidateControlPlaneIdentity(context.Background()); err != nil {
		return fmt.Errorf("validate control plane identity: %w", err)
	}
	return nil
}
