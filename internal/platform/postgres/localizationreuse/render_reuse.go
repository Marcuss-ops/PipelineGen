// Package localizationreuse owns the PostgreSQL adapter for the localized
// render reuse seam. The port and its records stay in
// internal/capabilities/localization; this package owns the statements, the DDL
// and the engine handle.
package localizationreuse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	localization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
)

// LocalizedRenderReuse is the PostgreSQL-backed reuse store.
type LocalizedRenderReuse struct {
	db  *sql.DB
	log *zap.Logger
}

var _ localization.RenderReuseCache = (*LocalizedRenderReuse)(nil)
var _ localization.RenderDriveIdentityRecorder = (*LocalizedRenderReuse)(nil)

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
			created_at  TEXT   NOT NULL
		);`
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("localization render reuse: ensure table: %w", err)
	}
	// Publication identity was added after the first release of this table:
	// existing deployments get the columns in place, and a record written before
	// them simply keeps empty values (a cache that cannot name a Drive file is a
	// render-only cache, never a broken one).
	const alter = `
		ALTER TABLE localized_render_reuse ADD COLUMN IF NOT EXISTS drive_file_id   TEXT NOT NULL DEFAULT '';
		ALTER TABLE localized_render_reuse ADD COLUMN IF NOT EXISTS drive_link      TEXT NOT NULL DEFAULT '';
		ALTER TABLE localized_render_reuse ADD COLUMN IF NOT EXISTS drive_folder_id TEXT NOT NULL DEFAULT '';`
	if _, err := db.ExecContext(ctx, alter); err != nil {
		return fmt.Errorf("localization render reuse: ensure publication columns: %w", err)
	}
	return nil
}

// Lookup resolves a plan fingerprint to the artifact a previous render
// certified. An absent row is a MISS, not an error: the first run of every plan
// takes this path.
func (s *LocalizedRenderReuse) Lookup(ctx context.Context, fingerprint string) (*localization.ReusedRenderArtifact, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, errors.New("localization render reuse: store is not initialized")
	}
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if fingerprint == "" {
		return nil, false, errors.New("localization render reuse: fingerprint is required")
	}
	const q = `
		SELECT local_path, sha256, size_bytes, duration_ms, video_codec, audio_codec, backend,
		       drive_file_id, drive_link, drive_folder_id
		FROM localized_render_reuse
		WHERE fingerprint = $1`
	var (
		record  localization.ReusedRenderArtifact
		backend string
	)
	err := s.db.QueryRowContext(ctx, q, fingerprint).
		Scan(&record.LocalPath, &record.SHA256, &record.SizeBytes, &record.DurationMS, &record.VideoCodec, &record.AudioCodec, &backend,
			&record.DriveFileID, &record.DriveLink, &record.DriveFolderID)
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
// usable bytes is refused rather than stored.
func (s *LocalizedRenderReuse) Store(ctx context.Context, artifact localization.ReusedRenderArtifact) error {
	if s == nil || s.db == nil {
		return errors.New("localization render reuse: store is not initialized")
	}
	fingerprint := strings.ToLower(strings.TrimSpace(artifact.Fingerprint))
	if fingerprint == "" || strings.TrimSpace(artifact.LocalPath) == "" || strings.TrimSpace(artifact.SHA256) == "" ||
		artifact.SizeBytes <= 0 || artifact.DurationMS <= 0 {
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

// RecordDriveIdentity stores where certified bytes were published, so an
// identical later run can certify them UPLOADED without moving 400 MB again.
// A missing row is deliberately not an error.
func (s *LocalizedRenderReuse) RecordDriveIdentity(ctx context.Context, fingerprint string, identity localization.RenderDriveIdentity) error {
	if s == nil || s.db == nil {
		return errors.New("localization render reuse: store is not initialized")
	}
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if fingerprint == "" || strings.TrimSpace(identity.FileID) == "" || strings.TrimSpace(identity.Link) == "" {
		return fmt.Errorf("localization render reuse: incomplete Drive identity for fingerprint %q", fingerprint)
	}
	const q = `
		UPDATE localized_render_reuse
		SET drive_file_id = $2, drive_link = $3, drive_folder_id = $4
		WHERE fingerprint = $1`
	if _, err := s.db.ExecContext(ctx, q, fingerprint, identity.FileID, identity.Link, identity.FolderID); err != nil {
		s.log.Warn("localization render reuse: record drive identity failed",
			zap.String("subsystem", "localization_render_reuse"),
			zap.String("fingerprint", fingerprint),
			zap.Error(err))
		return fmt.Errorf("localization render reuse: record drive identity %q: %w", fingerprint, err)
	}
	return nil
}
