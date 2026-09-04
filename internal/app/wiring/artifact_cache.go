package wiring

import (
	"database/sql"

	assetswiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	platformcache "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/artifactcache"
	"go.uber.org/zap"
)

func NewArtifactCache(cfg *config.Config, db *sql.DB, log *zap.Logger) (*platformcache.Cache, error) {
	return assetswiring.NewArtifactCache(cfg, db, log)
}
