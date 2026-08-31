package cli

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"go.uber.org/zap"
)

// OpenDatabaseSet opens the canonical admin database topology: one primary
// control-plane database plus the separate observability database. Callers
// must use set.Primary and set.Observability and close the returned set; they
// must not reopen either path with OpenSQLiteDB.
func OpenDatabaseSet(cfg *config.Config, log *zap.Logger) (*storage.DatabaseSet, error) {
	if cfg == nil {
		return nil, fmt.Errorf("open database set: config is nil")
	}
	set, err := storage.OpenSet(storage.StorageConfig{
		DataDir:             cfg.Storage.DataDir,
		ObservabilityDBPath: cfg.Storage.ObservabilityDBFullPath(),
		WorkspaceDir:        cfg.Storage.WorkspaceDir,
		CacheDir:            cfg.Storage.CacheDir,
		ExportDir:           cfg.Storage.ExportDir,
	}, log)
	if err != nil {
		return nil, fmt.Errorf("open database set: %w", err)
	}
	return set, nil
}
