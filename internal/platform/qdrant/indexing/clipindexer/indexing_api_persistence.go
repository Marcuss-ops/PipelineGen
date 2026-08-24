// Package clipindexer — indexing_api_persistence.go: SQLite read/write
// helpers extracted from indexing_api.go (PR-CLIPINDEXER-SPLIT, July 2026).
//
// These helpers are the canonical Go-side readers and writers for
// the embedding columns on media_assets. The Python sidecar no longer
// touches media.db.sqlite (QDRANT-001, June 2026 closure).
package clipindexer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ── SQLite read/write helpers (Go is canonical owner) ──────────────────

func (s *Service) fetchClipSearchInputs(
	ctx context.Context,
	clipID string,
) (searchText, name string, err error) {
	// PR-QDRANT-SEARCH-TEXT-SOURCE-FIX (2026-07-09): read search_text
	// from the COLUMN first, then fall back to metadata_json. YouTube
	// clips (process_segment_step6to9) write search_text to the column
	// via clip_atomic_writer, but metadata_json.search_text is empty.
	// The prior query read only from metadata_json, causing indexViaAPI
	// to generate embeddings from `name` alone (e.g. "Round_7_Broner_barcolla")
	// instead of the rich search text (title + summary + hook + topics +
	// source_url + speakers + mentioned_people). This mismatch between
	// computeContentHash (reads column) and fetchClipSearchInputs (read
	// metadata_json) was the root cause of outbox-completed but empty
	// Qdrant search results for YouTube clips.
	row := s.db.QueryRowContext(ctx, `
SELECT COALESCE(name, ''),
       COALESCE(NULLIF(search_text, ''),
                json_extract(COALESCE(metadata_json, '{}'), '$.search_text'),
                '')
FROM media_assets WHERE id = ?`, clipID)
	if err := row.Scan(&name, &searchText); err != nil {
		return "", "", err
	}
	return searchText, name, nil
}

func (s *Service) lookupTranscriptPath(ctx context.Context, clipID string) string {
	var localPath string
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(local_path, '') FROM media_assets WHERE id = ?`,
		clipID,
	).Scan(&localPath); err != nil || localPath == "" {
		return ""
	}
	candidate := strings.TrimSuffix(localPath, filepath.Ext(localPath)) + ".txt"
	if _, err := os.Stat(candidate); err != nil {
		return ""
	}
	return candidate
}

func (s *Service) persistEmbeddingJSON(
	ctx context.Context, clipID string, embedding []float64,
) error {
	if s.db == nil {
		return fmt.Errorf("clipindexer: db handle is nil")
	}
	raw, err := json.Marshal(embedding)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE media_assets
   SET embedding_json = ?
 WHERE id = ?`, string(raw), clipID)
	return err
}

func (s *Service) persistTranscriptEmbedding(
	ctx context.Context, clipID string, embedding []float64,
) error {
	if s.db == nil {
		return fmt.Errorf("clipindexer: db handle is nil")
	}
	raw, err := json.Marshal(embedding)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE media_assets
   SET transcript_embedding = ?
 WHERE id = ?`, string(raw), clipID)
	return err
}

func (s *Service) persistVisualEmbedding(
	ctx context.Context, clipID string, embedding []float64,
) error {
	if s.db == nil {
		return fmt.Errorf("clipindexer: db handle is nil")
	}
	raw, err := json.Marshal(embedding)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE media_assets
   SET visual_embedding = ?
 WHERE id = ?`, string(raw), clipID)
	return err
}

// persistAudioEmbedding persists the CLAP-HTSAT 512-dim vector to
// media_assets.audio_embedding. The column is JSON-typed TEXT
// (mirroring embedding_json / transcript_embedding / visual_embedding).
// The AssetStore's FetchAsset SQL reads this column into AssetData.AudioVector
// at fetch time, so the next Qdrant upsert picks it up via the canonical
// payload_mapper.IndexDocumentToPoint path (case ChannelAudio).
//
// PR-AUDIO-CHANNEL-EXTENSION (July 2026): the persistence is the
// canonical SSOT for the audio vector. The Qdrant upsert is decoupled
// (outbox media.index.requested → IndexWriter.UpsertFromClips →
// PayloadMapper.AssetToPoint → IndexDocumentToPoint). This helper
// is invoked from indexAudioViaAPI inside the synchronous IndexClip
// path; the Qdrant write is async via the existing outbox contract.
func (s *Service) persistAudioEmbedding(
	ctx context.Context, clipID string, embedding []float64,
) error {
	if s.db == nil {
		return fmt.Errorf("clipindexer: db handle is nil")
	}
	raw, err := json.Marshal(embedding)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE media_assets
   SET audio_embedding = ?
 WHERE id = ?`, string(raw), clipID)
	return err
}
