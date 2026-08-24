// Package sqlite — artlist_search_cache_adapter.go: typed-port
// adapter for the persistent Artlist live-search cache (PR-P0-3,
// July 2026).
//
// godlike/06 SSOT (one owner per fact): this adapter is the SOLE
// canonical writer of artlist_search_cache rows in production. No
// other code path may insert or update that table.
//
// godlike/06 SSOT discipline: the pattern mirrors the sibling
// artlist_runs_repository.go and outboxevents/repository.go files
// inside this package — open the canonical *sql.DB pool, expose a
// constructor that RETURNS the typed-port interface (never the
// concrete struct), keep every SQL surface inside the adapter
// file. The application layer consumes ONLY the typed port.
//
// godlike/07 NO-FAKE-AVAILABILITY: every Set / CleanExpired
// surface returns its transport error verbatim — silent-degrade
// is forbidden (the cache WRITE path is observable truth; a
// failed Set MUST be visible to operators).
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
)

// SQLiteArtlistSearchCacheAdapter is the concrete implementation
// of artlist.ArtlistSearchCachePort backed by the canonical
// artlist_search_cache table.
type SQLiteArtlistSearchCacheAdapter struct {
	db  *sql.DB
	log *zap.Logger
}

// NewSQLiteArtlistSearchCacheAdapter constructs the adapter.
// db MUST be non-nil (composition-time fail-fast in the caller);
// log is nil-safe (zap.NewNop() fallback). Returns the typed port
// so callers in the application layer cannot accidentally couple
// to the concrete struct.
func NewSQLiteArtlistSearchCacheAdapter(db *sql.DB, log *zap.Logger) artlist.ArtlistSearchCachePort {
	if log == nil {
		log = zap.NewNop()
	}
	return &SQLiteArtlistSearchCacheAdapter{db: db, log: log}
}

// Compile-time assertion (godlike/06 SSOT) — the concrete
// satisfies the port. Drift surfaces as a build failure here
// rather than a runtime nil-port panic on first dispatch.
var _ artlist.ArtlistSearchCachePort = (*SQLiteArtlistSearchCacheAdapter)(nil)

// MaxCacheAgeHardLimit is the canonical 48h ceiling applied by
// every read + cleanup path. Mirrors the legacy search_cache.go
// constant; centralised here so future cache-policy edits have
// one canonical source.
const MaxCacheAgeHardLimit = 48 * time.Hour

// Warm bulk-loads recent (<48h) entries. Mirrors the legacy
// warmFromDB logic in search_cache.go verbatim, then returns
// the typed CachedEntry slice the caller promotes into the
// in-memory L1 map.
func (a *SQLiteArtlistSearchCacheAdapter) Warm(ctx context.Context) ([]artlist.CachedEntry, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT term, clips_json, cached_at FROM artlist_search_cache`)
	if err != nil {
		return nil, fmt.Errorf("artlist_search_cache.Warm: query: %w", err)
	}
	defer rows.Close()

	now := time.Now()
	var out []artlist.CachedEntry
	for rows.Next() {
		var term, clipsJSON, cachedAtStr string
		if scanErr := rows.Scan(&term, &clipsJSON, &cachedAtStr); scanErr != nil {
			continue
		}
		cachedAt, perr := time.Parse("2006-01-02 15:04:05", cachedAtStr)
		if perr != nil {
			continue
		}
		if now.Sub(cachedAt) > MaxCacheAgeHardLimit {
			continue
		}
		var clips []artlist.Candidate
		if uerr := json.Unmarshal([]byte(clipsJSON), &clips); uerr != nil {
			continue
		}
		out = append(out, artlist.CachedEntry{Term: term, Clips: clips, CachedAt: cachedAt})
	}
	if rerr := rows.Err(); rerr != nil {
		return out, fmt.Errorf("artlist_search_cache.Warm: rows: %w", rerr)
	}
	return out, nil
}

// Get reads one entry. Expired entries (>48h) are deleted
// in-line and returned as a miss (false, nil, no error) —
// callers branch on the boolean; transport failures are
// surfaced via the error path.
func (a *SQLiteArtlistSearchCacheAdapter) Get(ctx context.Context, term string) ([]artlist.Candidate, time.Time, bool, error) {
	var clipsJSON, cachedAtStr string
	err := a.db.QueryRowContext(ctx,
		`SELECT clips_json, cached_at FROM artlist_search_cache WHERE term = ?`,
		term,
	).Scan(&clipsJSON, &cachedAtStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, time.Time{}, false, nil
		}
		return nil, time.Time{}, false, fmt.Errorf("artlist_search_cache.Get: query: %w", err)
	}
	cachedAt, perr := time.Parse("2006-01-02 15:04:05", cachedAtStr)
	if perr != nil {
		return nil, time.Time{}, false, nil
	}
	if time.Since(cachedAt) > MaxCacheAgeHardLimit {
		_, _ = a.db.ExecContext(ctx, `DELETE FROM artlist_search_cache WHERE term = ?`, term)
		return nil, time.Time{}, false, nil
	}
	var clips []artlist.Candidate
	if uerr := json.Unmarshal([]byte(clipsJSON), &clips); uerr != nil {
		return nil, time.Time{}, false, nil
	}
	return clips, cachedAt, true, nil
}

// Set upserts an entry keyed by term (semantics preserved
// verbatim from the legacy persistToDB path in search_cache.go).
// ON CONFLICT(term) DO UPDATE keeps the schema's UNIQUE-term
// invariant intact under concurrent writers.
func (a *SQLiteArtlistSearchCacheAdapter) Set(ctx context.Context, term string, clips []artlist.Candidate) error {
	data, err := json.Marshal(clips)
	if err != nil {
		return fmt.Errorf("artlist_search_cache.Set: marshal: %w", err)
	}
	_, err = a.db.ExecContext(ctx,
		`INSERT INTO artlist_search_cache (term, clips_json, cached_at) VALUES (?, ?, datetime('now'))
         ON CONFLICT(term) DO UPDATE SET clips_json = excluded.clips_json, cached_at = excluded.cached_at`,
		term, string(data),
	)
	if err != nil {
		return fmt.Errorf("artlist_search_cache.Set: exec: %w", err)
	}
	return nil
}

// Delete removes one entry by term. Errors are surfaced to the
// caller; tombstoning is best-effort so a transient DB blip
// doesn't poison the L1 cache (the L1 is the source of truth).
func (a *SQLiteArtlistSearchCacheAdapter) Delete(ctx context.Context, term string) error {
	_, err := a.db.ExecContext(ctx, `DELETE FROM artlist_search_cache WHERE term = ?`, term)
	if err != nil {
		return fmt.Errorf("artlist_search_cache.Delete: exec: %w", err)
	}
	return nil
}

// CleanupExpired removes entries older than the 48h hard-limit.
// Mirrors the legacy Cleanup(ttl) SQL fragment.
func (a *SQLiteArtlistSearchCacheAdapter) CleanupExpired(ctx context.Context, _ time.Duration) error {
	expiryHours := int(MaxCacheAgeHardLimit / time.Hour)
	if a.log != nil {
		a.log.Debug("cleaning up expired search cache entries",
			zap.Int("expiry_hours", expiryHours),
		)
	}
	_, err := a.db.ExecContext(ctx,
		`DELETE FROM artlist_search_cache WHERE cached_at < datetime('now', '-' || ? || ' hours')`,
		expiryHours,
	)
	if err != nil {
		return fmt.Errorf("artlist_search_cache.CleanupExpired: exec: %w", err)
	}
	return nil
}
