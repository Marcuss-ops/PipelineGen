package clipindexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// indexViaAPI hits the embedding HTTP server for the three embedding kinds:
// semantic (/index), transcript (/index_transcript), and multi-frame visual
// (/index_visual_multi). All three are best-effort — failures here fall
// through to the script-based indexViaScript fallback.
//
// QDRANT-001 (June 2026) closure contract:
//   - The sidecar no longer reads from or writes to media.db.sqlite.
//   - The Go caller (this function) is now the canonical reader/writer:
//   - reads the clip row from SQLite to assemble the input payload,
//   - writes the embedding returned by the sidecar back to SQLite.
//   - The flow matches QDRANT-002 PR4 outbox contract: the embedding is
//     persisted synchronously here, but the Qdrant upsert is decoupled
//     and dispatched via media.index.requested.
func (s *Service) indexViaAPI(ctx context.Context, clipID string) error {
	baseURL := strings.TrimSuffix(s.cfg.ServerURL, "/")
	client := &http.Client{Timeout: 30 * time.Second}

	// === Step 1: Semantic embedding ===
	if err := s.indexTextViaAPI(ctx, clipID, baseURL, client); err != nil {
		return err
	}

	// === Step 2: Transcript embedding (best-effort) ===
	if err := s.indexTranscriptViaAPI(ctx, clipID, baseURL, client); err != nil {
		s.log.Warn("transcript embedding server returned error (non-fatal)",
			zap.String("clip_id", clipID), zap.Error(err))
	}

	// === Step 3: Multi-frame visual embedding (best-effort) ===
	var localPath string
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(local_path, '') FROM media_assets WHERE id = ?`,
		clipID).Scan(&localPath)
	if localPath != "" {
		s.indexVisualMultiViaAPI(ctx, clipID, localPath, baseURL, client)
	}

	return nil
}

// indexTextViaAPI calls POST /index on the embedding sidecar and persists
// the returned embedding JSON into media_assets.embedding_json.
func (s *Service) indexTextViaAPI(
	ctx context.Context,
	clipID, baseURL string,
	client *http.Client,
) error {
	searchText, name, err := s.fetchClipSearchInputs(ctx, clipID)
	if err != nil {
		return fmt.Errorf("read clip row for /index: %w", err)
	}

	text := searchText
	if text == "" {
		text = name
	}
	if text == "" {
		return fmt.Errorf("clip %s has neither search_text nor name", clipID)
	}

	payload := map[string]any{
		"clip_id":     clipID,
		"name":        name,
		"search_text": text,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/index", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start).Seconds()
	if err != nil {
		metrics.EmbeddingServerLatency.WithLabelValues("/index", "error").Observe(elapsed)
		return fmt.Errorf("embedding server (%s) unreachable: %w", s.cfg.ServerURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGone {
		metrics.EmbeddingServerLatency.WithLabelValues("/index", "ok").Observe(elapsed)
		s.log.Info("/index retired (QDRANT-001) — skipping",
			zap.String("clip_id", clipID))
		return nil
	}

	bodyMap, bodyRaw := readJSONResponse(resp, "/index")
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/index returned status %d: %s", resp.StatusCode, bodyRaw)
	}

	embedding, err := extractEmbedding(bodyMap)
	if err != nil {
		return fmt.Errorf("/index response: %w", err)
	}
	if err := s.persistEmbeddingJSON(ctx, clipID, embedding); err != nil {
		return fmt.Errorf("/index persist: %w", err)
	}
	s.log.Info("semantic embedding generated via API and persisted",
		zap.String("clip_id", clipID))
	return nil
}

// indexTranscriptViaAPI calls POST /index_transcript and persists to
// media_assets.transcript_embedding. Best-effort.
func (s *Service) indexTranscriptViaAPI(
	ctx context.Context,
	clipID, baseURL string,
	client *http.Client,
) error {
	transcriptPath := s.lookupTranscriptPath(ctx, clipID)
	if transcriptPath == "" {
		s.log.Debug("transcript skipped (no .txt sidecar)",
			zap.String("clip_id", clipID))
		return nil
	}

	payload := map[string]any{
		"clip_id":         clipID,
		"transcript_path": transcriptPath,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/index_transcript", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start).Seconds()
	if err != nil {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_transcript", "error").Observe(elapsed)
		return fmt.Errorf("transcript server unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGone {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_transcript", "ok").Observe(elapsed)
		s.log.Info("/index_transcript retired (QDRANT-001) — skipping",
			zap.String("clip_id", clipID))
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_transcript", "error").Observe(elapsed)
		_, raw := readJSONResponse(resp, "/index_transcript")
		s.log.Debug("transcript not generated",
			zap.String("clip_id", clipID),
			zap.Int("status", resp.StatusCode),
			zap.String("body", raw))
		return nil
	}

	metrics.EmbeddingServerLatency.WithLabelValues("/index_transcript", "ok").Observe(elapsed)

	bodyMap, _ := readJSONResponse(resp, "/index_transcript")
	embedding, err := extractEmbedding(bodyMap)
	if err != nil {
		s.log.Debug("transcript response lacks embedding",
			zap.String("clip_id", clipID))
		return nil
	}
	if err := s.persistTranscriptEmbedding(ctx, clipID, embedding); err != nil {
		return fmt.Errorf("/index_transcript persist: %w", err)
	}
	s.log.Info("transcript embedding generated via API and persisted",
		zap.String("clip_id", clipID))
	return nil
}

// indexVisualMultiViaAPI calls POST /index_visual_multi and persists the
// averaged embedding into media_assets.visual_embedding.
func (s *Service) indexVisualMultiViaAPI(
	ctx context.Context,
	clipID, videoPath, baseURL string,
	client *http.Client,
) {
	payload := map[string]any{
		"clip_id":         clipID,
		"video_path":      videoPath,
		"frame_positions": []float64{0.2, 0.5, 0.8},
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		s.log.Warn("failed to marshal visual multi payload", zap.Error(err))
		return
	}

	url := fmt.Sprintf("%s/index_visual_multi", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonData))
	if err != nil {
		s.log.Warn("failed to create visual multi request", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	visualClient := &http.Client{Timeout: 120 * time.Second}
	start := time.Now()
	resp, err := visualClient.Do(req)
	elapsed := time.Since(start).Seconds()
	if err != nil {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_visual_multi", "error").Observe(elapsed)
		s.log.Warn("multi-frame visual embedding failed (non-fatal)",
			zap.String("clip_id", clipID), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGone {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_visual_multi", "ok").Observe(elapsed)
		s.log.Info("/index_visual_multi retired (QDRANT-001) — skipping",
			zap.String("clip_id", clipID))
		return
	}

	if resp.StatusCode != http.StatusOK {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_visual_multi", "error").Observe(elapsed)
		_, raw := readJSONResponse(resp, "/index_visual_multi")
		s.log.Debug("multi-frame visual skipped or failed (non-fatal)",
			zap.String("clip_id", clipID),
			zap.Int("status", resp.StatusCode),
			zap.String("body", raw))
		return
	}

	metrics.EmbeddingServerLatency.WithLabelValues("/index_visual_multi", "ok").Observe(elapsed)

	bodyMap, _ := readJSONResponse(resp, "/index_visual_multi")
	if embedding, err := extractEmbedding(bodyMap); err == nil {
		if err := s.persistVisualEmbedding(ctx, clipID, embedding); err != nil {
			s.log.Warn("/index_visual_multi persist failed (non-fatal)",
				zap.String("clip_id", clipID), zap.Error(err))
			return
		}
		s.log.Info("multi-frame visual embedding generated and persisted",
			zap.String("clip_id", clipID))
		return
	}
	averaged, err := extractEmbeddingField(bodyMap, "averaged_embedding")
	if err != nil {
		// Fall back to averaging frame_embeddings.
		frames, _ := bodyMap["frame_embeddings"].([]any)
		if len(frames) == 0 {
			s.log.Debug("visual response missing embeddings",
				zap.String("clip_id", clipID))
			return
		}
		avg, avgErr := averageFrameEmbeddings(frames)
		if avgErr != nil {
			s.log.Debug("visual frame averaging failed",
				zap.String("clip_id", clipID), zap.Error(avgErr))
			return
		}
		averaged = avg
	}
	if err := s.persistVisualEmbedding(ctx, clipID, averaged); err != nil {
		s.log.Warn("/index_visual_multi persist failed (non-fatal)",
			zap.String("clip_id", clipID), zap.Error(err))
		return
	}
	s.log.Info("multi-frame visual embeddings generated and persisted",
		zap.String("clip_id", clipID))
}

// indexBulkAPI hits /index_bulk to embed a batch of clips in one request.
// Reads each clip's name + search_text from SQLite (Go is canonical),
// sends them to the sidecar, and writes each returned embedding back.
func (s *Service) indexBulkAPI(ctx context.Context, clipIDs []string) error {
	if len(clipIDs) == 0 {
		return nil
	}
	clips := make([]map[string]any, 0, len(clipIDs))
	for _, clipID := range clipIDs {
		var name, searchText string
		err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(name, ''), json_extract(COALESCE(metadata_json, '{}'), '$.search_text') FROM media_assets WHERE id = ?`,
			clipID,
		).Scan(&name, &searchText)
		if err != nil {
			s.log.Warn("bulk read clip failed; skipping",
				zap.String("clip_id", clipID), zap.Error(err))
			continue
		}
		clips = append(clips, map[string]any{
			"clip_id":     clipID,
			"name":        name,
			"search_text": searchText,
		})
	}

	if len(clips) == 0 {
		return nil
	}

	payload := map[string]any{"clips": clips}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/index_bulk", strings.TrimSuffix(s.cfg.ServerURL, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start).Seconds()
	if err != nil {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_bulk", "error").Observe(elapsed)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGone {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_bulk", "ok").Observe(elapsed)
		s.log.Info("/index_bulk retired (QDRANT-001) — skipping")
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_bulk", "error").Observe(elapsed)
		_, raw := readJSONResponse(resp, "/index_bulk")
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, raw)
	}
	metrics.EmbeddingServerLatency.WithLabelValues("/index_bulk", "ok").Observe(elapsed)

	bodyMap, _ := readJSONResponse(resp, "/index_bulk")
	results, _ := bodyMap["results"].([]any)
	persisted := 0
	for _, item := range results {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		embed, err := extractEmbeddingField(m, "embedding")
		if err != nil {
			continue
		}
		clipIDLocal, _ := m["clip_id"].(string)
		if clipIDLocal == "" {
			continue
		}
		if err := s.persistEmbeddingJSON(ctx, clipIDLocal, embed); err != nil {
			s.log.Warn("bulk persist failed",
				zap.String("clip_id", clipIDLocal), zap.Error(err))
			continue
		}
		persisted++
	}
	s.log.Info("bulk indexing completed via API and persisted",
		zap.Int("count", persisted),
		zap.Int("requested", len(clipIDs)))
	return nil
}

// ── SQLite read/write helpers (Go is canonical owner) ──────────────────

func (s *Service) fetchClipSearchInputs(
	ctx context.Context,
	clipID string,
) (searchText, name string, err error) {
	row := s.db.QueryRowContext(ctx, `
SELECT COALESCE(name, ''),
       COALESCE(json_extract(COALESCE(metadata_json, '{}'), '$.search_text'), '')
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
