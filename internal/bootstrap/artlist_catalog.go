package bootstrap

import (
	"context"
	"go.uber.org/zap"
	"velox/go-master/internal/service/clipcatalog"
	"velox/go-master/internal/service/clipindexer"
)

func wireArtlistCatalog(coreDeps *CoreDeps, log *zap.Logger) (*clipcatalog.Repository, *clipindexer.Service) {
	if coreDeps.ArtlistDB != nil && coreDeps.ArtlistDB.DB != nil {
		if err := clipcatalog.EnsureSchema(context.Background(), coreDeps.ArtlistDB.DB, log); err != nil {
			log.Warn("failed to ensure clipcatalog schema", zap.Error(err))
		}
	}

	clipCatalogRepo := clipcatalog.NewRepository(coreDeps.ArtlistDB.DB, log)
	clipIndexerSvc := clipindexer.NewService(nil, coreDeps.ArtlistDB.DB, coreDeps.ArtlistDB.Path(), log)

	return clipCatalogRepo, clipIndexerSvc
}
