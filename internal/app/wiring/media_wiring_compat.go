package wiring

// This file keeps the composition-root package's public test seam aligned
// with the dedicated media wiring package. The implementation remains owned
// by internal/app/wiring/media; these thin forwards avoid a second policy.

import (
	"context"
	"database/sql"

	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// SelectMediaAssetCommitter was REMOVED (media cutover demolition,
// September 2026): it was never invoked by production wiring and encoded
// a caller-supplied adapter-pair decision that conflicted with the
// canonical single decision point in canonical_media_committer.go
// (newCanonicalAssetCommitterCfg / canonicalCommitterForRoot). The media
// storage policy lives there — cfg.MediaPostgreSQL.Enabled + the open
// root.MediaPostgres handle, fail-closed.

func RequireMediaPostgres(ctx context.Context, cfg *config.Config) (*sql.DB, error) {
	return mediasub.RequireMediaPostgres(ctx, cfg)
}
