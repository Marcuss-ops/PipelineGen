package clipindexer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/pkg/hashutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/metrics"
)

const (
	// embeddingModel and embeddingModelVersion are appended to metadata_json
	// as $.embedding_model and $.embedding_model_version when a clip reaches
	// the "indexed" state. They are separate from vectorstore's
	// CurrentEmbeddingVersion / CurrentSearchTextVersion because those
	// track the *content schema* version, whereas these track the
	// *model identity* (which model produced the vector).
	// Bump these when switching models to force re-indexing of all assets.
	embeddingModel        = "multilingual-e5-base"
	embeddingModelVersion = "2026-06-16-v1"

	// collectionVersion tracks the Qdrant collection schema/alias binding.
	// When the collection schema changes (e.g. new named vector, payload
	// field, BM25 tokenization rules), bump this and all clips will be
	// identified as needing re-indexing via content hash mismatch.
	collectionVersion = "v1"
)

// EmbeddingModel returns the current embedding model name.
func EmbeddingModel() string { return embeddingModel }

// EmbeddingModelVersion returns the current embedding model version.
func EmbeddingModelVersion() string { return embeddingModelVersion }

// CollectionVersion returns the current collection version.
func CollectionVersion() string { return collectionVersion }

// isSkippableAssetName reports whether the asset name represents a
// bookkeeping artifact (e.g. the cumulative `metadata.json` sidecar that
// Drive ingest uploads next to each clip) that must NOT be indexed into
// the vector store. Without this guard, every re-ingestion of a clip folder
// reinserts ~1 metadata.json point into Qdrant, polluting semantic search.
//
// "ends with .json" catches any JSON artifact uploaded as media (per-file
// captions, transcript exports, etc.) which is not a real searchable asset.
func isSkippableAssetName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if name == "metadata.json" {
		return true
	}
	return strings.HasSuffix(strings.ToLower(name), ".json")
}

// IndexClip generates embeddings for a clip and upserts it into Qdrant.
// Uses a state machine tracked in metadata_json.index_state:
//
//	pending → embedding → upserting → indexed
//	                      ↘ failed     ↘ retrying
//
// The fast path skips regeneration when BOTH embeddings exist AND the
// content hash matches AND index_state == "indexed".
// Clips without a transcript only require the semantic embedding to be valid.
func (s *Service) IndexClip(ctx context.Context, clipID string) error {
	if !s.cfg.Enabled {
		s.log.Debug("clipindexer disabled, skipping", zap.String("clip_id", clipID))
		return nil
	}

	// Fast early-out: skip metadata-only asset names BEFORE any embedding work.
	// These rows exist in media_assets (sidecars ingested by Drive upload) but
	// are not real searchable media and would just pollute the vector store.
	if skippable, name := s.shouldSkipByName(ctx, clipID); skippable {
		s.log.Debug("skipping indexing for non-media asset name",
			zap.String("clip_id", clipID),
			zap.String("name", name))
		return nil
	}

	contentHash, hasTranscript, err := s.computeContentHash(ctx, clipID)
	if err != nil {
		s.log.Warn("failed to compute content hash, will re-index",
			zap.String("clip_id", clipID), zap.Error(err))
		contentHash = ""
		hasTranscript = false
	}

	if contentHash != "" {
		if s.tryFastPath(ctx, clipID, contentHash, hasTranscript) {
			return nil
		}
	}

	s.setIndexState(ctx, clipID, "embedding", "")

	if s.cfg.ServerURL != "" {
		err := s.indexViaAPI(ctx, clipID)
		if err == nil {
			return s.finalizeIndex(ctx, clipID, contentHash)
		}
		s.log.Warn("embedding server failed, falling back to script",
			zap.String("clip_id", clipID),
			zap.String("server_url", s.cfg.ServerURL),
			zap.Error(err))
	}

	err = s.indexViaScript(ctx, clipID)
	if err != nil {
		s.setIndexState(ctx, clipID, "failed", err.Error())
		return fmt.Errorf("indexViaScript failed for %s: %w", clipID, err)
	}
	return s.finalizeIndex(ctx, clipID, contentHash)
}

