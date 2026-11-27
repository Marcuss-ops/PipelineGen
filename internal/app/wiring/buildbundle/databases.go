package buildbundle

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"go.uber.org/zap"
)

type Databases struct {
	Set      *storage.DatabaseSet
	Main     *storage.SQLiteDB
	Logs     *storage.SQLiteDB
	Cache    *storage.SQLiteDB
	DualPool *storage.DualPool

	// Jobs is the execution-plane handle when the canonical split is enabled;
	// it remains nil only for explicit rollback/staged deployments.
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

	// DB-08: read-only health gate in warning mode. Asynchronous by design so
	// a large primary DB cannot delay startup; findings go to the log and to
	// the db_health_findings metric.
	runBootDoctor(log, dbs)

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
