// render_cache.go owns the deterministic render cache for clip.render:
// fingerprint → certified artifact locator. The cache makes the
// cost-marginal-zero property concrete: a repeated POST with identical
// semantics returns in milliseconds (no download, no Chronon, no probe)
// by reusing the locator-first artifact the previous render already
// certified, staged and committed.
//
// Storage is PostgreSQL media path (a new table, not the legacy SQLite
// sidecar). No extra janitor beyond DB vacuum.
//
// STALENESS IS CHECKED, NOT ASSUMED. The comment above used to claim the
// cache "naturally misses" when the media row is deleted. It did not:
// clip_render_cache carries no foreign key to media_assets and Get() returned
// the row without reading the asset at all, so a deleted or overwritten asset
// still produced a HIT — and the batch handler trusts the record immediately
// (fingerprint -> asset_id -> status=CACHED), so the product would report a
// certified artifact that no longer exists. Get() therefore JOINs the media
// row and requires BOTH identity and content to agree:
//
//	cache row + asset exists + same content sha256 + ACTIVE/not-deleted -> HIT
//	cache row + asset missing                                          -> MISS
//	cache row + asset sha256 differs (overwritten)                     -> MISS
//	cache row + asset retired (DELETED / deleted_at set)               -> MISS
//
// A MISS is the fail-safe direction: the worker renders a fresh artifact
// instead of advertising a stale locator.
//
// Lookup key: fingerprint hex (64 chars, lower hex). Value is the
// certified locator and the committed asset id, so the worker can
// fabricate a valid completion without touching the GPU.
//
// SEGMENT IDENTITY — a recorded precondition, deliberately NOT implemented
// here. A segmented render (N jobs rendering disjoint frame ranges of the same
// sealed plan) needs a per-SEGMENT cache entry: the key becomes
// (fingerprint, frame_range) because one plan legitimately produces many
// artifacts, one per window. The plan itself carries no range by contract
// (bench_seek_test.go pins that the source block exposes exactly
// asset_id/path/sha256 and no hidden offset), so the range can only come from a
// producer that does not exist yet (RenderingGen's queue accepts
// parent_job_id/chunk_index/frame_range, and jobFrameRange renders the window,
// but nothing on the clip lane submits chunks). Adding a range key now would be
// a dead field that silently never matches; the key must be extended in the
// same change that introduces the producer, and this comment is the record of
// why the cache was left whole-clip.

package cliprender

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// RenderCacheRecord is the durable cache value addressed by fingerprint.
type RenderCacheRecord struct {
	Fingerprint string
	AssetID     string
	StorageKey  string
	ArtifactURL string
	ContentType string
	SHA256      string
	SizeBytes   int64
	DurationSec float64
	Width       uint32
	Height      uint32
	FPSNum      uint32
	FPSDen      uint32
	Backend     RenderBackend
}

// RenderCache is the port for the deterministic render cache.
type RenderCache interface {
	Get(ctx context.Context, fingerprint string) (*RenderCacheRecord, error)
	Put(ctx context.Context, rec *RenderCacheRecord) error
}

// ErrCacheMiss is returned when no cache entry exists for a fingerprint.
var ErrCacheMiss = errors.New("clip.render cache: miss")

var _ RenderCache = (*pgRenderCache)(nil)

// pgRenderCache is the PostgreSQL implementation.
//
// DDL (applied via ensureRenderCacheTable):
//
//	CREATE TABLE IF NOT EXISTS clip_render_cache (
//	    fingerprint  TEXT PRIMARY KEY,
//	    asset_id     TEXT NOT NULL,
//	    storage_key  TEXT NOT NULL DEFAULT '',
//	    artifact_url TEXT NOT NULL DEFAULT '',
//	    content_type TEXT NOT NULL DEFAULT '',
//	    sha256       TEXT NOT NULL,
//	    size_bytes   BIGINT NOT NULL,
//	    duration_sec REAL NOT NULL DEFAULT 0,
//	    width        INTEGER NOT NULL DEFAULT 0,
//	    height       INTEGER NOT NULL DEFAULT 0,
//	    fps_num      INTEGER NOT NULL DEFAULT 0,
//	    fps_den      INTEGER NOT NULL DEFAULT 1,
//	    backend      TEXT NOT NULL DEFAULT '',
//	    created_at   TEXT NOT NULL DEFAULT ''
//	);
//
// The table lives in the PostgreSQL media database (same DSN as
// MediaSearcher/MediaCommitter). No trigger, no FK: the entry is a hint that
// points at a row in media_assets, and Get() validates that row explicitly
// (see the staleness contract above) rather than trusting the hint.
type pgRenderCache struct {
	db *sql.DB
}

