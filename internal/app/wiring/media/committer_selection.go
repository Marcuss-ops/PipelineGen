package media

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// SelectMediaAssetCommitter enforces the media storage policy at the
// composition root. PostgreSQL mode is never silently downgraded to SQLite.
// The PostgreSQL adapter is supplied by the caller once its connection and
// schema have been initialized.
func SelectMediaAssetCommitter(cfg *config.Config, sqliteCommitter persistence.AssetCommitter, postgresCommitter persistence.AssetCommitter) (persistence.AssetCommitter, error) {
	if cfg == nil {
		return nil, fmt.Errorf("media committer selection: configuration is nil")
	}
	if cfg.MediaPostgreSQL.Enabled {
		if postgresCommitter == nil {
			return nil, fmt.Errorf("media committer selection: PostgreSQL media SSOT is enabled but PostgresAssetCommitter is not wired")
		}
		return postgresCommitter, nil
	}
	if sqliteCommitter == nil {
		return nil, fmt.Errorf("media committer selection: SQLite compatibility committer is unavailable and PostgreSQL media SSOT is disabled")
	}
	return sqliteCommitter, nil
}
