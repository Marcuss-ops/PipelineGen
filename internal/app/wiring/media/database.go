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
// It never falls back to SQLite or Qdrant. Callers should only invoke this
// when cfg.MediaPostgreSQL.Enabled is true.
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

// RequireMediaPostgres makes the composition decision explicit. Disabled
// PostgreSQL returns (nil, nil) so current migration-mode callers can remain
// on SQLite; enabled PostgreSQL without a working backend returns an error.
func RequireMediaPostgres(ctx context.Context, cfg *config.Config) (*sql.DB, error) {
	if cfg == nil || !cfg.MediaPostgreSQL.Enabled {
		return nil, nil
	}
	return OpenMediaPostgres(ctx, cfg)
}
