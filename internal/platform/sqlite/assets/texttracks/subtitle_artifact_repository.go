package texttracks

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

type SubtitleArtifactRepositorySQLite struct {
	db  *sql.DB
	log *zap.Logger
}

func NewSubtitleArtifactRepository(db *sql.DB, log *zap.Logger) (*SubtitleArtifactRepositorySQLite, error) {
	if db == nil {
		return nil, errors.New("subtitle_artifact_repository: sql.DB is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &SubtitleArtifactRepositorySQLite{db: db, log: log}, nil
}

var _ detail.SubtitleArtifactRepository = (*SubtitleArtifactRepositorySQLite)(nil)

func (r *SubtitleArtifactRepositorySQLite) Upsert(ctx context.Context, art *detail.SubtitleArtifact) error {
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
	// Resolve the current natural-key row before deciding between UPDATE and
	// INSERT. Callers commonly build a fresh artifact value for a retry; using
	// the natural key keeps the Drive metadata update on that same row.
	if art.ID == 0 {
		_ = tx.QueryRowContext(ctx, `
			SELECT id FROM asset_subtitle_artifacts
			WHERE asset_id = ? AND language_code = ? AND format = ? AND is_current = 1
			ORDER BY id DESC LIMIT 1`,
			art.AssetID, art.LanguageCode, string(art.Format)).Scan(&art.ID)
	}

	// If is_current is 1, we must set other rows with same (asset_id, language_code, format) to is_current = 0
	if art.IsCurrent {
		_, err = tx.ExecContext(ctx, `
			UPDATE asset_subtitle_artifacts 
			SET is_current = 0, updated_at = ? 
			WHERE asset_id = ? AND language_code = ? AND format = ? AND is_current = 1 AND id != ?`,
			nowStr, art.AssetID, art.LanguageCode, string(art.Format), art.ID)
		if err != nil {
			return err
		}
	}

	if art.ID > 0 {
		// Update existing
		_, err = tx.ExecContext(ctx, `
			UPDATE asset_subtitle_artifacts SET 
				asset_id = ?, text_track_id = ?, language_code = ?, format = ?,
				local_path = ?, drive_file_id = ?, drive_url = ?, legacy_file_md5 = ?, text_hash = ?,
				cues_hash = ?, clip_content_hash = ?, cue_count = ?,
				clip_duration_ms = ?, last_cue_end_ms = ?, style_version = ?,
				generator_version = ?, status = ?, is_current = ?,
				validation_error = ?, updated_at = ?
			WHERE id = ?`,
			art.AssetID, art.TextTrackID, art.LanguageCode, string(art.Format),
			art.LocalPath, art.DriveFileID, art.DriveURL, art.LegacyFileMD5, art.TextHash,
			art.CuesHash, art.ClipContentHash, art.CueCount,
			art.ClipDurationMs, art.LastCueEndMs, art.StyleVersion,
			art.GeneratorVersion, string(art.Status), checkBoolInt(art.IsCurrent),
			art.ValidationError, nowStr, art.ID)
		if err != nil {
			return err
		}
	} else {
		// Insert new
		res, err := tx.ExecContext(ctx, `
			INSERT INTO asset_subtitle_artifacts (
				asset_id, text_track_id, language_code, format,
				local_path, drive_file_id, drive_url, file_hash, text_hash,
				cues_hash, clip_content_hash, cue_count,
				clip_duration_ms, last_cue_end_ms, style_version,
				generator_version, status, is_current,
				validation_error, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			art.AssetID, art.TextTrackID, art.LanguageCode, string(art.Format),
			art.LocalPath, art.DriveFileID, art.DriveURL, art.LegacyFileMD5, art.TextHash,
			art.CuesHash, art.ClipContentHash, art.CueCount,
			art.ClipDurationMs, art.LastCueEndMs, art.StyleVersion,
			art.GeneratorVersion, string(art.Status), checkBoolInt(art.IsCurrent),
			art.ValidationError, nowStr, nowStr)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err == nil {
			art.ID = id
		}
		// Some SQLite driver/build combinations do not expose LastInsertId
		// reliably. Resolve the just-created current row by its natural key
		// so the follow-up publish update cannot accidentally insert a second
		// READY row without Drive metadata.
		if art.ID == 0 {
			err = tx.QueryRowContext(ctx, `
				SELECT id FROM asset_subtitle_artifacts
				WHERE asset_id = ? AND language_code = ? AND format = ? AND is_current = 1
				ORDER BY id DESC LIMIT 1`,
				art.AssetID, art.LanguageCode, string(art.Format)).Scan(&art.ID)
			if err != nil {
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

func (r *SubtitleArtifactRepositorySQLite) FindCurrent(ctx context.Context, assetID string, languageCode string, format detail.SubtitleFormat) (*detail.SubtitleArtifact, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, asset_id, text_track_id, language_code, format,
		       local_path, drive_file_id, drive_url, file_hash, text_hash,
		       cues_hash, clip_content_hash, cue_count,
		       clip_duration_ms, last_cue_end_ms, style_version,
		       generator_version, status, is_current, validation_error,
		       created_at, updated_at
		FROM asset_subtitle_artifacts
		WHERE asset_id = ? AND language_code = ? AND format = ? AND is_current = 1`,
		assetID, languageCode, string(format))

	return scanSubtitleArtifact(row)
}

func (r *SubtitleArtifactRepositorySQLite) ListByAsset(ctx context.Context, assetID string) ([]detail.SubtitleArtifact, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, asset_id, text_track_id, language_code, format,
		       local_path, drive_file_id, drive_url, file_hash, text_hash,
		       cues_hash, clip_content_hash, cue_count,
		       clip_duration_ms, last_cue_end_ms, style_version,
		       generator_version, status, is_current, validation_error,
		       created_at, updated_at
		FROM asset_subtitle_artifacts
		WHERE asset_id = ?
		ORDER BY created_at DESC`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []detail.SubtitleArtifact
	for rows.Next() {
		art, err := scanSubtitleArtifact(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *art)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return list, nil
}

func checkBoolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

type subtitleScanner interface {
	Scan(dest ...any) error
}

func scanSubtitleArtifact(s subtitleScanner) (*detail.SubtitleArtifact, error) {
	var art detail.SubtitleArtifact
	var (
		formatStr    string
		statusStr    string
		isCurrentInt int
		createdStr   string
		updatedStr   string
	)
	err := s.Scan(
		&art.ID, &art.AssetID, &art.TextTrackID, &art.LanguageCode, &formatStr,
		&art.LocalPath, &art.DriveFileID, &art.DriveURL, &art.LegacyFileMD5, &art.TextHash,
		&art.CuesHash, &art.ClipContentHash, &art.CueCount,
		&art.ClipDurationMs, &art.LastCueEndMs, &art.StyleVersion,
		&art.GeneratorVersion, &statusStr, &isCurrentInt, &art.ValidationError,
		&createdStr, &updatedStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	art.Format = detail.SubtitleFormat(formatStr)
	art.Status = detail.SubtitleArtifactStatus(statusStr)
	art.IsCurrent = isCurrentInt == 1
	if t, err := time.Parse(time.RFC3339, createdStr); err == nil {
		art.CreatedAt = t
	} else if t, err := time.Parse("2006-01-02 15:04:05", createdStr); err == nil {
		art.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, updatedStr); err == nil {
		art.UpdatedAt = t
	} else if t, err := time.Parse("2006-01-02 15:04:05", updatedStr); err == nil {
		art.UpdatedAt = t
	}
	return &art, nil
}
