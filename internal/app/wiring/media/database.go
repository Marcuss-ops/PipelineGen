package media

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// OpenMediaPostgres opens and validates the PostgreSQL media SSOT database.
// It never falls back to SQLite or Qdrant for media writes or reads.
func OpenMediaPostgres(ctx context.Context, cfg *config.Config) (*sql.DB, error) {
	if cfg == nil {
		return nil, fmt.Errorf("media PostgreSQL: configuration is nil")
	}
	if err := cfg.MediaPostgreSQL.Validate(); err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", cfg.MediaPostgreSQL.DSN)
	if err != nil {
		return nil, fmt.Errorf("media PostgreSQL: open: %w", err)
	}
	db.SetMaxOpenConns(cfg.MediaPostgreSQL.MaxOpenConnections)
	db.SetMaxIdleConns(cfg.MediaPostgreSQL.MaxIdleConnections)
	if cfg.MediaPostgreSQL.ConnMaxLifetimeSeconds > 0 {
		db.SetConnMaxLifetime(time.Duration(cfg.MediaPostgreSQL.ConnMaxLifetimeSeconds) * time.Second)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("media PostgreSQL: health check failed: %w", err)
	}
	return db, nil
}

// RequireMediaPostgres is the ONLY engine decision point for the media plane.
// Disabled means the media plane is intentionally not deployed. Enabled means
// PostgreSQL is mandatory: invalid configuration, an empty DSN, or an
// unreachable backend aborts composition. There is no SQLite/Qdrant fallback.
func RequireMediaPostgres(ctx context.Context, cfg *config.Config) (*sql.DB, error) {
	if cfg == nil || !cfg.MediaPostgreSQL.Enabled {
		return nil, nil
	}
	return OpenMediaPostgres(ctx, cfg)
}
