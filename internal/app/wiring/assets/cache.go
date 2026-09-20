package assets

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	artifacts "github.com/Marcuss-ops/PipelineGen/internal/platform/artifactstaging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	platformcache "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/artifactcache"
	regsql "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
)

// NewArtifactCache constructs the shared derived-artifact cache used by media
// pipelines. The cache mappings/metrics live in cacheDB; the immutable CAS
// content registry is canonical control-plane state and therefore lives in
// contentDB (the primary media database).
func NewArtifactCache(cfg *config.Config, cacheDB, contentDB *sql.DB, log *zap.Logger) (*platformcache.Cache, error) {
	if cfg == nil || cacheDB == nil || contentDB == nil {
		return nil, fmt.Errorf("artifact cache wiring: cfg, cacheDB, and contentDB are required")
	}
	if log == nil {
		return nil, fmt.Errorf("artifact cache wiring: log is required")
	}
	root := filepath.Join(cfg.Storage.AbsDataDir(), "cas")
	stager, err := artifacts.NewLocalStore(artifacts.Config{Workspace: filepath.Join(root, ".staging")})
	if err != nil {
		return nil, fmt.Errorf("artifact cache wiring: stager: %w", err)
	}
	store, err := cas.NewStore(cas.Config{Root: root, Stager: stager})
	if err != nil {
		return nil, fmt.Errorf("artifact cache wiring: cas: %w", err)
	}
	content, err := regsql.NewContentObjectStore(contentDB)
	if err != nil {
		return nil, fmt.Errorf("artifact cache wiring: content registry: %w", err)
	}
	return platformcache.New(cacheDB, store, content)
}
