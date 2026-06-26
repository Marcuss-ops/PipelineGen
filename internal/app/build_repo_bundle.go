package app

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	idemsqlite "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/idempotency"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
)

// BuildRepoBundle constructs the canonical Repositories.
//
// PR8 (June 2026): added IdempotencyStore — the canonical port backing the
// reusable Gin idempotency middleware (internal/api/middleware/idempotency.go).
// All write handlers route replay requests through this store; a single
// repository instance is shared across the application so concurrent writes
// share an in_flight mutex-via-PRIMARY-KEY.
func BuildRepoBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger) (*RepoBundle, error) {
	_ = ctx
	_ = cfg
	assetsStore := asset.NewAssetStoreSQLite(dbs.main.DB, log)
	assetsSvc := asset.NewService(assetsStore, log)
	imageRepo := assets.NewImagesRepository(dbs.main.DB)
	voiceoverRepo := assets.NewVoiceoversRepository(dbs.main.DB)
	monitorsRepo := assets.NewMonitorsRepository(dbs.main.DB)
	clipsRepo := assets.NewClipsRepositoryCanonical(dbs.main.DB, log, assetsSvc.Repository())
	catalogRepo := catalog.NewRepository(clipsRepo, clipsRepo, clipsRepo)
	scriptsRepo := sqlitescripts.NewScriptRepository(dbs.main.DB)
	sqRepo := assets.NewSearchQueriesRepository(dbs.main.DB)

	// PR8: idempotency store (compiles cleanly against the port).
	var idempotencyStore middleware.IdempotencyStore = idemsqlite.NewSQLiteRepository(dbs.main.DB)

	return &RepoBundle{
		ScriptsRepo:      scriptsRepo,
		ImageRepo:        imageRepo,
		VoiceoverRepo:    voiceoverRepo,
		MonitorsRepo:     monitorsRepo,
		ClipsRepo:        clipsRepo,
		Assets:           assetsSvc,
		CatalogRepo:      catalogRepo,
		SQRepo:           sqRepo,
		IdempotencyStore: idempotencyStore,
	}, nil
}
