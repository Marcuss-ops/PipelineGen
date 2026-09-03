package media

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// OpenMediaPostgres opens and validates the PostgreSQL media SSOT database.
// It never falls back to SQLite or Qdrant for media writes.
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

// RequireMediaPostgres makes the composition decision explicit and is the
// ONLY engine decision point for the media plane. Enabled PostgreSQL without
// a working backend returns an error (fail-closed: a half-open media SSOT
// must never boot).
//
// GRACEFUL DEGRADE (media demolition, September 2026): when the media
// PostgreSQL is enabled but no DSN is configured the media plane is treated
// as NOT DEPLOYED — (nil, nil) is returned and the composition skips every
// media-dependent wiring site. A nil handle can never route media writes to
// SQLite: the SQLite media engine no longer exists. Deployments that want
// media MUST set PIPELINEGEN_MEDIA_POSTGRES_DSN.
func RequireMediaPostgres(ctx context.Context, cfg *config.Config) (*sql.DB, error) {
	if cfg == nil || !cfg.MediaPostgreSQL.Enabled {
		return nil, nil
	}
	if strings.TrimSpace(cfg.MediaPostgreSQL.DSN) == "" {
		return nil, nil
	}
	return OpenMediaPostgres(ctx, cfg)
}