// NewPostgresRenderCache constructs the cache. db is the PostgreSQL
// media *sql.DB (nil → fail-closed at ensure time, never a silent SQLite
// fallback).
func NewPostgresRenderCache(db *sql.DB) RenderCache {
	if db == nil {
		return nil
	}
	return &pgRenderCache{db: db}
}

func ensureRenderCacheTable(ctx context.Context, db *sql.DB) error {
	const ddl = `
		CREATE TABLE IF NOT EXISTS clip_render_cache (
			fingerprint  TEXT PRIMARY KEY,
			asset_id     TEXT NOT NULL,
			storage_key  TEXT NOT NULL DEFAULT '',
			artifact_url TEXT NOT NULL DEFAULT '',
			content_type TEXT NOT NULL DEFAULT '',
			sha256       TEXT NOT NULL,
			size_bytes   BIGINT NOT NULL,
			duration_sec REAL  NOT NULL DEFAULT 0,
			width        INTEGER NOT NULL DEFAULT 0,
			height       INTEGER NOT NULL DEFAULT 0,
			fps_num      INTEGER NOT NULL DEFAULT 0,
			fps_den      INTEGER NOT NULL DEFAULT 1,
			backend      TEXT NOT NULL DEFAULT '',
			created_at   TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_clip_render_cache_asset
			ON clip_render_cache (asset_id);
	`
	_, err := db.ExecContext(ctx, ddl)
	if err != nil {
		return fmt.Errorf("clip.render cache: ensure table: %w", err)
	}
	return nil
}

// EnsureRenderCacheTable ensures the cache table exists. Called once at
// boot from the composition root; safe to call concurrently.
func EnsureRenderCacheTable(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("clip.render cache: media DB is not deployed (nil handle)")
	}
	return ensureRenderCacheTable(ctx, db)
}

// renderCacheGetQuery resolves a fingerprint to a certified locator, but only
// while the media row it references still carries the same bytes.
//
// The JOIN is the whole point: without it the cache advertised artifacts that
// had already been deleted or overwritten. `lifecycle_state`/`deleted_at`
// cover the retirement states media_assets can be left in; a row that is no
// longer ACTIVE must not be served as a hit.
const renderCacheGetQuery = `
		SELECT c.fingerprint, c.asset_id, c.storage_key, c.artifact_url, c.content_type,
		       c.sha256, c.size_bytes, c.duration_sec, c.width, c.height, c.fps_num, c.fps_den, c.backend
		FROM clip_render_cache c
		JOIN media_assets a
		  ON a.id = c.asset_id
		 AND a.content_sha256 = c.sha256
		WHERE c.fingerprint = $1
		  AND a.lifecycle_state = 'ACTIVE'
		  AND a.deleted_at = ''`

func (c *pgRenderCache) Get(ctx context.Context, fingerprint string) (*RenderCacheRecord, error) {
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if fingerprint == "" {
		return nil, fmt.Errorf("clip.render cache: fingerprint is required")
	}
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("clip.render cache: not wired (media DB nil)")
	}
	row := c.db.QueryRowContext(ctx, renderCacheGetQuery, fingerprint)
	var r RenderCacheRecord
	var backend string
	if err := row.Scan(&r.Fingerprint, &r.AssetID, &r.StorageKey, &r.ArtifactURL, &r.ContentType,
		&r.SHA256, &r.SizeBytes, &r.DurationSec, &r.Width, &r.Height, &r.FPSNum, &r.FPSDen, &backend); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCacheMiss
		}
		return nil, fmt.Errorf("clip.render cache: get %q: %w", fingerprint, err)
	}
	r.Backend = RenderBackend(backend)
	return &r, nil
}

