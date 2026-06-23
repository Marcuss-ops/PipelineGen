package clipindexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// indexViaAPI hits the embedding HTTP server for the three embedding kinds:
// semantic (/index), transcript (/index_transcript), and multi-frame visual
// (/index_visual_multi). All three are best-effort — failures here fall
// through to the script-based indexViaScript fallback.
func (s *Service) indexViaAPI(ctx context.Context, clipID string) error {
	payload := map[string]string{
		"db_path": s.dbPath,
		"clip_id": clipID,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	baseURL := strings.TrimSuffix(s.cfg.ServerURL, "/")
	client := &http.Client{Timeout: 30 * time.Second}

	// === Step 1: Semantic embedding ===
	semanticURL := fmt.Sprintf("%s/index", baseURL)
	semanticReq, err := http.NewRequestWithContext(ctx, "POST", semanticURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	semanticReq.Header.Set("Content-Type", "application/json")

	semanticStart := time.Now()
	resp, err := client.Do(semanticReq)
	semanticElapsed := time.Since(semanticStart).Seconds()
	if err != nil {
		metrics.EmbeddingServerLatency.WithLabelValues("/index", "error").Observe(semanticElapsed)
		return fmt.Errorf("embedding server (%s) unreachable: %w", s.cfg.ServerURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		metrics.EmbeddingServerLatency.WithLabelValues("/index", "error").Observe(semanticElapsed)
		return fmt.Errorf("/index returned status %d", resp.StatusCode)
	}
	metrics.EmbeddingServerLatency.WithLabelValues("/index", "ok").Observe(semanticElapsed)

	s.log.Info("semantic embedding generated via API", zap.String("clip_id", clipID))

	// === Step 2: Transcript embedding ===
	transcriptURL := fmt.Sprintf("%s/index_transcript", baseURL)
	transcriptReq, err := http.NewRequestWithContext(ctx, "POST", transcriptURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	transcriptReq.Header.Set("Content-Type", "application/json")

	transcriptStart := time.Now()
	transcriptResp, err := client.Do(transcriptReq)
	transcriptElapsed := time.Since(transcriptStart).Seconds()
	if err != nil {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_transcript", "error").Observe(transcriptElapsed)
		s.log.Warn("transcript embedding server unreachable (non-fatal)",
			zap.String("clip_id", clipID), zap.Error(err))
		return nil
	}
	transcriptResp.Body.Close()

	if transcriptResp.StatusCode == http.StatusOK {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_transcript", "ok").Observe(transcriptElapsed)
		s.log.Info("transcript embedding generated via API", zap.String("clip_id", clipID))
	} else {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_transcript", "error").Observe(transcriptElapsed)
		s.log.Debug("transcript embedding skipped or failed (non-fatal)",
			zap.String("clip_id", clipID), zap.Int("status", transcriptResp.StatusCode))
	}

	// === Step 3: Multi-frame visual embedding ===
	// local_path is a canonical column (migration 059).
	var localPath string
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(local_path, '') FROM media_assets WHERE id = ?`,
		clipID).Scan(&localPath)

	if localPath != "" {
		s.indexVisualMulti(ctx, clipID, localPath, baseURL, client)
	}

	return nil
}

func (s *Service) indexVisualMulti(ctx context.Context, clipID, localPath, baseURL string, client *http.Client) {
	visualPayload := map[string]any{
		"db_path":         s.dbPath,
		"clip_id":         clipID,
		"video_path":      localPath,
		"frame_positions": []float64{0.2, 0.5, 0.8},
	}
	visualData, err := json.Marshal(visualPayload)
	if err != nil {
		s.log.Warn("failed to marshal visual multi payload", zap.Error(err))
		return
	}

	visualURL := fmt.Sprintf("%s/index_visual_multi", baseURL)
	visualReq, err := http.NewRequestWithContext(ctx, "POST", visualURL, bytes.NewBuffer(visualData))
	if err != nil {
		s.log.Warn("failed to create visual multi request", zap.Error(err))
		return
	}
	visualReq.Header.Set("Content-Type", "application/json")

	visualClient := &http.Client{Timeout: 120 * time.Second}
	visualStart := time.Now()
	visualResp, err := visualClient.Do(visualReq)
	visualElapsed := time.Since(visualStart).Seconds()
	if err != nil {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_visual_multi", "error").Observe(visualElapsed)
		s.log.Warn("multi-frame visual embedding failed (non-fatal)",
			zap.String("clip_id", clipID), zap.Error(err))
		return
	}
	defer visualResp.Body.Close()

	if visualResp.StatusCode == http.StatusOK {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_visual_multi", "ok").Observe(visualElapsed)
		s.log.Info("multi-frame visual embeddings generated",
			zap.String("clip_id", clipID),
			zap.Int("status", visualResp.StatusCode))
	} else {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_visual_multi", "error").Observe(visualElapsed)
		s.log.Debug("multi-frame visual embedding skipped or failed (non-fatal)",
			zap.String("clip_id", clipID), zap.Int("status", visualResp.StatusCode))
	}
}

// indexBulkAPI hits /index_bulk to embed a batch of clips in one request.
// Used by IndexRunItems when a bulk path is preferred over per-clip
// EmbeddingServer calls.
func (s *Service) indexBulkAPI(ctx context.Context, clipIDs []string) error {
	payload := map[string]any{
		"db_path":  s.dbPath,
		"clip_ids": clipIDs,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/index_bulk", strings.TrimSuffix(s.cfg.ServerURL, "/"))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
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

	if resp.StatusCode != http.StatusOK {
		metrics.EmbeddingServerLatency.WithLabelValues("/index_bulk", "error").Observe(elapsed)
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}
	metrics.EmbeddingServerLatency.WithLabelValues("/index_bulk", "ok").Observe(elapsed)

	s.log.Info("bulk indexing completed via API", zap.Int("count", len(clipIDs)))
	return nil
}
