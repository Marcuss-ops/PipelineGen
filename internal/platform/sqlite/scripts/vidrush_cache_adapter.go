package scripts

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"go.uber.org/zap"
)

const vidRushCacheTTL = 48 * time.Hour

// SQLiteVidRushCacheAdapter is the durable L2 cache for VidRush provider
// discovery and materialization. It owns only the cache table; canonical
// media_assets, outbox and Qdrant state remain owned by their normal writers.
type SQLiteVidRushCacheAdapter struct {
	db  *sql.DB
	log *zap.Logger
}

func NewSQLiteVidRushCacheAdapter(db *sql.DB, log *zap.Logger) scriptports.VidRushCachePort {
	if log == nil {
		log = zap.NewNop()
	}
	return &SQLiteVidRushCacheAdapter{db: db, log: log}
}

var _ scriptports.VidRushCachePort = (*SQLiteVidRushCacheAdapter)(nil)

func (a *SQLiteVidRushCacheAdapter) Get(ctx context.Context, namespace, key string) ([]byte, bool, error) {
	if a == nil || a.db == nil {
		return nil, false, fmt.Errorf("vidrush cache: database unavailable")
	}
	var payload, updated string
	err := a.db.QueryRowContext(ctx, `
		SELECT payload_json, updated_at
		FROM vidrush_provider_cache
		WHERE namespace = ? AND cache_key = ?`, namespace, key).Scan(&payload, &updated)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("vidrush cache get: %w", err)
	}
	when, err := parseSQLiteTime(updated)
	if err != nil || time.Since(when) > vidRushCacheTTL {
		if _, deleteErr := a.db.ExecContext(ctx, `DELETE FROM vidrush_provider_cache WHERE namespace = ? AND cache_key = ?`, namespace, key); deleteErr != nil {
			return nil, false, fmt.Errorf("vidrush cache expire: %w", deleteErr)
		}
		return nil, false, nil
	}
	return []byte(payload), true, nil
}

func (a *SQLiteVidRushCacheAdapter) Put(ctx context.Context, namespace, key string, payload []byte) error {
	if a == nil || a.db == nil {
		return fmt.Errorf("vidrush cache: database unavailable")
	}
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(key) == "" || len(payload) == 0 {
		return fmt.Errorf("vidrush cache: namespace, key and payload are required")
	}
	_, err := a.db.ExecContext(ctx, `
		INSERT INTO vidrush_provider_cache(namespace, cache_key, payload_json, created_at, updated_at)
		VALUES (?, ?, ?, datetime('now'), datetime('now'))
		ON CONFLICT(namespace, cache_key) DO UPDATE SET
			payload_json = excluded.payload_json,
			updated_at = excluded.updated_at`, namespace, key, string(payload))
	if err != nil {
		return fmt.Errorf("vidrush cache put: %w", err)
	}
	return nil
}

func parseSQLiteTime(value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, time.RFC3339Nano} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid sqlite timestamp %q", value)
}