func (s *Service) tryFastPath(ctx context.Context, clipID, contentHash string, hasTranscript bool) bool {
	var hasSemantic bool
	var hasTranscriptEmb bool
	var storedHash string
	var indexState string
	err := s.db.QueryRowContext(ctx, `SELECT
		(embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]' AND embedding_json != '{}'),
		(transcript_embedding IS NOT NULL AND transcript_embedding != '' AND transcript_embedding != '[]' AND transcript_embedding != '{}'),
		COALESCE(json_extract(metadata_json, '$.indexed_content_hash'), ''),
		COALESCE(json_extract(metadata_json, '$.index_state'), '')
		FROM media_assets WHERE id = ?`, clipID).Scan(&hasSemantic, &hasTranscriptEmb, &storedHash, &indexState)
	if err != nil {
		s.log.Warn("fast path check failed, will re-index",
			zap.String("clip_id", clipID), zap.Error(err))
		return false
	}

	embeddingsOK := hasSemantic && (hasTranscriptEmb || !hasTranscript)
	if !embeddingsOK || indexState != "indexed" || storedHash != contentHash {
		return false
	}

	s.log.Info("clip already indexed with valid content hash, fast-path upsert",
		zap.String("clip_id", clipID))

	if err := s.UpsertVectorStore(ctx, clipID); err != nil {
		s.setIndexState(ctx, clipID, "retrying", err.Error())
		s.log.Error("fast-path upsert failed", zap.String("clip_id", clipID), zap.Error(err))
		return false
	}

	if setErr := s.setIndexedAt(ctx, clipID, contentHash); setErr != nil {
		s.log.Error("fast-path: failed to persist indexed state, falling through to full re-index",
			zap.String("clip_id", clipID), zap.Error(setErr))
		return false
	}
	return true
}

func (s *Service) finalizeIndex(ctx context.Context, clipID, contentHash string) error {
	s.setIndexState(ctx, clipID, "upserting", "")

	if err := s.UpsertVectorStore(ctx, clipID); err != nil {
		s.setIndexState(ctx, clipID, "failed", err.Error())
		return fmt.Errorf("Qdrant upsert failed for %s: %w", clipID, err)
	}

	if err := s.setIndexedAt(ctx, clipID, contentHash); err != nil {
		return fmt.Errorf("failed to persist indexed state for %s: %w", clipID, err)
	}
	s.log.Info("clip fully indexed and upserted to Qdrant", zap.String("clip_id", clipID))
	return nil
}

// setIndexState atomically updates index_state in metadata_json.
// Increments Prometheus counters for terminal states and StaleAssets
// gauges for stuck-failure transitions (failed / retrying).
func (s *Service) setIndexState(ctx context.Context, clipID, state, lastError string) {
	source := sourceFromClipID(clipID)

	now := time.Now().UTC().Format(time.RFC3339)
	if lastError != "" {
		_, err := s.db.ExecContext(ctx,
			`UPDATE media_assets SET metadata_json = json_set(json_set(json_set(COALESCE(metadata_json,'{}'), '$.index_state', ?), '$.last_index_error', ?), '$.index_state_updated_at', ?) WHERE id = ?`,
			state, lastError, now, clipID)
		if err != nil {
			s.log.Warn("failed to set index state", zap.String("clip_id", clipID), zap.Error(err))
		}
	} else {
		_, err := s.db.ExecContext(ctx,
			`UPDATE media_assets SET metadata_json = json_set(json_set(COALESCE(metadata_json,'{}'), '$.index_state', ?), '$.index_state_updated_at', ?) WHERE id = ?`,
			state, now, clipID)
		if err != nil {
			s.log.Warn("failed to set index state", zap.String("clip_id", clipID), zap.Error(err))
		}
	}

	switch state {
	case "indexed":
		metrics.MediaIndexSuccessTotal.WithLabelValues(source).Inc()
	case "failed":
		metrics.MediaIndexFailureTotal.WithLabelValues(source).Inc()
		metrics.StaleAssets.WithLabelValues(source, "failed").Inc()
	case "retrying":
		metrics.MediaIndexRetryTotal.WithLabelValues(source).Inc()
		metrics.StaleAssets.WithLabelValues(source, "retrying").Inc()
	}

	s.log.Debug("index state transition",
		zap.String("clip_id", clipID),
		zap.String("state", state))
}

