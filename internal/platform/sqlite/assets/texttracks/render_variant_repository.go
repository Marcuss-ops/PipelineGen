package texttracks

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// RenderVariantRepositorySQLite is the SQLite-backed implementation of
// detail.RenderVariantRepository (migrations/sqlite/219_asset_render_variants.sql).
type RenderVariantRepositorySQLite struct {
	db  *sql.DB
	log *zap.Logger
}

func NewRenderVariantRepository(db *sql.DB, log *zap.Logger) (*RenderVariantRepositorySQLite, error) {
	if db == nil {
		return nil, errors.New("render_variant_repository: sql.DB is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &RenderVariantRepositorySQLite{db: db, log: log}, nil
}

var _ detail.RenderVariantRepository = (*RenderVariantRepositorySQLite)(nil)

func (r *RenderVariantRepositorySQLite) Upsert(ctx context.Context, v *detail.RenderVariant) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	nowStr := time.Now().UTC().Format(time.RFC3339)
	if v.ID == 0 {
		_ = tx.QueryRowContext(ctx, `
			SELECT id FROM asset_render_variants
			WHERE source_clip_id = ? AND language_code = ? AND is_current = 1
			ORDER BY id DESC LIMIT 1`,
			v.SourceClipID, v.LanguageCode).Scan(&v.ID)
	}

	if v.IsCurrent {
		_, err = tx.ExecContext(ctx, `
			UPDATE asset_render_variants
			SET is_current = 0, updated_at = ?
			WHERE source_clip_id = ? AND language_code = ? AND is_current = 1 AND id != ?`,
			nowStr, v.SourceClipID, v.LanguageCode, v.ID)
		if err != nil {
			return err
		}
	}

	if v.ID > 0 {
		_, err = tx.ExecContext(ctx, `
			UPDATE asset_render_variants SET
				source_clip_id = ?, language_code = ?, fingerprint = ?,
				source_clip_sha256 = ?, transcript_sha256 = ?,
				translation_version = ?, subtitle_style_version = ?,
				render_profile_version = ?, subtitle_hash = ?, output_hash = ?,
				drive_file_id = ?, drive_link = ?, duration_ms = ?, size_bytes = ?,
				status = ?, validation_error = ?, is_current = ?, updated_at = ?
			WHERE id = ?`,
			v.SourceClipID, v.LanguageCode, v.Fingerprint,
			v.SourceClipSHA256, v.TranscriptSHA256,
			v.TranslationVersion, v.SubtitleStyleVersion,
			v.RenderProfileVersion, v.SubtitleHash, v.OutputHash,
			v.DriveFileID, v.DriveLink, v.DurationMs, v.SizeBytes,
			string(v.Status), v.ValidationError, boolInt(v.IsCurrent), nowStr, v.ID)
		if err != nil {
			return err
		}
	} else {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO asset_render_variants (
				source_clip_id, language_code, fingerprint,
				source_clip_sha256, transcript_sha256,
				translation_version, subtitle_style_version,
				render_profile_version, subtitle_hash, output_hash,
				drive_file_id, drive_link, duration_ms, size_bytes,
				status, validation_error, is_current, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			v.SourceClipID, v.LanguageCode, v.Fingerprint,
			v.SourceClipSHA256, v.TranscriptSHA256,
			v.TranslationVersion, v.SubtitleStyleVersion,
			v.RenderProfileVersion, v.SubtitleHash, v.OutputHash,
			v.DriveFileID, v.DriveLink, v.DurationMs, v.SizeBytes,
			string(v.Status), v.ValidationError, boolInt(v.IsCurrent), nowStr, nowStr)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err == nil {
			v.ID = id
		}
		if v.ID == 0 {
			if err := tx.QueryRowContext(ctx, `
				SELECT id FROM asset_render_variants
				WHERE source_clip_id = ? AND language_code = ? AND is_current = 1
				ORDER BY id DESC LIMIT 1`,
				v.SourceClipID, v.LanguageCode).Scan(&v.ID); err != nil {
				return err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (r *RenderVariantRepositorySQLite) FindCurrent(ctx context.Context, sourceClipID, languageCode string) (*detail.RenderVariant, error) {
	row := r.db.QueryRowContext(ctx, renderVariantSelect+`
		WHERE source_clip_id = ? AND language_code = ? AND is_current = 1`,
		sourceClipID, languageCode)
	return scanRenderVariant(row)
}

func (r *RenderVariantRepositorySQLite) FindByFingerprint(ctx context.Context, sourceClipID, languageCode, fingerprint string) (*detail.RenderVariant, error) {
	row := r.db.QueryRowContext(ctx, renderVariantSelect+`
		WHERE source_clip_id = ? AND language_code = ? AND fingerprint = ?
		ORDER BY id DESC LIMIT 1`,
		sourceClipID, languageCode, fingerprint)
	return scanRenderVariant(row)
}

func (r *RenderVariantRepositorySQLite) ListBySourceClip(ctx context.Context, sourceClipID string) ([]detail.RenderVariant, error) {
	rows, err := r.db.QueryContext(ctx, renderVariantSelect+`
		WHERE source_clip_id = ?
		ORDER BY id DESC`, sourceClipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []detail.RenderVariant
	for rows.Next() {
		v, err := scanRenderVariant(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *v)
	}
	return list, rows.Err()
}

const renderVariantSelect = `
	SELECT id, source_clip_id, language_code, fingerprint,
	       source_clip_sha256, transcript_sha256,
	       translation_version, subtitle_style_version,
	       render_profile_version, subtitle_hash, output_hash,
	       drive_file_id, drive_link, duration_ms, size_bytes,
	       status, validation_error, is_current, created_at, updated_at
	FROM asset_render_variants`

func scanRenderVariant(s interface{ Scan(dest ...any) error }) (*detail.RenderVariant, error) {
	var v detail.RenderVariant
	var (
		statusStr    string
		isCurrentInt int
		createdStr   string
		updatedStr   string
	)
	err := s.Scan(
		&v.ID, &v.SourceClipID, &v.LanguageCode, &v.Fingerprint,
		&v.SourceClipSHA256, &v.TranscriptSHA256,
		&v.TranslationVersion, &v.SubtitleStyleVersion,
		&v.RenderProfileVersion, &v.SubtitleHash, &v.OutputHash,
		&v.DriveFileID, &v.DriveLink, &v.DurationMs, &v.SizeBytes,
		&statusStr, &v.ValidationError, &isCurrentInt, &createdStr, &updatedStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	v.Status = detail.RenderVariantStatus(statusStr)
	v.IsCurrent = isCurrentInt == 1
	v.CreatedAt = parseSQLiteTime(createdStr)
	v.UpdatedAt = parseSQLiteTime(updatedStr)
	return &v, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func parseSQLiteTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	return time.Time{}
}
