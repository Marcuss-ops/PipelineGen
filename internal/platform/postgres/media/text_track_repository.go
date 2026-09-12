package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// TextTrackRepositoryPG is the production PostgreSQL implementation of the
// canonical text-track port. Text tracks belong to the media aggregate and
// therefore share the PostgreSQL media SSOT with media_assets and its outbox.
type TextTrackRepositoryPG struct{ db *sql.DB }

func NewTextTrackRepositoryPG(db *sql.DB) (*TextTrackRepositoryPG, error) {
	if db == nil {
		return nil, errors.New("postgres text tracks: db is nil")
	}
	return &TextTrackRepositoryPG{db: db}, nil
}

var _ detail.TextTrackRepository = (*TextTrackRepositoryPG)(nil)

const textTrackColumns = `id, asset_id, language_code, text_kind, text_content,
 source_type, source_language_code, is_original, provider, model_name, model_version,
 prompt_version, text_hash, source_version, translation_key, is_current,
 source_track_id, source_text_hash, confidence, status, created_at, updated_at`

func scanPGTextTrack(s interface{ Scan(...any) error }) (*detail.TextTrack, error) {
	var t detail.TextTrack
	var kind, source, status string
	var original, current int
	var sourceTrack sql.NullInt64
	var confidence sql.NullFloat64
	var created, updated string
	err := s.Scan(&t.ID, &t.AssetID, &t.LanguageCode, &kind, &t.TextContent,
		&source, &t.SourceLanguageCode, &original, &t.Provider, &t.ModelName,
		&t.ModelVersion, &t.PromptVersion, &t.TextHash, &t.SourceVersion,
		&t.TranslationKey, &current, &sourceTrack, &t.SourceTextHash,
		&confidence, &status, &created, &updated)
	if err != nil {
		return nil, err
	}
	t.TextKind, t.SourceType, t.Status = detail.TextTrackKind(kind), detail.TextTrackSource(source), detail.TextTrackStatus(status)
	t.IsOriginal, t.IsCurrent = original != 0, current != 0
	if sourceTrack.Valid {
		v := sourceTrack.Int64
		t.SourceTrackID = &v
	}
	if confidence.Valid {
		v := confidence.Float64
		t.Confidence = &v
	}
	t.CreatedAt, t.UpdatedAt = parsePGTime(created), parsePGTime(updated)
	return &t, nil
}

func parsePGTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05.999999-07:00", raw); err == nil {
		return t
	}
	return time.Time{}
}