func sourceFromClipID(clipID string) string {
	switch {
	case strings.HasPrefix(clipID, "yt_"):
		return "youtube"
	case strings.HasPrefix(clipID, "artlist_"):
		return "artlist"
	case strings.HasPrefix(clipID, "stock_"):
		return "stock"
	case strings.HasPrefix(clipID, "img_"):
		return "image"
	default:
		return "other"
	}
}

// shouldSkipByName loads the asset's `name` column and decides whether to
// short-circuit indexing. Returns (false, "") on lookup errors so the caller
// falls through to the normal index path (better to attempt than to silently
// drop a real clip on a transient SQL blip). A clean sql.ErrNoRows is
// treated as a no-op skip so we do not seed a phantom asset row by accident.
func (s *Service) shouldSkipByName(ctx context.Context, clipID string) (bool, string) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT name FROM media_assets WHERE id = ?`, clipID).Scan(&name)
	if err != nil {
		if err == sql.ErrNoRows {
			s.log.Debug("asset row not found, nothing to index", zap.String("clip_id", clipID))
			return true, ""
		}
		s.log.Warn("could not load asset name for skip-check, will proceed",
			zap.String("clip_id", clipID), zap.Error(err))
		return false, ""
	}
	if isSkippableAssetName(name) {
		return true, name
	}
	return false, name
}

// filterSkippableClipIDs removes any ID whose media_assets.name matches a
// metadata-only pattern, so bulk indexing paths do not waste an embedding
// request on them. Errors fall through to the original slice (safe default).
func (s *Service) filterSkippableClipIDs(ctx context.Context, clipIDs []string) []string {
	if len(clipIDs) == 0 {
		return clipIDs
	}
	placeholders := make([]string, len(clipIDs))
	args := make([]any, len(clipIDs))
	for i, id := range clipIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(
		// Mirror isSkippableAssetName() exactly (case-insensitive on both branches)
		// so a row like `Metadata.JSON` is caught here just as it is by the
		// single-row shouldSkipByName() path.
		"SELECT id FROM media_assets WHERE id IN (%s) AND (LOWER(name) = 'metadata.json' OR LOWER(name) LIKE '%%.json')",
		strings.Join(placeholders, ","),
	)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		s.log.Warn("could not pre-filter skippable clip IDs, proceeding with original batch",
			zap.Int("count", len(clipIDs)), zap.Error(err))
		return clipIDs
	}
	defer rows.Close()

	skippable := make(map[string]struct{}, len(clipIDs))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		skippable[id] = struct{}{}
	}

	if len(skippable) == 0 {
		return clipIDs
	}
	filtered := make([]string, 0, len(clipIDs)-len(skippable))
	for _, id := range clipIDs {
		if _, drop := skippable[id]; !drop {
			filtered = append(filtered, id)
		}
	}
	s.log.Info("filtered skippable JSON-metadata ids from batch",
		zap.Int("total", len(clipIDs)),
		zap.Int("skipped", len(skippable)),
		zap.Int("kept", len(filtered)))
	return filtered
}

func (s *Service) setIndexedAt(ctx context.Context, clipID, contentHash string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`UPDATE media_assets SET metadata_json = json_set(json_set(json_set(json_set(json_set(COALESCE(metadata_json,'{}'), '$.index_state', 'indexed'), '$.indexed_at', ?), '$.indexed_content_hash', ?), '$.embedding_model', ?), '$.embedding_model_version', ?) WHERE id = ?`,
		now, contentHash, embeddingModel, embeddingModelVersion, clipID)
	if err != nil {
		return fmt.Errorf("set indexed_at for %s: %w", clipID, err)
	}
	metrics.MediaIndexSuccessTotal.WithLabelValues(sourceFromClipID(clipID)).Inc()
	return nil
}

func (s *Service) computeContentHash(ctx context.Context, clipID string) (hash string, hasTranscript bool, err error) {
	var name, searchText, cleanTranscript string
	err = s.db.QueryRowContext(ctx,
		`SELECT
			COALESCE(name, ''),
			COALESCE(json_extract(metadata_json, '$.search_text'), ''),
			COALESCE(json_extract(metadata_json, '$.clean_transcript'), '')
		FROM media_assets WHERE id = ?`, clipID).Scan(&name, &searchText, &cleanTranscript)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, fmt.Errorf("clip not found: %s", clipID)
		}
		return "", false, fmt.Errorf("compute content hash: %w", err)
	}
	content := strings.Join([]string{
		"name:" + name,
		"search_text:" + searchText,
		"transcript:" + cleanTranscript,
		"model:" + embeddingModel,
		"model_ver:" + embeddingModelVersion,
		"coll_ver:" + collectionVersion,
		"bm25_ver:" + vectorstore.BM25SchemaVersion,
	}, "|")
	contentParts := strings.SplitN(content, "|model:", 2)
	if len(contentParts) == 2 && strings.TrimSpace(contentParts[0]) == "name:|search_text:|transcript:" {
		return "", false, nil
	}
	return hashutil.SHA256String(content), strings.TrimSpace(cleanTranscript) != "", nil
}

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
	var localPath string
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(json_extract(metadata_json, '$.local_path'), '') FROM media_assets WHERE id = ?`,
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

