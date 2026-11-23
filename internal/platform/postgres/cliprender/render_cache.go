// Package cliprender owns the PostgreSQL adapter for the deterministic
// clip.render cache. The port and its record type stay in
// internal/capabilities/cliprender; this package owns the statements, the DDL
// and the engine handle, so the capability never imports database/sql.
package cliprender

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// PostgresRenderCache is the PostgreSQL implementation of
// kernelmedia.RenderCache.
type PostgresRenderCache struct {
	db *sql.DB
}

var _ kernelmedia.RenderCache = (*PostgresRenderCache)(nil)

// NewPostgresRenderCache constructs the cache. db is the PostgreSQL media
// *sql.DB (nil → fail-closed at ensure time, never a silent SQLite fallback).
func NewPostgresRenderCache(db *sql.DB) kernelmedia.RenderCache {
	if db == nil {
		return nil
	}
	return &PostgresRenderCache{db: db}
}

// renderCacheDDL is the full cache schema applied at boot. It owns exactly the
// whole-clip table and its asset index; the segmented chunk table was removed
// with the ChunkRenderCache port (no producer, no reader) and is deliberately
// not recreated. An existing clip_render_chunk_cache in a deployed database is
// left untouched: no destructive migration.
const renderCacheDDL = `
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
			created_at   TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_clip_render_cache_asset
			ON clip_render_cache (asset_id);
	`

// EnsureRenderCacheTable ensures the cache table exists. Called once at boot
// from the composition root; safe to call concurrently.
func EnsureRenderCacheTable(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("clip.render cache: media DB is not deployed (nil handle)")
	}
	if _, err := db.ExecContext(ctx, renderCacheDDL); err != nil {
		return fmt.Errorf("clip.render cache: ensure table: %w", err)
	}
	return nil
}

// renderCacheGetQuery resolves a fingerprint to a certified locator, but only
// while the media row it references still carries the same bytes.
const renderCacheGetQuery = `
		SELECT c.fingerprint, c.asset_id, c.storage_key, c.artifact_url, c.content_type,
		       c.sha256, c.size_bytes, c.duration_sec, c.width, c.height, c.fps_num, c.fps_den, c.backend,
		       COALESCE(a.drive_file_id, ''),
		       COALESCE(NULLIF(a.metadata_json, '')::jsonb->>'delivery_status', '')
		FROM clip_render_cache c
		JOIN media_assets a
		  ON a.id = c.asset_id
		 AND a.content_sha256 = c.sha256
		WHERE c.fingerprint = $1
		  AND a.lifecycle_state = 'ACTIVE'
		  AND a.deleted_at = ''`

// Get returns the certified locator for fingerprint, or kernelmedia.ErrCacheMiss.
func (c *PostgresRenderCache) Get(ctx context.Context, fingerprint string) (*kernelmedia.RenderCacheRecord, error) {
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if fingerprint == "" {
		return nil, fmt.Errorf("clip.render cache: fingerprint is required")
	}
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("clip.render cache: not wired (media DB nil)")
	}
	row := c.db.QueryRowContext(ctx, renderCacheGetQuery, fingerprint)
	var r kernelmedia.RenderCacheRecord
	var backend string
	if err := row.Scan(&r.Fingerprint, &r.AssetID, &r.StorageKey, &r.ArtifactURL, &r.ContentType,
		&r.SHA256, &r.SizeBytes, &r.DurationSec, &r.Width, &r.Height, &r.FPSNum, &r.FPSDen, &backend,
		&r.DriveFileID, &r.DeliveryStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, kernelmedia.ErrCacheMiss
		}
		return nil, fmt.Errorf("clip.render cache: get %q: %w", fingerprint, err)
	}
	r.Backend = kernelmedia.RenderBackend(backend)
	return &r, nil
}

// GetMany resolves up to N fingerprints in ONE query. It is the batch cache
// fast path: POST /render/batch previously issued one round-trip per unique
// item (CR-05). Missing/stale fingerprints are simply absent from the map.
func (c *PostgresRenderCache) GetMany(ctx context.Context, fingerprints []string) (map[string]*kernelmedia.RenderCacheRecord, error) {
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("clip.render cache: not wired (media DB nil)")
	}
	seen := make(map[string]struct{}, len(fingerprints))
	normalized := make([]string, 0, len(fingerprints))
	for _, fp := range fingerprints {
		fp = strings.ToLower(strings.TrimSpace(fp))
		if fp == "" {
			continue
		}
		if _, ok := seen[fp]; ok {
			continue
		}
		seen[fp] = struct{}{}
		normalized = append(normalized, fp)
	}
	out := make(map[string]*kernelmedia.RenderCacheRecord, len(normalized))
	if len(normalized) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(normalized))
	args := make([]any, len(normalized))
	for i, fp := range normalized {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = fp
	}
	query := fmt.Sprintf(`
		SELECT c.fingerprint, c.asset_id, c.storage_key, c.artifact_url, c.content_type,
		       c.sha256, c.size_bytes, c.duration_sec, c.width, c.height, c.fps_num, c.fps_den, c.backend,
		       COALESCE(a.drive_file_id, ''),
		       COALESCE(NULLIF(a.metadata_json, '')::jsonb->>'delivery_status', '')
		FROM clip_render_cache c
		JOIN media_assets a
		  ON a.id = c.asset_id
		 AND a.content_sha256 = c.sha256
		WHERE c.fingerprint IN (%s)
		  AND a.lifecycle_state = 'ACTIVE'
		  AND a.deleted_at = ''`, strings.Join(placeholders, ","))
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clip.render cache: get many: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r kernelmedia.RenderCacheRecord
		var backend string
		if err := rows.Scan(&r.Fingerprint, &r.AssetID, &r.StorageKey, &r.ArtifactURL, &r.ContentType,
			&r.SHA256, &r.SizeBytes, &r.DurationSec, &r.Width, &r.Height, &r.FPSNum, &r.FPSDen, &backend,
			&r.DriveFileID, &r.DeliveryStatus); err != nil {
			return nil, fmt.Errorf("clip.render cache: get many scan: %w", err)
		}
		r.Backend = kernelmedia.RenderBackend(backend)
		out[r.Fingerprint] = &r
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clip.render cache: get many rows: %w", err)
	}
	return out, nil
}

// Put upserts a certified locator under its fingerprint.
func (c *PostgresRenderCache) Put(ctx context.Context, rec *kernelmedia.RenderCacheRecord) error {
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
