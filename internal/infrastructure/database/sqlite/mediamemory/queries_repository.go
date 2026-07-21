// Package mediamemory (sqlite infrastructure) — queries_repository.go
// is the canonical concrete impl of mediamemory.QueryCacheRepository.
//
// godlike/06 SSOT: the SQL ↔ Go row conversion for media_query_cache
// lives ONLY here. DDL canonical home:
// migrations/sqlite/165_mediamemory_query_cache.sql. Wire canonical
// home: internal/application/mediamemory/ports.go::QueryCacheEntry.
// This file is the bridge.
//
// godlike/07 NO-FAKE-AVAILABILITY: hit_count is incremented via
// ON CONFLICT DO UPDATE (atomic, no read-then-write race). The
// expiration sweep runs at the composition root (TTL pass deletes
// expired rows; no fake availability on stale entries).
//
// godlike/06 SSOT (separation from script cache): this cache is
// DISTINCT from the script-generation fingerprint cache per
// architecture doc section 13. Two caches, two SSOTs, different
// fingerprints; new dependents import THIS port, never the
// script-generation cache.
package mediamemory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
)

// queriesRepository is the canonical concrete
// mediamemory.QueryCacheRepository backed by SQLite.
type queriesRepository struct {
	db *sql.DB
}

// NewQueriesRepository constructs the canonical repository.
func NewQueriesRepository(db *sql.DB) mediamemory.QueryCacheRepository {
	return &queriesRepository{db: db}
}

// Compile-time assertion.
var _ mediamemory.QueryCacheRepository = (*queriesRepository)(nil)

const queriesSelectColumns = `id, phrase_fingerprint, language,
		request_json, result_json, provider_state_json,
		hit_count, expires_at, created_at, updated_at`

// Get returns (entry, true, nil) on a cached hit; (zero, false,
// nil) on a miss; (zero, false, ErrXxx) only on real IO failures
// (no fake availability).
func (r *queriesRepository) Get(ctx context.Context, fingerprint string) (mediamemory.QueryCacheEntry, bool, error) {
	q := `SELECT ` + queriesSelectColumns + `
		FROM media_query_cache
		WHERE phrase_fingerprint = ?
		ORDER BY updated_at DESC
		LIMIT 1`
	row := r.db.QueryRowContext(ctx, q, fingerprint)
	e, err := scanQueryCacheRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return mediamemory.QueryCacheEntry{}, false, nil
		}
		return mediamemory.QueryCacheEntry{}, false, fmt.Errorf("mediamemory: query cache get %q: %w", fingerprint, err)
	}
	// godlike/07 NO-FAKE-AVAILABILITY: an expired entry is treated
	// as a miss (consumers see the same about-to-be-replaced
	// behaviour as a cache miss + a deferred Invalidate).
	if e.ExpiresAt != nil && e.ExpiresAt.Before(time.Now().UTC()) {
		// Best-effort delete; cache expiry is a soft signal.
		_, _ = r.db.ExecContext(ctx, `DELETE FROM media_query_cache WHERE id = ?`, e.ID)
		return mediamemory.QueryCacheEntry{}, false, nil
	}
	// Atomic hit_count increment (no read-then-write race).
	_, _ = r.db.ExecContext(ctx,
		`UPDATE media_query_cache SET hit_count = hit_count + 1, updated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), e.ID,
	)
	e.HitCount++
	return e, true, nil
}

// Put inserts a new cache row or updates the existing one keyed
// by phrase_fingerprint + language. The new row resets hit_count
// to 0 (a fresh put is "first write wins").
func (r *queriesRepository) Put(ctx context.Context, entry mediamemory.QueryCacheEntry) error {
	now := time.Now().UTC()
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	entry.UpdatedAt = now

	q := `INSERT INTO media_query_cache
		(` + queriesSelectColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(phrase_fingerprint) DO UPDATE SET
			language            = excluded.language,
			request_json        = excluded.request_json,
			result_json         = excluded.result_json,
			provider_state_json = excluded.provider_state_json,
			expires_at          = excluded.expires_at,
			updated_at          = excluded.updated_at
		RETURNING ` + queriesSelectColumns

	row := r.db.QueryRowContext(ctx, q,
		entry.ID, entry.PhraseFingerprint, entry.Language,
		entry.RequestJSON, entry.ResultJSON, nullableString(entry.ProviderStateJSON),
		entry.HitCount, nullableTimePtr(entry.ExpiresAt),
		entry.CreatedAt.Format(time.RFC3339Nano), entry.UpdatedAt.Format(time.RFC3339Nano),
	)
	if _, err := scanQueryCacheRow(row); err != nil {
		return fmt.Errorf("mediamemory: query cache put: %w", err)
	}
	return nil
}

// Invalidate removes every row keyed by the fingerprint (a
// fingerprint is the SSOT identity for a cached phrase).
func (r *queriesRepository) Invalidate(ctx context.Context, fingerprint string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM media_query_cache WHERE phrase_fingerprint = ?`, fingerprint)
	if err != nil {
		return fmt.Errorf("mediamemory: query cache invalidate %q: %w", fingerprint, err)
	}
	return nil
} // ── Row scanning (helper) ──────────────────────────────────

func scanQueryCacheRow(s rowScanner) (mediamemory.QueryCacheEntry, error) {
	var (
		e             mediamemory.QueryCacheEntry
		providerState sql.NullString
		expiresAt     sql.NullString
		createdAt     string
		updatedAt     string
	)
	if err := s.Scan(
		&e.ID, &e.PhraseFingerprint, &e.Language,
		&e.RequestJSON, &e.ResultJSON, &providerState,
		&e.HitCount, &expiresAt,
		&createdAt, &updatedAt,
	); err != nil {
		return mediamemory.QueryCacheEntry{}, err
	}
	if providerState.Valid {
		e.ProviderStateJSON = providerState.String
	}
	if expiresAt.Valid {
		t, err := parseTime(expiresAt.String)
		if err != nil {
			return mediamemory.QueryCacheEntry{}, fmt.Errorf("mediamemory: query cache entry %q expires_at: %w", e.ID, err)
		}
		e.ExpiresAt = &t
	}
	var err error
	if e.CreatedAt, err = parseTime(createdAt); err != nil {
		return mediamemory.QueryCacheEntry{}, fmt.Errorf("mediamemory: query cache entry %q created_at: %w", e.ID, err)
	}
	if e.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return mediamemory.QueryCacheEntry{}, fmt.Errorf("mediamemory: query cache entry %q updated_at: %w", e.ID, err)
	}
	return e, nil
}
