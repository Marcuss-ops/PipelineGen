package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// CacheHealth reports cache availability without making it a business
// readiness dependency. Callers can continue with a miss when Available is
// false.
type CacheHealth struct {
	Available bool
	Error     error
}

func CheckCacheHealth(ctx context.Context, db *SQLiteDB) CacheHealth {
	if db == nil || db.DB == nil {
		return CacheHealth{Error: fmt.Errorf("cache database unavailable")}
	}
	var status string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&status); err != nil {
		return CacheHealth{Error: err}
	}
	if status != "ok" {
		return CacheHealth{Error: fmt.Errorf("cache quick_check returned %q", status)}
	}
	return CacheHealth{Available: true}
}

// SweepCache removes expired and stale rows. It only touches the cache DB;
// failure is returned to the caller for logging and cannot alter business DB.
func SweepCache(ctx context.Context, db *SQLiteDB, staleDays int) (int64, error) {
	if db == nil || db.DB == nil {
		return 0, fmt.Errorf("cache database unavailable")
	}
	if staleDays <= 0 {
		staleDays = 30
	}
	var total int64
	for _, query := range []string{
		"DELETE FROM research_cache WHERE expires_at IS NOT NULL AND expires_at <= datetime('now')",
		"DELETE FROM research_cache WHERE last_used < datetime('now', ?)",
		"DELETE FROM artlist_search_cache WHERE cached_at < datetime('now', ?)",
		"DELETE FROM transcript_cache WHERE cached_at < datetime('now', ?)",
		"DELETE FROM translation_cache WHERE last_used < datetime('now', ?)",
		"DELETE FROM vidrush_provider_cache WHERE updated_at < datetime('now', ?)",
		"DELETE FROM media_query_cache WHERE expires_at IS NOT NULL AND expires_at <= datetime('now')",
		"DELETE FROM artifact_cache_entries WHERE status IN ('FAILED','INVALID') AND updated_at < datetime('now', ?)",
	} {
		var result sql.Result
		var err error
		if query == "DELETE FROM research_cache WHERE expires_at IS NOT NULL AND expires_at <= datetime('now')" || query == "DELETE FROM media_query_cache WHERE expires_at IS NOT NULL AND expires_at <= datetime('now')" {
			result, err = db.ExecContext(ctx, query)
		} else {
			result, err = db.ExecContext(ctx, query, fmt.Sprintf("-%d days", staleDays))
		}
		if err != nil {
			return total, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}
