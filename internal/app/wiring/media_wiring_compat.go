package wiring

// This file keeps the composition-root package's public test seam aligned
// with the dedicated media wiring package. The implementation remains owned
// by internal/app/wiring/media; these thin forwards avoid a second policy.

import (
	"context"
	"database/sql"

	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func SelectMediaAssetCommitter(cfg *config.Config, sqliteCommitter persistence.AssetCommitter, postgresCommitter persistence.AssetCommitter) (persistence.AssetCommitter, error) {
	return mediasub.SelectMediaAssetCommitter(cfg, sqliteCommitter, postgresCommitter)
}

func RequireMediaPostgres(ctx context.Context, cfg *config.Config) (*sql.DB, error) {
	return mediasub.RequireMediaPostgres(ctx, cfg)
}
