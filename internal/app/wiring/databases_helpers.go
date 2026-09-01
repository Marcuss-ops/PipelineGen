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
//	// FASE 6 Cut 6.2: DualPool owns an independent writer handle and reader
//
// pool over the same canonical media database file. The DatabaseSet primary
// remains available to legacy callers, while all bundle DB access uses the
// true reader/writer split.
package wiring

import (
	"context"
	"fmt"
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
// Jobs is a separate execution-plane database when the canonical split is
// enabled; Main remains the media control-plane database and Logs remains the
// observability axis.
//
// Compatibility field population rules (godlike/06 SSOT):
//   - dbs.Set: always non-nil after a successful OpenSet.
//   - dbs.Main: dbs.Set.Primary.
//   - dbs.Logs: dbs.Set.Observability.
//   - dbs.DualPool: a true reader/writer pool over the canonical primary file.
//   - dbs.Jobs: non-nil only when the explicit jobs split is enabled.
type Databases struct {
	Set      *storage.DatabaseSet
	Main     *storage.SQLiteDB
	Logs     *storage.SQLiteDB
	Cache    *storage.SQLiteDB
	DualPool *storage.DualPool

	// Jobs is retained as a nil-only compatibility field while callers
	// migrate away from the retired split-database shape.
	Jobs *storage.SQLiteDB
}

func (d *Databases) Close() {
	// Close the compatibility adapter before DatabaseSet. Attached adapters do
	// not own the primary handle; DatabaseSet remains the sole owner.
	if d.DualPool != nil {
		_ = d.DualPool.Close()
	}
	if d.Set != nil {
		_ = d.Set.Close()
	}
}

// InitDatabases opens the primary + observability DBs and, when enabled, the
// jobs + cache planes via the
// canonical `storage.OpenSet` (codex/db-set-and-paths). No `sql.Open`
// remains outside `internal/platform/sqlite/**`.
//
// Bundle constructors may still consume the compatibility DualPool shape,
// but it is attached to the DatabaseSet primary below. No alternative
// primary opening is allowed here.
//
// The jobs.db.sqlite split is opt-in and opens an independent execution-plane
// database. It is never used for media transactions or cross-DB atomicity.
func InitDatabases(ctx context.Context, cfg *config.Config, log *zap.Logger) (*Databases, error) {
	if cfg == nil {
		return nil, fmt.Errorf("init databases: config is nil")
	}
	if strings.TrimSpace(cfg.Jobs.JobsDBPath) != "" && !cfg.Jobs.SplitDBEnabled {
		return nil, fmt.Errorf("init databases: jobs_db_path requires jobs.split_db_enabled=true")
	}

	setCfg := storage.StorageConfig{
		DataDir:             cfg.Storage.DataDir,
		ObservabilityDBPath: cfg.Storage.ObservabilityDBFullPath(),
		CacheDBPath:         cfg.Storage.CacheDBFullPath(),
		WorkspaceDir:        cfg.Storage.WorkspaceDir,
		CacheDir:            cfg.Storage.CacheDir,
		ExportDir:           cfg.Storage.ExportDir,
	}
	if cfg.Jobs.SplitDBEnabled {
		setCfg.JobsDBPath = cfg.Storage.JobsDBFullPath()
		if strings.TrimSpace(cfg.Jobs.JobsDBPath) != "" {
			setCfg.JobsDBPath = cfg.Jobs.JobsDBPath
		}
	}
	set, err := storage.OpenSet(setCfg, log)
	if err != nil {
		return nil, fmt.Errorf("init databases: %w", err)
	}
	dbs := &Databases{
		Set:   set,
		Main:  set.Primary,
		Logs:  set.Observability,
		Cache: set.Cache,
		Jobs:  set.Jobs,
	}

	// Transitional bundle constructors consume the reader/writer-shaped field.
	// Open a real reader pool over the same media file; unlike AttachDualPool,
	// this prevents reads from queueing behind the primary's single connection.
	dualPool, err := storage.NewDualPool(ctx, set.Primary.Path(), cfg.Storage.SQLiteReaderCount())
	if err != nil {
		dbs.Close()
		return nil, fmt.Errorf("init databases: open media dual pool: %w", err)
	}
	dbs.DualPool = dualPool

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
			Role:         storage.ControlPlaneRoleReadOnly,
			Writable:     true,
			ControlPlane: false,
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
