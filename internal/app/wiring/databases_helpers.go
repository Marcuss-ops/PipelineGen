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
// FASE 6 Cut 6.2 compatibility: the `dualPool *sqlite.DualPool` field
// remains as a reader/writer-shaped adapter for bundle constructors. It
// is attached to the already-open DatabaseSet.Primary handle; it does not
// open a second pool or a second operational connection set. The canonical
// ownership remains DatabaseSet.Primary, while DatabaseSet.Observability is
// the only separate database.
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
// A second operational SQLite database is intentionally not supported;
// the unified runtime uses only Main for business state and Logs for the
// separate observability axis.
//
// Compatibility field population rules (godlike/06 SSOT):
//   - dbs.Set: always non-nil after a successful OpenSet.
//   - dbs.Main: dbs.Set.Primary.
//   - dbs.Logs: dbs.Set.Observability.
//   - dbs.DualPool: an attached adapter over dbs.Set.Primary; it never
//     opens or owns another SQLite handle.
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
	// Close the compatibility adapter before DatabaseSet. Attached adapters do
	// not own the primary handle; DatabaseSet remains the sole owner.
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
// Bundle constructors may still consume the compatibility DualPool shape,
// but it is attached to the DatabaseSet primary below. No alternative
// primary opening is allowed here.
//
// The retired jobs.db.sqlite split is rejected fail-closed; no second
// operational database is opened.
func InitDatabases(ctx context.Context, cfg *config.Config, log *zap.Logger) (*Databases, error) {
	if cfg == nil {
		return nil, fmt.Errorf("init databases: config is nil")
	}
	// The unified data-layer contract permits exactly one operational
	// primary SQLite file. Reject the former split jobs configuration
	// before opening any database handle.
	if cfg.Jobs.SplitDBEnabled || strings.TrimSpace(cfg.Jobs.JobsDBPath) != "" {
		return nil, fmt.Errorf("init databases: split jobs SQLite is disabled; use the canonical primary media/media.db.sqlite")
	}

	setCfg := storage.StorageConfig{
		DataDir:             cfg.Storage.DataDir,
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

	// Transitional bundle constructors still consume the reader/writer-shaped
	// field. Attach it to the already-open canonical primary handle; this
	// performs no second sql.Open and leaves DatabaseSet as sole owner.
	dualPool, err := storage.AttachDualPool(set.Primary)
	if err != nil {
		dbs.Close()
		return nil, fmt.Errorf("init databases: attach canonical primary: %w", err)
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
