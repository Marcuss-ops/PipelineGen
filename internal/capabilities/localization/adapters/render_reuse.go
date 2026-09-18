package adapters

// render_reuse.go — the durable store behind localization's content-addressed
// reuse seam (localization.RenderReuseCache).
//
// WHY A TABLE AND NOT THE cliprender CACHE: cliprender's clip_render_cache
// records a DURABLE OBJECT-STORE LOCATOR and nothing else — it carries no media
// codec facts and no local materialization, because its consumer only needs to
// advertise a certified asset id. A localized render is consumed differently: the
// localization service publishes LOCAL bytes to Drive and commits them to the
// media SSOT with their codecs, so a reuse record must carry exactly those facts
// (path, digest, size, duration, codecs, backend). Storing a locator-only record
// here would produce hits that cannot be consumed; storing these facts in
// clip_render_cache would widen a table whose integrity contract is a JOIN
// against media_assets.
//
// The record is a HINT, never an authority: the renderer re-stats the file, checks
// the size and re-hashes the bytes before a hit is accepted (render_reuse.go), so
// a stale row costs a render, not a wrong artifact. A row whose file has since
// been cleaned from the work directory therefore degrades to a normal render.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
)

// LocalizedRenderReuse is the PostgreSQL-backed reuse store.
type LocalizedRenderReuse struct {
	db  *sql.DB
	log *zap.Logger
}

var _ localization.RenderReuseCache = (*LocalizedRenderReuse)(nil)

// NewLocalizedRenderReuse builds the store. A nil database yields nil: the
// caller then wires no cache at all, which means "always render" — the same
// behaviour as before the seam existed, and never a silent second store.
func NewLocalizedRenderReuse(db *sql.DB, log *zap.Logger) localization.RenderReuseCache {
	if db == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &LocalizedRenderReuse{db: db, log: log}
}

// EnsureLocalizedRenderReuseTable creates the reuse table. Called once at boot
// from the composition root, next to the canonical render cache's own ensure.
func EnsureLocalizedRenderReuseTable(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("localization render reuse: media DB is not deployed (nil handle)")
	}
	const ddl = `
		CREATE TABLE IF NOT EXISTS localized_render_reuse (
			fingerprint TEXT PRIMARY KEY,
			local_path  TEXT   NOT NULL,
			sha256      TEXT   NOT NULL,
			size_bytes  BIGINT NOT NULL,
			duration_ms BIGINT NOT NULL,
			video_codec TEXT   NOT NULL DEFAULT '',
			audio_codec TEXT   NOT NULL DEFAULT '',
			backend     TEXT   NOT NULL DEFAULT '',
			created_at  TEXT   NOT NULL DEFAULT ''
		);`
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("localization render reuse: ensure table: %w", err)
	}
	return nil
}

// Lookup resolves a plan fingerprint to the artifact a previous render
// certified. An absent row is a MISS, not an error: the first run of every plan
// takes this path.
//
// A store-level failure is reported as an error, and the renderer treats it as a
// miss (fail-soft, see render_reuse.go) — the cache can never fail a render, and
// it can never turn a broken store into a wrong artifact either.
func (s *LocalizedRenderReuse) Lookup(ctx context.Context, fingerprint string) (*localization.ReusedRenderArtifact, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, errors.New("localization render reuse: store is not initialized")
	}
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if fingerprint == "" {
		return nil, false, errors.New("localization render reuse: fingerprint is required")
	}
	const q = `
		SELECT local_path, sha256, size_bytes, duration_ms, video_codec, audio_codec, backend
		FROM localized_render_reuse
		WHERE fingerprint = $1`
	var (
		record  localization.ReusedRenderArtifact
		backend string
	)
	err := s.db.QueryRowContext(ctx, q, fingerprint).
		Scan(&record.LocalPath, &record.SHA256, &record.SizeBytes, &record.DurationMS, &record.VideoCodec, &record.AudioCodec, &backend)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("localization render reuse: lookup %q: %w", fingerprint, err)
	}
	record.Fingerprint = fingerprint
	record.Backend = backend
	return &record, true, nil
}

// Store upserts the certified artifact under its fingerprint. A record with no
// usable bytes is refused rather than stored: a row that cannot pass the
// renderer's own verification is a row that would only ever cost a lookup.
func (s *LocalizedRenderReuse) Store(ctx context.Context, artifact localization.ReusedRenderArtifact) error {
	if s == nil || s.db == nil {
		return errors.New("localization render reuse: store is not initialized")
	}
	fingerprint := strings.ToLower(strings.TrimSpace(artifact.Fingerprint))
	if fingerprint == "" || strings.TrimSpace(artifact.LocalPath) == "" || strings.TrimSpace(artifact.SHA256) == "" ||
		artifact.SizeBytes <= 0 || artifact.DurationMS <= 0 {
		// The renderer swallows Store errors (the artifact is already certified),
		// so this log is the ONLY visibility a lost optimization gets. Without it
		// a systematically failing store would be indistinguishable from a cold
		// cache forever.
		s.log.Warn("localization render reuse: refusing to store an incomplete artifact",
			zap.String("subsystem", "localization_render_reuse"),
			zap.String("fingerprint", fingerprint),
			zap.String("local_path", artifact.LocalPath),
			zap.Int64("size_bytes", artifact.SizeBytes))
		return fmt.Errorf("localization render reuse: incomplete artifact for fingerprint %q", fingerprint)
	}
	const q = `
		INSERT INTO localized_render_reuse
			(fingerprint, local_path, sha256, size_bytes, duration_ms, video_codec, audio_codec, backend, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8, NOW()::text)
		ON CONFLICT (fingerprint) DO UPDATE SET
			local_path  = EXCLUDED.local_path,
			sha256      = EXCLUDED.sha256,
			size_bytes  = EXCLUDED.size_bytes,
			duration_ms = EXCLUDED.duration_ms,
			video_codec = EXCLUDED.video_codec,
			audio_codec = EXCLUDED.audio_codec,
			backend     = EXCLUDED.backend,
			created_at  = NOW()::text`
	if _, err := s.db.ExecContext(ctx, q,
		fingerprint, artifact.LocalPath, strings.ToLower(artifact.SHA256), artifact.SizeBytes,
		artifact.DurationMS, artifact.VideoCodec, artifact.AudioCodec, artifact.Backend); err != nil {
		s.log.Warn("localization render reuse: store failed",
			zap.String("subsystem", "localization_render_reuse"),
			zap.String("fingerprint", fingerprint),
			zap.Error(err))
		return fmt.Errorf("localization render reuse: store %q: %w", fingerprint, err)
	}
	return nil
}