func (s *Service) indexViaScript(ctx context.Context, clipID string) error {
	select {
	case globalScriptSem <- struct{}{}:
		defer func() { <-globalScriptSem }()
	case <-ctx.Done():
		return ctx.Err()
	}

	var name, localPath string
	err := s.db.QueryRowContext(ctx, "SELECT name, COALESCE(json_extract(metadata_json, '$.local_path'), '') FROM media_assets WHERE id = ?", clipID).Scan(&name, &localPath)
	if err != nil {
		return fmt.Errorf("failed to get clip info: %w", err)
	}

	scriptName := filepath.Base(s.scriptPath)
	args := []string{scriptName}

	if s.dbPath != "" {
		args = append(args, "--db", s.dbPath)
	}
	if name != "" {
		args = append(args, "--clip-name", name)
	}
	if localPath != "" {
		args = append(args, "--clip-path", localPath)
	}
	args = append(args, "--clip-id", clipID)

	cmd := exec.CommandContext(ctx, s.cfg.PythonBin, args...)
	cmd.Dir = filepath.Dir(s.scriptPath)

	s.log.Info("indexing clip via script", zap.String("clip_id", clipID), zap.String("script", s.scriptPath))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to index clip %s: %w, output: %s", clipID, err, strings.TrimSpace(string(output)))
	}

	s.log.Info("clip indexed successfully via script", zap.String("clip_id", clipID))
	return nil
}

func (s *Service) IndexRunItems(ctx context.Context, items []map[string]any) error {
	if !s.cfg.Enabled {
		return nil
	}

	clipIDs := make([]string, 0, len(items))
	for _, item := range items {
		clipID, _ := item["clip_id"].(string)
		if clipID != "" {
			clipIDs = append(clipIDs, clipID)
		}
	}

	if len(clipIDs) == 0 {
		return nil
	}

	// Strip out metadata-only sidecars before we waste an embedding call on them.
	clipIDs = s.filterSkippableClipIDs(ctx, clipIDs)
	if len(clipIDs) == 0 {
		return nil
	}

	if s.cfg.ServerURL != "" {
		err := s.indexBulkAPI(ctx, clipIDs)
		if err == nil {
			if bulkErr := s.UpsertVectorStoreBulk(ctx, clipIDs); bulkErr != nil {
				s.log.Warn("bulk upsert to vector store failed", zap.Error(bulkErr))
				return bulkErr
			}
			return nil
		}
		s.log.Warn("bulk embedding server failed, falling back to individual indexing",
			zap.String("server_url", s.cfg.ServerURL),
			zap.Error(err))
	}

	for _, clipID := range clipIDs {
		if err := s.IndexClip(ctx, clipID); err != nil {
			s.log.Warn("failed to index clip", zap.String("clip_id", clipID), zap.Error(err))
		}
	}
	return nil
}

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