func (c *pgRenderCache) Put(ctx context.Context, rec *RenderCacheRecord) error {
	if rec == nil || strings.TrimSpace(rec.Fingerprint) == "" {
		return fmt.Errorf("clip.render cache: record/fingerprint is required")
	}
	if strings.TrimSpace(rec.SHA256) == "" || rec.SizeBytes <= 0 {
		return fmt.Errorf("clip.render cache: sha256/size are required")
	}
	if c == nil || c.db == nil {
		return fmt.Errorf("clip.render cache: not wired (media DB nil)")
	}
	const q = `
		INSERT INTO clip_render_cache
			(fingerprint, asset_id, storage_key, artifact_url, content_type,
			 sha256, size_bytes, duration_sec, width, height, fps_num, fps_den, backend, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13, NOW()::text)
		ON CONFLICT (fingerprint) DO UPDATE SET
			asset_id     = EXCLUDED.asset_id,
			storage_key  = EXCLUDED.storage_key,
			artifact_url = EXCLUDED.artifact_url,
			content_type = EXCLUDED.content_type,
			sha256       = EXCLUDED.sha256,
			size_bytes   = EXCLUDED.size_bytes,
			duration_sec = EXCLUDED.duration_sec,
			width        = EXCLUDED.width,
			height       = EXCLUDED.height,
			fps_num      = EXCLUDED.fps_num,
			fps_den      = EXCLUDED.fps_den,
			backend      = EXCLUDED.backend,
			created_at   = NOW()::text
	`
	_, err := c.db.ExecContext(ctx, q,
		strings.ToLower(rec.Fingerprint), rec.AssetID, rec.StorageKey, rec.ArtifactURL, rec.ContentType,
		strings.ToLower(rec.SHA256), rec.SizeBytes, rec.DurationSec,
		rec.Width, rec.Height, rec.FPSNum, rec.FPSDen, string(rec.Backend))
	if err != nil {
		return fmt.Errorf("clip.render cache: put %q: %w", rec.Fingerprint, err)
	}
	return nil
}

// memoryRenderCache is the in-process fallback for tests and for the
// media-disabled path. Not used in production.
//
// It holds only the records Put into it and has no view of media_assets, so it
// cannot reproduce the postgres cache's staleness check. That check is pinned
// against a live PostgreSQL in render_cache_postgres_test.go; keeping the
// in-process double validation-free is deliberate — a double that pretended to
// validate would make the hermetic suite pass over the very bug the SQL JOIN
// exists to prevent.
type memoryRenderCache struct {
	m map[string]*RenderCacheRecord
}

func newMemoryRenderCache() *memoryRenderCache {
	return &memoryRenderCache{m: make(map[string]*RenderCacheRecord)}
}

func (c *memoryRenderCache) Get(_ context.Context, fingerprint string) (*RenderCacheRecord, error) {
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if fingerprint == "" {
		return nil, fmt.Errorf("clip.render cache: fingerprint is required")
	}
	if rec, ok := c.m[fingerprint]; ok {
		cp := *rec
		return &cp, nil
	}
	return nil, ErrCacheMiss
}

func (c *memoryRenderCache) Put(_ context.Context, rec *RenderCacheRecord) error {
	if rec == nil || strings.TrimSpace(rec.Fingerprint) == "" {
		return fmt.Errorf("clip.render cache: record/fingerprint is required")
	}
	cp := *rec
	cp.Fingerprint = strings.ToLower(cp.Fingerprint)
	c.m[cp.Fingerprint] = &cp
	return nil
}

// Ensure memory cache satisfies the port (keeps the test helper honest).
var _ RenderCache = (*memoryRenderCache)(nil)
