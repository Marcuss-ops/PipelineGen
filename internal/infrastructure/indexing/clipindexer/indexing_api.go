package clipindexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// indexViaAPI hits the embedding HTTP server for the four embedding kinds:
// semantic (/index), transcript (/index_transcript), multi-frame visual
// (/index_visual_multi), and audio (/embed_audio_from_file). Only Step 1
// is fatal — Steps 2/3/4 are best-effort and fall through to the
// The API is the only embedding path. If it is unavailable the caller
// returns a transient error so the canonical job retry can recover it.
//
// QDRANT-001 (June 2026) closure contract:
//   - The sidecar no longer reads from or writes to media.db.sqlite.
//   - The Go caller (this function) is now the canonical reader/writer:
//   - reads the clip row from SQLite to assemble the input payload,
//   - writes the embedding returned by the sidecar back to SQLite.
//   - The flow matches QDRANT-002 PR4 outbox contract: the embedding is
//     persisted synchronously here, but the Qdrant upsert is decoupled
//     and dispatched via media.index.requested.
//
// PR-AUDIO-CHANNEL-EXTENSION (July 2026): Step 4 was added to pop the
// CLAP-HTSAT 512-dim vector into the Qdrant audio channel. The
// Qdrant schema (internal/infrastructure/qdrant/schema/schema.go
// ::DefaultV3Schema) already declares the audio channel; the DB
// column media_assets.audio_embedding already exists (migration 099);
// the AssetStore + PayloadMapper already read AudioVector (drift-safe
// per QDRANT-003). The Phase 4 best-effort fail-soft contract mirrors
// Step 3 (visual): HTTP 501 (CLAP model not loaded) and HTTP 410
// (endpoint retired) are logged at INFO and the channel is silently
// skipped — the clip remains valid for the other 3 channels per
// godlike/07 no-fake-availability.
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

	// Read local_path once — used by both Step 3 (visual) and Step 4 (audio).
	// The same local_path is the canonical source for both channels (the video
	// file IS the audio track source); no per-channel path resolution needed.
	var localPath string
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(local_path, '') FROM media_assets WHERE id = ?`,
		clipID).Scan(&localPath)

	// === Steps 3+4: Multi-frame visual + CLAP audio (best-effort, fail-soft) ===
	// Both channels share the same local_path (the video file IS the
	// audio track source), so they fan out under a single guard.
	if localPath != "" {
		// Step 3: visual multi-frame (per-frame averaging, 120s timeout).
		if !visualEmbeddingDisabled() {
			s.indexVisualMultiViaAPI(ctx, clipID, localPath, baseURL, client)
		}
		// Step 4: CLAP audio (PR-AUDIO-CHANNEL-EXTENSION, July 2026).
		// Queries like "suono di pioggia" or "musica drammatica" route
		// through the Qdrant audio channel.
		s.indexAudioViaAPI(ctx, clipID, localPath, baseURL, client)
	}

	return nil
}

func visualEmbeddingDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("VELOX_DISABLE_VISUAL_EMBEDDING"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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

// indexAudioViaAPI calls POST /embed_audio_from_file and persists the
// returned CLAP-HTSAT 512-dim embedding into media_assets.audio_embedding.
//
// PR-AUDIO-CHANNEL-EXTENSION (July 2026): this is Phase 4 of the canonical
// indexViaAPI 4-phase surface. The audio channel is OPTIONAL in the
// Qdrant v3 schema (clap-htsat-fused, 512d, Cosine, normalized); the
// Python sidecar's /embed_audio_from_file endpoint returns the CLAP
// vector OR HTTP 501 if the CLAP model is not loaded on the sidecar
// (e.g. the operator chose not to deploy the model).
//
// Fail-soft contract (per godlike/07 no-fake-availability):
//   - HTTP 200 → extract embedding, persist to media_assets.audio_embedding.
//   - HTTP 501 → CLAP model not loaded; log INFO + return (channel dropped).
//   - HTTP 410 → endpoint retired; log INFO + return (channel dropped).
//   - HTTP != 200/501/410 → log WARN + return (best-effort, like visual).
//   - Network error → log WARN + return (best-effort, like visual).
//
// The audio file path is the same local_path used by the visual phase
// (the video file IS the audio track source). No per-channel path
// resolution is needed.
//
// Phase 4 is intentionally void-return + best-effort — failing the
// audio channel MUST NOT fail the entire IndexClip. The clip remains
// valid for the other 3 channels (text / transcript / visual) per
// godlike/07. The Qdrant audio channel is dropped on the payload
// mapper side when audio_embedding is empty (see payload_mapper.go
// ::IndexDocumentToPoint case ChannelAudio).
func (s *Service) indexAudioViaAPI(
	ctx context.Context,
	clipID, audioPath, baseURL string,
	client *http.Client,
) {
	// CLAP inference is single-pass (no multi-frame averaging like
	// visual multi) so a 60s timeout is generous; the default 30s
	// client is upgraded to 60s to absorb model-load latency on a
	// cold sidecar start.
	audioClient := &http.Client{Timeout: 60 * time.Second}

	payload := map[string]any{
		"clip_id":    clipID,
		"audio_path": audioPath,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		s.log.Warn("failed to marshal audio embed payload",
			zap.String("clip_id", clipID), zap.Error(err))
		return
	}

	url := fmt.Sprintf("%s/embed_audio_from_file", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonData))
	if err != nil {
		s.log.Warn("failed to create audio embed request",
			zap.String("clip_id", clipID), zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := audioClient.Do(req)
	elapsed := time.Since(start).Seconds()
	if err != nil {
		metrics.EmbeddingServerLatency.WithLabelValues("/embed_audio_from_file", "error").Observe(elapsed)
		s.log.Warn("audio embedding server unreachable (non-fatal)",
			zap.String("clip_id", clipID), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	// HTTP 410: endpoint retired (QDRANT-001 contract). Mirror the
	// /index retirement pattern — log INFO, do not retry, channel
	// dropped silently.
	if resp.StatusCode == http.StatusGone {
		metrics.EmbeddingServerLatency.WithLabelValues("/embed_audio_from_file", "ok").Observe(elapsed)
		s.log.Info("/embed_audio_from_file retired (QDRANT-001) — audio channel dropped",
			zap.String("clip_id", clipID))
		return
	}

	// HTTP 501: CLAP model not loaded on the sidecar. Per
	// scripts/services/embedding_server/audio.py the sidecar
	// returns 501 when the CLAP model failed to load. The clip
	// remains valid for the other 3 channels.
	if resp.StatusCode == http.StatusNotImplemented {
		metrics.EmbeddingServerLatency.WithLabelValues("/embed_audio_from_file", "ok").Observe(elapsed)
		s.log.Info("CLAP audio model not loaded on sidecar (HTTP 501) — audio channel dropped",
			zap.String("clip_id", clipID))
		return
	}

	if resp.StatusCode != http.StatusOK {
		metrics.EmbeddingServerLatency.WithLabelValues("/embed_audio_from_file", "error").Observe(elapsed)
		_, raw := readJSONResponse(resp, "/embed_audio_from_file")
		s.log.Debug("audio embedding skipped or failed (non-fatal)",
			zap.String("clip_id", clipID),
			zap.Int("status", resp.StatusCode),
			zap.String("body", raw))
		return
	}

	metrics.EmbeddingServerLatency.WithLabelValues("/embed_audio_from_file", "ok").Observe(elapsed)

	bodyMap, _ := readJSONResponse(resp, "/embed_audio_from_file")
	embedding, err := extractEmbedding(bodyMap)
	if err != nil {
		s.log.Debug("audio response missing embedding",
			zap.String("clip_id", clipID), zap.Error(err))
		return
	}
	if err := s.persistAudioEmbedding(ctx, clipID, embedding); err != nil {
		s.log.Warn("/embed_audio_from_file persist failed (non-fatal)",
			zap.String("clip_id", clipID), zap.Error(err))
		return
	}
	s.log.Info("CLAP audio embedding generated and persisted",
		zap.String("clip_id", clipID),
		zap.Int("dimensions", len(embedding)))
}

// ── SQLite read/write helpers ──
// fetchClipSearchInputs, lookupTranscriptPath, persistEmbeddingJSON,
// persistTranscriptEmbedding, persistVisualEmbedding, persistAudioEmbedding
// — extracted to indexing_api_persistence.go (PR-CLIPINDEXER-SPLIT, July 2026).
