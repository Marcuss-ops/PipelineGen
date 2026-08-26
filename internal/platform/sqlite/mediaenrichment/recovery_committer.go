// Package mediaenrichment contains SQLite adapters for the enrichment
// capability. The adapter owns the transaction boundary: text artifacts,
// registry events and targeted Qdrant outbox requests commit together.
package mediaenrichment

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/enrichment"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/google/uuid"
)

type RecoveryCommitter struct {
	db     *sql.DB
	outbox *outboxevents.Repository
}

type AssetReader struct{ db *sql.DB }

func NewAssetReader(db *sql.DB) (*AssetReader, error) {
	if db == nil {
		return nil, fmt.Errorf("media enrichment asset reader: database is required")
	}
	return &AssetReader{db: db}, nil
}
func (r *AssetReader) Exists(ctx context.Context, assetID string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM media_assets WHERE id=? AND lifecycle_state NOT IN ('DELETED','TOMBSTONED')`, assetID).Scan(&n)
	return n > 0, err
}

type TextTrackReader struct{ repo detail.TextTrackRepository }

func NewTextTrackReader(repo detail.TextTrackRepository) (*TextTrackReader, error) {
	if repo == nil {
		return nil, fmt.Errorf("media enrichment text reader: repository is required")
	}
	return &TextTrackReader{repo: repo}, nil
}
func (r *TextTrackReader) Find(ctx context.Context, assetID, language string, kind detail.TextTrackKind) (*detail.TextTrack, error) {
	return r.repo.Find(ctx, assetID, language, kind)
}

var _ enrichment.AssetReader = (*AssetReader)(nil)
var _ enrichment.TextTrackReader = (*TextTrackReader)(nil)

func NewRecoveryCommitter(db *sql.DB, outboxRepo *outboxevents.Repository, targetSchema string) (*RecoveryCommitter, error) {
	if db == nil || outboxRepo == nil {
		return nil, fmt.Errorf("media enrichment committer: database and outbox are required")
	}
	_ = targetSchema // retained in the constructor for caller compatibility; the canonical event owns its schema.
	return &RecoveryCommitter{db: db, outbox: outboxRepo}, nil
}

func (c *RecoveryCommitter) CommitRecoveredText(ctx context.Context, assetID, language string, tracks []detail.TextTrack, projection string) error {
	if len(tracks) == 0 {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	var sourceVersion string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(json_extract(metadata_json,'$.content_hash'),''), NULLIF(json_extract(metadata_json,'$.file_hash'),''), NULLIF(content_sha256,''), NULLIF(binary_sha256,''), NULLIF(legacy_file_md5,''), id) FROM media_assets WHERE id=?`, assetID).Scan(&sourceVersion); err != nil {
		return fmt.Errorf("asset source version: %w", err)
	}
	for _, track := range tracks {
		if strings.TrimSpace(track.TextContent) == "" {
			continue
		}
		var existingID int64
		var existingStatus, existingContent string
		err := tx.QueryRowContext(ctx, `SELECT id,status,text_content FROM asset_text_tracks WHERE asset_id=? AND language_code=? AND text_kind=? AND is_current=1 LIMIT 1`, track.AssetID, track.LanguageCode, track.TextKind).Scan(&existingID, &existingStatus, &existingContent)
		switch {
		case err == nil && (existingStatus != string(detail.TextTrackReady) || strings.TrimSpace(existingContent) == ""):
			_, err = tx.ExecContext(ctx, `UPDATE asset_text_tracks SET text_content=?,source_type=?,source_language_code=?,is_original=?,provider=?,model_name=?,model_version=?,prompt_version=?,text_hash=?,source_version=?,translation_key=?,status=?,updated_at=datetime('now') WHERE id=?`, track.TextContent, track.SourceType, track.SourceLanguageCode, boolInt(track.IsOriginal), track.Provider, track.ModelName, track.ModelVersion, track.PromptVersion, track.TextHash, sourceVersion, track.TranslationKey, string(detail.TextTrackReady), existingID)
		case err == sql.ErrNoRows:
			_, err = tx.ExecContext(ctx, `INSERT INTO asset_text_tracks (asset_id,language_code,text_kind,text_content,source_type,source_language_code,is_original,provider,model_name,model_version,prompt_version,text_hash,source_version,translation_key,is_current,source_track_id,source_text_hash,confidence,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,NULL,'',NULL,?,datetime('now'),datetime('now'))`, track.AssetID, track.LanguageCode, track.TextKind, track.TextContent, track.SourceType, track.SourceLanguageCode, boolInt(track.IsOriginal), track.Provider, track.ModelName, track.ModelVersion, track.PromptVersion, track.TextHash, sourceVersion, track.TranslationKey, string(detail.TextTrackReady))
		case err == nil:
			// A READY canonical track wins over historical recovery text.
			err = nil
		}
		if err != nil {
			return fmt.Errorf("text track %s: %w", track.TextKind, err)
		}
		payload := map[string]any{"asset_id": assetID, "text_kind": track.TextKind, "text_hash": track.TextHash, "source": "qdrant-recovery", "projection": projection}
		body, _ := json.Marshal(payload)
		sum := digest.SHA256Bytes(body)
		eventID := uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO registry_events(event_id,asset_id,event_type,actor,after_hash,payload_json,created_at) VALUES(?,?,?,?,?,?,datetime('now'))`, eventID, assetID, "TEXT_TRACK_RECOVERED", "qdrant-recovery", sum, body); err != nil {
			return fmt.Errorf("registry event: %w", err)
		}
	}
	if _, err := imagesregistry.CommitIndexRequestTx(ctx, tx, c.outbox, imagesregistry.IndexRequest{
		AssetID: assetID, Source: "qdrant-recovery", MediaType: "video",
		SourceVersion: sourceVersion, RequestedAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("targeted reindex outbox: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	rollback = false
	return nil
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

var _ enrichment.RecoveryCommitter = (*RecoveryCommitter)(nil)
