package cli

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"go.uber.org/zap"
)

// OpenDatabaseSet opens the canonical admin database topology: the primary
// control-plane database, the separate observability database, the rebuildable
// cache plane, and the execution plane when the jobs split is enabled. Callers
// must use set.Primary/set.Observability/set.Cache/set.Jobs and close the
// returned set; they must not reopen any of those paths with OpenSQLiteDB.
//
// The topology must mirror InitDatabases (internal/app/wiring) exactly, which
// is why the cache and jobs paths are threaded here. A set that silently omits
// the execution plane makes every job_id-keyed maintenance command fail: with
// jobs.split_db_enabled=true the `jobs` table lives ONLY in
// <DataDir>/jobs/jobs.db.sqlite, so `admin reconcile-orphaned-runs` died with
// "no such table: jobs" against the media DB and could not finalize a single
// orphaned run.
func OpenDatabaseSet(cfg *config.Config, log *zap.Logger) (*storage.DatabaseSet, error) {
	if cfg == nil {
		return nil, fmt.Errorf("open database set: config is nil")
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
		return nil, fmt.Errorf("open database set: %w", err)
	}
	return set, nil
}
