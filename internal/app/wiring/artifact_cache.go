package wiring

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	platformcache "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/artifactcache"
	regsql "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
)

// NewArtifactCache constructs the single shared derived-artifact cache used
// by Whisper and media processing. The CAS is immutable; SQLite stores only
// deterministic key -> content-address mappings and durable metrics.
func NewArtifactCache(cfg *config.Config, db *sql.DB, log *zap.Logger) (*platformcache.Cache, error) {
	if cfg == nil || db == nil {
		return nil, fmt.Errorf("artifact cache wiring: cfg and db are required")
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
	content, err := regsql.NewContentObjectStore(db)
	if err != nil {
		return nil, fmt.Errorf("artifact cache wiring: content registry: %w", err)
	}
	return platformcache.New(db, store, content)
}