func (r *TextTrackRepositoryPG) UpsertBatch(ctx context.Context, tracks []detail.TextTrack) error {
	if len(tracks) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres text tracks: begin: %w", err)
	}
	defer tx.Rollback()
	for _, t := range tracks {
		if t.AssetID == "" || t.LanguageCode == "" || t.TextKind == "" {
			return errors.New("postgres text tracks: asset, language and kind are required")
		}
		status := t.Status
		if status == "" {
			status = detail.TextTrackReady
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO asset_text_tracks (asset_id, language_code, text_kind, text_content, source_type,
 source_language_code, is_original, provider, model_name, model_version, prompt_version,
 text_hash, source_version, translation_key, is_current, source_track_id, source_text_hash,
 confidence, status, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,1,$15,$16,$17,$18,$19,$19)
ON CONFLICT (asset_id, language_code, text_kind) WHERE is_current = 1 DO UPDATE SET
 text_content=EXCLUDED.text_content, source_type=EXCLUDED.source_type,
 source_language_code=EXCLUDED.source_language_code, is_original=EXCLUDED.is_original,
 provider=EXCLUDED.provider, model_name=EXCLUDED.model_name, model_version=EXCLUDED.model_version,
 prompt_version=EXCLUDED.prompt_version, text_hash=EXCLUDED.text_hash,
 source_version=EXCLUDED.source_version, translation_key=EXCLUDED.translation_key,
 source_track_id=EXCLUDED.source_track_id, source_text_hash=EXCLUDED.source_text_hash,
 confidence=EXCLUDED.confidence, status=EXCLUDED.status, updated_at=EXCLUDED.updated_at`,
			t.AssetID, t.LanguageCode, string(t.TextKind), t.TextContent, string(t.SourceType), t.SourceLanguageCode, boolInt(t.IsOriginal), t.Provider, t.ModelName, t.ModelVersion, t.PromptVersion, t.TextHash, t.SourceVersion, t.TranslationKey, nullInt64(t.SourceTrackID), t.SourceTextHash, nullFloat(t.Confidence), string(status), time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("postgres text tracks: upsert %s: %w", t.AssetID, err)
		}
	}
	return tx.Commit()
}

func (r *TextTrackRepositoryPG) Find(ctx context.Context, assetID, language string, kind detail.TextTrackKind) (*detail.TextTrack, error) {
	return r.find(ctx, `WHERE asset_id=$1 AND language_code=$2 AND text_kind=$3`, assetID, language, string(kind))
}
func (r *TextTrackRepositoryPG) find(ctx context.Context, where string, args ...any) (*detail.TextTrack, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+textTrackColumns+` FROM asset_text_tracks `+where, args...)
	t, err := scanPGTextTrack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres text tracks: find: %w", err)
	}
	return t, nil
}
func (r *TextTrackRepositoryPG) FindByID(ctx context.Context, trackID int64) (*detail.TextTrack, []detail.TimedCue, error) {
	if trackID <= 0 {
		return nil, nil, fmt.Errorf("postgres text tracks: FindByID: TrackID is required")
	}
	t, err := r.find(ctx, `WHERE id=$1`, trackID)
	if err != nil || t == nil {
		return t, nil, err
	}
	cues, err := r.cues(ctx, t.ID)
	return t, cues, err
}
func (r *TextTrackRepositoryPG) FindReady(ctx context.Context, assetID, language string, kind detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	t, err := r.find(ctx, `WHERE asset_id=$1 AND language_code=$2 AND text_kind=$3 AND is_current=1 AND status=$4`, assetID, language, string(kind), string(detail.TextTrackReady))
	if err != nil || t == nil {
		return t, nil, err
	}
	cues, err := r.cues(ctx, t.ID)
	return t, cues, err
}
func (r *TextTrackRepositoryPG) cues(ctx context.Context, id int64) ([]detail.TimedCue, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT start_ms,end_ms,text FROM asset_text_track_segments WHERE track_id=$1 ORDER BY sequence_no`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []detail.TimedCue
	for rows.Next() {
		var c detail.TimedCue
		if err := rows.Scan(&c.StartMs, &c.EndMs, &c.Text); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (r *TextTrackRepositoryPG) ListByAsset(ctx context.Context, assetID string) ([]detail.TextTrack, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+textTrackColumns+` FROM asset_text_tracks WHERE asset_id=$1 ORDER BY language_code,text_kind`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]detail.TextTrack, 0)
	for rows.Next() {
		t, e := scanPGTextTrack(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}
func (r *TextTrackRepositoryPG) ListReadyLanguages(ctx context.Context, assetID string, kind detail.TextTrackKind) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT language_code FROM asset_text_tracks WHERE asset_id=$1 AND text_kind=$2 AND is_current=1 AND status=$3 ORDER BY language_code`, assetID, string(kind), string(detail.TextTrackReady))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r *TextTrackRepositoryPG) FindCurrentForTranslation(ctx context.Context, assetID string, kind detail.TextTrackKind, target, sourceHash, model, version, prompt string) (*detail.TextTrack, error) {
	key := detail.TranslationKey(sourceHash, target, model, version, prompt)
	return r.find(ctx, `WHERE asset_id=$1 AND language_code=$2 AND text_kind=$3 AND translation_key=$4 AND is_current=1 AND status=$5`, assetID, target, string(kind), key, string(detail.TextTrackReady))
}
func (r *TextTrackRepositoryPG) InsertTranslationWithAuditPredecessor(ctx context.Context, t detail.TextTrack) error {
	if t.AssetID == "" || t.LanguageCode == "" || t.TextKind == "" || t.TranslationKey == "" {
		return errors.New("postgres text tracks: translation identity is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM asset_text_tracks WHERE asset_id=$1 AND language_code=$2 AND text_kind=$3 AND translation_key=$4 AND is_current=1`, t.AssetID, t.LanguageCode, string(t.TextKind), t.TranslationKey).Scan(&id); err == nil {
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE asset_text_tracks SET is_current=0,updated_at=$1 WHERE asset_id=$2 AND language_code=$3 AND text_kind=$4 AND is_current=1`, now, t.AssetID, t.LanguageCode, string(t.TextKind)); err != nil {
		return err
	}
	status := t.Status
	if status == "" {
		status = detail.TextTrackReady
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO asset_text_tracks (asset_id,language_code,text_kind,text_content,source_type,source_language_code,is_original,provider,model_name,model_version,prompt_version,text_hash,source_version,translation_key,is_current,source_track_id,source_text_hash,confidence,status,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,1,$15,$16,$17,$18,$19,$19)`, t.AssetID, t.LanguageCode, string(t.TextKind), t.TextContent, string(t.SourceType), t.SourceLanguageCode, boolInt(t.IsOriginal), t.Provider, t.ModelName, t.ModelVersion, t.PromptVersion, t.TextHash, t.SourceVersion, t.TranslationKey, nullInt64(t.SourceTrackID), t.SourceTextHash, nullFloat(t.Confidence), string(status), now)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
func nullFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
