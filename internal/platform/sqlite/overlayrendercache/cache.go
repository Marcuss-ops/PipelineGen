// Package overlayrendercache persists the content-addressed reuse index for
// certified per-item overlay renders in the dedicated cache-plane SQLite DB.
//
// The table is rebuildable and the cache is never a dependency: this adapter
// reports a miss for every condition it cannot certify (missing row, INVALID
// status, corrupt JSON, artifact/key identity drift). It returns an error only
// for a database failure the caller must not mistake for a miss decision —
// although the enqueuer degrades even that to a normal render and counts it.
package overlayrendercache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// ErrNotWired is returned when the adapter is constructed without a database.
var ErrNotWired = errors.New("overlay render cache sqlite adapter: not wired")

// Cache is the SQLite-backed index. It is safe for concurrent use: every
// method is a single statement or a single-row transaction on *sql.DB.
type Cache struct {
	db *sql.DB
}

var _ scriptgen.OverlayRenderCache = (*Cache)(nil)

func New(db *sql.DB) (*Cache, error) {
	if db == nil {
		return nil, ErrNotWired
	}
	return &Cache{db: db}, nil
}

// Lookup returns the certified artifact for a key. A row that cannot be
// certified (corrupt JSON, status drift, digest drift) is marked INVALID and
// reported as a miss, so the next render overwrites it instead of failing.
func (c *Cache) Lookup(ctx context.Context, key scriptgen.OverlayRenderCacheKey) (*scriptgen.RenderArtifact, bool, error) {
	if c == nil || c.db == nil {
		return nil, false, ErrNotWired
	}
	if err := key.Validate(); err != nil {
		return nil, false, err
	}
	digest := key.Digest()
	var (
		artifactJSON string
		artifactSHA  string
		status       string
	)
	err := c.db.QueryRowContext(ctx, `
		SELECT artifact_json, artifact_sha256, status
		FROM overlay_render_cache_entries
		WHERE cache_key = ?`, digest).Scan(&artifactJSON, &artifactSHA, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("overlay render cache lookup %q: %w", digest, err)
	}
	if status != "READY" {
		return nil, false, nil
	}
	var artifact scriptgen.RenderArtifact
	if err := json.Unmarshal([]byte(artifactJSON), &artifact); err != nil {
		if invalidationErr := c.markInvalid(ctx, digest, "stored artifact JSON is corrupt: "+err.Error()); invalidationErr != nil {
			return nil, false, invalidationErr
		}
		return nil, false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(artifact.SHA256), strings.TrimSpace(artifactSHA)) {
		if invalidationErr := c.markInvalid(ctx, digest, "stored artifact digest does not match its index row"); invalidationErr != nil {
			return nil, false, invalidationErr
		}
		return nil, false, nil
	}
	if _, err := c.db.ExecContext(ctx, `
		UPDATE overlay_render_cache_entries
		SET hit_count = hit_count + 1, last_used_at = ?, updated_at = ?
		WHERE cache_key = ?`,
		nowRFC3339Nano(), nowRFC3339Nano(), digest); err != nil {
		return nil, false, fmt.Errorf("overlay render cache touch %q: %w", digest, err)
	}
	return &artifact, true, nil
}

// Store upserts a certified artifact. Re-storing a key refreshes the locator
// and resets an INVALID row to READY: a fresh render is the repair.
func (c *Cache) Store(ctx context.Context, key scriptgen.OverlayRenderCacheKey, artifact scriptgen.RenderArtifact) error {
	if c == nil || c.db == nil {
		return ErrNotWired
	}
	if err := key.Validate(); err != nil {
		return err
	}
	sha := strings.ToLower(strings.TrimSpace(artifact.SHA256))
	if len(sha) != 64 {
		return fmt.Errorf("overlay render cache store: artifact SHA-256 %q is not a 64-hex digest", artifact.SHA256)
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		return fmt.Errorf("overlay render cache store: encode artifact: %w", err)
	}
	digest := key.Digest()
	now := nowRFC3339Nano()
	_, err = c.db.ExecContext(ctx, `
		INSERT INTO overlay_render_cache_entries
		(cache_key, render_key, plan_context, epoch, artifact_json, artifact_sha256,
		 size_bytes, job_id, status, invalidated_reason, hit_count,
		 created_at, last_used_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'READY', '', 0, ?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET
			artifact_json = excluded.artifact_json,
			artifact_sha256 = excluded.artifact_sha256,
			size_bytes = excluded.size_bytes,
			job_id = excluded.job_id,
			status = 'READY',
			invalidated_reason = '',
			last_used_at = excluded.last_used_at,
			updated_at = excluded.updated_at`,
		digest, key.RenderKey, key.PlanContext, key.Epoch, string(raw), sha,
		artifact.SizeBytes, strings.TrimSpace(artifact.ID), now, now, now)
	if err != nil {
		return fmt.Errorf("overlay render cache store %q: %w", digest, err)
	}
	return nil
}

// Invalidate marks the row unusable. A key that has no row is not an error:
// the cache may have been swept or never written.
func (c *Cache) Invalidate(ctx context.Context, key scriptgen.OverlayRenderCacheKey, reason string) error {
	if c == nil || c.db == nil {
		return ErrNotWired
	}
	if err := key.Validate(); err != nil {
		return err
	}
	return c.markInvalid(ctx, key.Digest(), reason)
}

func (c *Cache) markInvalid(ctx context.Context, digest, reason string) error {
	reason = strings.TrimSpace(reason)
	if len(reason) > 512 {
		reason = reason[:512]
	}
	now := nowRFC3339Nano()
	if _, err := c.db.ExecContext(ctx, `
		UPDATE overlay_render_cache_entries
		SET status = 'INVALID', invalidated_reason = ?, updated_at = ?
		WHERE cache_key = ?`, reason, now, digest); err != nil {
		return fmt.Errorf("overlay render cache invalidate %q: %w", digest, err)
	}
	return nil
}

func nowRFC3339Nano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
