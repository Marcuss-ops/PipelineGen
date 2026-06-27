package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	common "github.com/Marcuss-ops/PipelineGen/internal/api/common"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	idemsqlite "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/idempotency"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	infrahealth "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/health"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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

// BuildSearchBundle builds the asset metadata search index + tree + resolver.
func BuildSearchBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle) (*SearchBundle, error) {
	_ = ctx
	_ = cfg
	assetIndexRepo := assetindex.NewRepository(dbs.main.DB)
	assetIndexService := assetindex.NewService(assetIndexRepo)

	assetTreeRepo, err := assets.NewAssetTreeRepository(dbs.main.DB, log)
	if err != nil {
		return nil, fmt.Errorf("init asset tree repository: %w", err)
	}
	assetTreeService := assettree.NewService(assetTreeRepo, log)
	if assetTreeService == nil {
		return nil, fmt.Errorf("assettree service is nil after construction")
	}

	clipsRepos := map[string]*assets.ClipsRepository{
		"youtube": repos.ClipsRepo,
		"stock":   repos.ClipsRepo,
		"artlist": repos.ClipsRepo,
	}
	resolverCfg := &assetindex.ResolverConfig{
		ClipsRepos:    clipsRepos,
		ImageRepo:     repos.ImageRepo,
		VoiceoverRepo: repos.VoiceoverRepo,
	}
	assetResolver := assetindex.NewResolver(assetIndexService, resolverCfg, log)

	return &SearchBundle{
		AssetIndexService: assetIndexService,
		AssetTreeService:  assetTreeService,
		AssetResolver:     assetResolver,
		ProviderRegistry:  providers.NewRegistry(),
	}, nil
}

// BuildUtilityBundle constructs the lightweight utility handlers
// and the health-check Service (PR1 Health boundary, June 2026).
func BuildUtilityBundle(cfg *config.Config, db *storage.SQLiteDB) *UtilityBundle {
	svc := buildHealthService(cfg, db)
	return &UtilityBundle{
		Utility:       common.NewUtilityHandler(),
		HealthService: svc,
		ReadyChecker:  systemhealth.NewReadyChecker(svc),
	}
}

// buildHealthService constructs the health.Service from infrastructure
// checkers. Lives here because it's the only place that wires concrete
// adapters (PR1 Health boundary, June 2026).
//
// PG-011 typed-handle migration (June 2026): the previous
// implementation unwrapped `db *storage.SQLiteDB` to `*sql.DB` via
// `var sqlDB *sql.DB; if db != nil { sqlDB = db.DB }` so it could hand
// a raw handle to infrahealth.NewSQLiteChecker / NewJobsChecker. The
// checkers now accept *storage.SQLiteDB directly (the underlying
// *sql.DB is reached via the embedded field), which removes the
// `database/sql` import from this file. The `db` arg may itself be
// nil — infrahealth.Checker constructors accept nil and the zero
// value remains safe.
func buildHealthService(cfg *config.Config, db *storage.SQLiteDB) *systemhealth.Service {
	if cfg == nil {
		return nil
	}

	var driveChecker systemhealth.DriveChecker
	credsPath := cfg.GetCredentialsPath()
	tokenPath := cfg.GetTokenPath()
	if credsPath != "" && tokenPath != "" {
		driveChecker = infrahealth.NewDriveChecker(credsPath, tokenPath)
	}

	// QDRANT-005 (June 2026): QdrantChecker wired back. When qdrant.enabled=true,
	// /health?check=qdrant probes the Qdrant /collections endpoint. When disabled,
	// the checker returns {ok:true, applicable:false} so the health endpoint
	// correctly reports "not applicable" rather than "unknown check".
	var qdrantChecker systemhealth.QdrantChecker
	if cfg.Qdrant.Enabled {
		qdrantChecker = infrahealth.NewQdrantChecker(cfg.Qdrant.BaseURL, "", true)
	}

	return systemhealth.NewService(systemhealth.ServiceDeps{
		DB:     infrahealth.NewSQLiteChecker(db),
		Drive:  driveChecker,
		Qdrant: qdrantChecker,
		Jobs:   infrahealth.NewJobsChecker(db),
	})
}
