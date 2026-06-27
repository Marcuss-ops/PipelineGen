package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

type qdrantVisualBackfillDeps struct {
	Apply bool
	JSON  bool
	Limit int
}

type qdrantVisualBackfillReport struct {
	Mode              string `json:"mode"`
	TotalAssets       int    `json:"total_assets"`
	Already768        int    `json:"already_768"`
	Legacy512         int    `json:"legacy_512"`
	Regenerated       int    `json:"regenerated"`
	Failed            int    `json:"failed"`
	MissingSourceFile int    `json:"missing_source_file"`
	UnsupportedMedia  int    `json:"unsupported_media"`
}

type qdrantReadinessReport struct {
	Ready                      bool     `json:"ready"`
	QdrantReachable            bool     `json:"qdrant_reachable"`
	SQLiteMigrationsComplete   bool     `json:"sqlite_migrations_complete"`
	ActiveCollection           string   `json:"active_collection,omitempty"`
	ActiveCollectionCompatible bool     `json:"active_collection_compatible"`
	RequiredColumnsPresent     []string `json:"required_columns_present,omitempty"`
	MissingColumns             []string `json:"missing_columns,omitempty"`
	TotalAssets                int      `json:"total_assets"`
	NonMediaAssets             int      `json:"non_media_assets"`
	InvalidTextVectors         int      `json:"invalid_text_vectors"`
	InvalidTranscriptVectors   int      `json:"invalid_transcript_vectors"`
	InvalidVisualVectors       int      `json:"invalid_visual_vectors"`
	InvalidAudioVectors        int      `json:"invalid_audio_vectors"`
	SchemaErrors               int      `json:"schema_errors"`
	MissingSourceFile          int      `json:"missing_source_file"`
	LegacyStatusRows           int      `json:"legacy_status_rows"`
	LegacyLocatorRows          int      `json:"legacy_locator_rows"`
	OutboxOperational          bool     `json:"outbox_operational"`
}

func runBackfillVisualEmbeddings(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseQdrantVisualBackfillArgs(args)
	if err != nil {
		return err
	}

	ctx := cmdContext()
	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	report, err := backfillVisualEmbeddings(ctx, sqliteDB.DB, cfg, deps, log)
	if err != nil {
		return err
	}

	if deps.JSON {
		b, _ := json.Marshal(report)
		fmt.Println(string(b))
		return nil
	}

	if !deps.Apply {
		log.Info("visual embedding backfill dry-run complete",
			zap.Int("total_assets", report.TotalAssets),
			zap.Int("already_768", report.Already768),
			zap.Int("legacy_512", report.Legacy512),
			zap.Int("missing_source_file", report.MissingSourceFile),
			zap.Int("unsupported_media", report.UnsupportedMedia))
		return nil
	}

	log.Info("visual embedding backfill complete",
		zap.Int("total_assets", report.TotalAssets),
		zap.Int("already_768", report.Already768),
		zap.Int("legacy_512", report.Legacy512),
		zap.Int("regenerated", report.Regenerated),
		zap.Int("failed", report.Failed),
		zap.Int("missing_source_file", report.MissingSourceFile),
		zap.Int("unsupported_media", report.UnsupportedMedia))
	return nil
}

func runQdrantReadiness(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	jsonOut := false
	for _, a := range args {
		switch strings.TrimSpace(a) {
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(strings.TrimSpace(a), "-") {
				return fmt.Errorf("unknown flag: %s", a)
			}
		}
	}

	ctx := cmdContext()
	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	report, err := qdrantReadiness(ctx, sqliteDB.DB, cfg, log)
	if err != nil {
		return err
	}

	if jsonOut {
		b, _ := json.Marshal(report)
		fmt.Println(string(b))
	} else {
		fmt.Printf("READY=%t\n", report.Ready)
		fmt.Printf("qdrant_reachable=%t\n", report.QdrantReachable)
		fmt.Printf("sqlite_migrations_complete=%t\n", report.SQLiteMigrationsComplete)
		fmt.Printf("active_collection=%s\n", report.ActiveCollection)
		fmt.Printf("active_collection_compatible=%t\n", report.ActiveCollectionCompatible)
		fmt.Printf("required_columns_present=%d\n", len(report.RequiredColumnsPresent))
		fmt.Printf("missing_columns=%d\n", len(report.MissingColumns))
		fmt.Printf("total_assets=%d\n", report.TotalAssets)
		fmt.Printf("non_media_assets=%d\n", report.NonMediaAssets)
		fmt.Printf("invalid_text_vectors=%d\n", report.InvalidTextVectors)
		fmt.Printf("invalid_transcript_vectors=%d\n", report.InvalidTranscriptVectors)
		fmt.Printf("invalid_visual_vectors=%d\n", report.InvalidVisualVectors)
		fmt.Printf("invalid_audio_vectors=%d\n", report.InvalidAudioVectors)
		fmt.Printf("schema_errors=%d\n", report.SchemaErrors)
		fmt.Printf("missing_source_file=%d\n", report.MissingSourceFile)
		fmt.Printf("legacy_status_rows=%d\n", report.LegacyStatusRows)
		fmt.Printf("legacy_locator_rows=%d\n", report.LegacyLocatorRows)
		fmt.Printf("outbox_operational=%t\n", report.OutboxOperational)
	}

	if !report.Ready {
		return fmt.Errorf("qdrant readiness gate failed")
	}
	return nil
}

func parseQdrantVisualBackfillArgs(args []string) (qdrantVisualBackfillDeps, error) {
	deps := qdrantVisualBackfillDeps{}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
		case a == "--apply":
			deps.Apply = true
		case a == "--json":
			deps.JSON = true
		case strings.HasPrefix(a, "--limit="):
			n, err := parsePositiveFlag(a, "--limit")
			if err != nil {
				return deps, err
			}
			deps.Limit = n
		default:
			if strings.HasPrefix(a, "-") {
				return deps, fmt.Errorf("unknown flag: %s", a)
			}
		}
	}
	return deps, nil
}

func backfillVisualEmbeddings(ctx context.Context, db *sql.DB, cfg *config.Config, deps qdrantVisualBackfillDeps, log *zap.Logger) (qdrantVisualBackfillReport, error) {
	report := qdrantVisualBackfillReport{
		Mode: "dry-run",
	}
	if deps.Apply {
		report.Mode = "apply"
	}

	const supportedQuery = `
		SELECT id, COALESCE(media_type, ''), COALESCE(local_path, ''), COALESCE(visual_embedding, '')
		FROM media_assets
		WHERE COALESCE(media_type, '') IN ('video', 'image')
		ORDER BY id`

	rows, err := db.QueryContext(ctx, supportedQuery)
	if err != nil {
		return report, fmt.Errorf("query visual backfill candidates: %w", err)
	}
	defer rows.Close()

	ffmpegProc := ffmpeg.NewFromConfig(cfg)
	schema := qdrant.DefaultV3Schema()
	imageEmbedder := qdrant.NewImageEmbedderAdapter(qdrant.ImageEmbedderConfig{
		ServerURL: cfg.ClipIndexer.ServerURL,
		Timeout:   90 * time.Second,
	}, schema, log)
	visualVersion := ""
	if spec := schema.GetDense("visual"); spec != nil {
		visualVersion = spec.ModelVersion
	}

	for rows.Next() {
		var id, mediaType, localPath, visualEmbedding string
		if err := rows.Scan(&id, &mediaType, &localPath, &visualEmbedding); err != nil {
			return report, fmt.Errorf("scan visual backfill row: %w", err)
		}
		if deps.Limit > 0 && report.TotalAssets >= deps.Limit {
			break
		}

		report.TotalAssets++
		normalizedType := strings.ToLower(strings.TrimSpace(mediaType))
		if normalizedType != "video" && normalizedType != "image" {
			report.UnsupportedMedia++
			continue
		}

		_, vecLen, vecErr := parseVectorLen(visualEmbedding)
		switch {
		case vecErr != nil:
			report.Legacy512++
		case vecLen == 768:
			report.Already768++
			continue
		case vecLen == 512:
			report.Legacy512++
		default:
			report.Legacy512++
		}

		if !deps.Apply {
			continue
		}

		if strings.TrimSpace(localPath) == "" {
			report.MissingSourceFile++
			report.Failed++
			continue
		}
		if _, err := os.Stat(localPath); err != nil {
			report.MissingSourceFile++
			report.Failed++
			continue
		}

		newVec, err := regenerateVisualEmbedding(ctx, ffmpegProc, imageEmbedder, id, mediaType, localPath)
		if err != nil {
			report.Failed++
			log.Warn("visual backfill failed", zap.String("asset_id", id), zap.String("path", localPath), zap.Error(err))
			continue
		}
		if len(newVec) != 768 {
			report.Failed++
			log.Warn("visual backfill produced unexpected dimension", zap.String("asset_id", id), zap.Int("dims", len(newVec)))
			continue
		}

		raw, err := json.Marshal(newVec)
		if err != nil {
			report.Failed++
			continue
		}

		metaJSON := "{}"
		var meta map[string]any
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(metadata_json, '{}') FROM media_assets WHERE id = ?`, id).Scan(&metaJSON); err == nil {
			_ = json.Unmarshal([]byte(metaJSON), &meta)
		}
		if meta == nil {
			meta = make(map[string]any)
		}
		if visualVersion != "" {
			meta["embedding_version_visual"] = visualVersion
		}
		metaBytes, _ := json.Marshal(meta)

		if _, err := db.ExecContext(ctx, `
			UPDATE media_assets
			SET visual_embedding = ?, metadata_json = ?, updated_at = datetime('now')
			WHERE id = ?`,
			string(raw), string(metaBytes), id); err != nil {
			report.Failed++
			log.Warn("visual backfill update failed", zap.String("asset_id", id), zap.Error(err))
			continue
		}
		report.Regenerated++
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("iterate visual backfill rows: %w", err)
	}

	return report, nil
}

func qdrantReadiness(ctx context.Context, db *sql.DB, cfg *config.Config, log *zap.Logger) (qdrantReadinessReport, error) {
	report := qdrantReadinessReport{}

	requiredColumns := []string{
		"audio_embedding",
		"youtube_video_id",
		"youtube_url",
		"start_time",
		"end_time",
		"workspace_id",
		"channel_id",
		"license",
		"source_version",
		"style",
	}
	present, missing, err := inspectRequiredColumns(ctx, db, requiredColumns)
	if err != nil {
		return report, err
	}
	report.RequiredColumnsPresent = present
	report.MissingColumns = missing
	report.SQLiteMigrationsComplete = len(missing) == 0
	if len(missing) > 0 {
		report.SchemaErrors += len(missing)
	}

	if qErr := qdrantProbeAndSchema(ctx, cfg, log, &report); qErr != nil {
		return report, qErr
	}

	if err := collectReadinessCounters(ctx, db, &report); err != nil {
		return report, err
	}

	report.OutboxOperational = tableExists(ctx, db, "outbox_events")
	report.Ready = report.QdrantReachable &&
		report.SQLiteMigrationsComplete &&
		report.ActiveCollectionCompatible &&
		report.SchemaErrors == 0 &&
		report.NonMediaAssets == 0 &&
		report.InvalidTextVectors == 0 &&
		report.InvalidTranscriptVectors == 0 &&
		report.InvalidVisualVectors == 0 &&
		report.InvalidAudioVectors == 0 &&
		report.MissingSourceFile == 0 &&
		report.LegacyStatusRows == 0 &&
		report.LegacyLocatorRows == 0 &&
		report.OutboxOperational

	return report, nil
}

func inspectRequiredColumns(ctx context.Context, db *sql.DB, required []string) ([]string, []string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(media_assets)`)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect media_assets columns: %w", err)
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &defaultValue, &pk); err != nil {
			return nil, nil, fmt.Errorf("scan pragma table_info: %w", err)
		}
		seen[strings.ToLower(name)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	present := make([]string, 0, len(required))
	missing := make([]string, 0)
	for _, col := range required {
		if _, ok := seen[strings.ToLower(col)]; ok {
			present = append(present, col)
		} else {
			missing = append(missing, col)
		}
	}
	return present, missing, nil
}

func collectReadinessCounters(ctx context.Context, db *sql.DB, report *qdrantReadinessReport) error {
	rows, err := db.QueryContext(ctx, `
		SELECT
			id,
			COALESCE(media_type, ''),
			COALESCE(local_path, ''),
			COALESCE(embedding_json, ''),
			COALESCE(transcript_embedding, ''),
			COALESCE(visual_embedding, ''),
			COALESCE(audio_embedding, ''),
			COALESCE(status, ''),
			COALESCE(lifecycle_state, ''),
			COALESCE(metadata_json, '{}')
		FROM media_assets
		ORDER BY id`)
	if err != nil {
		return fmt.Errorf("readiness scan: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, mediaType, localPath, textJSON, transcriptJSON, visualJSON, audioJSON, status, lifecycleState, metaJSON string
		if err := rows.Scan(&id, &mediaType, &localPath, &textJSON, &transcriptJSON, &visualJSON, &audioJSON, &status, &lifecycleState, &metaJSON); err != nil {
			return fmt.Errorf("scan readiness row: %w", err)
		}
		report.TotalAssets++

		switch strings.ToLower(strings.TrimSpace(mediaType)) {
		case "video", "audio", "image":
		default:
			report.NonMediaAssets++
		}

		if _, dim, err := parseVectorLen(textJSON); err != nil || dim != 768 {
			report.InvalidTextVectors++
		}
		if _, dim, err := parseVectorLen(transcriptJSON); err != nil || dim != 768 {
			report.InvalidTranscriptVectors++
		}
		if _, dim, err := parseVectorLen(visualJSON); err != nil || dim != 768 {
			report.InvalidVisualVectors++
		}
		if _, dim, err := parseVectorLen(audioJSON); err != nil || dim != 512 {
			report.InvalidAudioVectors++
		}

		if strings.TrimSpace(localPath) == "" {
			report.MissingSourceFile++
		}
		if status != "" && !strings.EqualFold(status, lifecycleState) && lifecycleState != "" {
			report.LegacyStatusRows++
		}
		if hasLegacyLocatorKey(metaJSON) {
			report.LegacyLocatorRows++
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate readiness rows: %w", err)
	}

	return nil
}

func qdrantProbeAndSchema(ctx context.Context, cfg *config.Config, log *zap.Logger, report *qdrantReadinessReport) error {
	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		Timeout: cfg.Qdrant.Timeout,
		APIKey:  cfg.Qdrant.APIKey,
	}, log)
	probe := qdrant.NewHealthProbe(client)
	if err := probe.Probe(ctx); err != nil {
		report.QdrantReachable = false
		return fmt.Errorf("qdrant health probe failed: %w", err)
	}
	report.QdrantReachable = true

	schema := qdrant.DefaultV3Schema()
	mgr := qdrant.NewCollectionManager(client, schema, log)
	active, err := mgr.GetActiveCollection(ctx)
	if err != nil {
		return fmt.Errorf("resolve active collection: %w", err)
	}
	report.ActiveCollection = active
	if active == "" {
		return fmt.Errorf("qdrant runtime alias %q has no target", schema.RuntimeAlias)
	}
	diff, err := mgr.CompareActiveCollection(ctx)
	if err != nil {
		return fmt.Errorf("compare active collection: %w", err)
	}
	report.ActiveCollectionCompatible = diff.Compatible
	if !diff.Compatible {
		report.SchemaErrors++
	}
	return nil
}

func parseVectorLen(raw string) ([]float32, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "{}" {
		return nil, 0, fmt.Errorf("empty vector")
	}
	var vec []float32
	if err := json.Unmarshal([]byte(raw), &vec); err != nil {
		return nil, 0, err
	}
	return vec, len(vec), nil
}

func regenerateVisualEmbedding(ctx context.Context, ffmpegProc *ffmpeg.Processor, embedder qdrant.ImageEmbedder, assetID, mediaType, localPath string) ([]float32, error) {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image":
		vecs, err := embedder.EmbedImages(ctx, []string{localPath})
		if err != nil {
			return nil, err
		}
		if len(vecs) == 0 {
			return nil, fmt.Errorf("no embedding returned for image")
		}
		return vecs[0], nil
	case "video":
		info, err := ffmpegProc.Probe(ctx, localPath)
		if err != nil {
			return nil, err
		}
		duration := info.Duration.Seconds()
		if duration <= 0 {
			duration = 1
		}
		timestamps := make([]float64, 0, int(math.Ceil(duration/2.0)))
		for ts := 1.0; ts < duration; ts += 2.0 {
			timestamps = append(timestamps, ts)
		}
		if len(timestamps) == 0 {
			timestamps = []float64{duration / 2.0}
		}
		tmpDir, err := os.MkdirTemp("", "qdrant-visual-backfill-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmpDir)

		embeddings := make([][]float32, 0, len(timestamps))
		for i, ts := range timestamps {
			framePath := filepath.Join(tmpDir, fmt.Sprintf("%s_%03d.png", assetID, i))
			if err := ffmpegProc.ExtractFrame(ctx, localPath, framePath, ts); err != nil {
				return nil, fmt.Errorf("extract frame %.3fs: %w", ts, err)
			}
			vecs, err := embedder.EmbedImages(ctx, []string{framePath})
			if err != nil {
				return nil, err
			}
			if len(vecs) == 0 || len(vecs[0]) == 0 {
				return nil, fmt.Errorf("empty frame embedding at %.3fs", ts)
			}
			embeddings = append(embeddings, vecs[0])
		}
		return averageFloat32Vectors(embeddings)
	default:
		return nil, fmt.Errorf("unsupported media_type %q for visual backfill", mediaType)
	}
}

func averageFloat32Vectors(vectors [][]float32) ([]float32, error) {
	if len(vectors) == 0 {
		return nil, fmt.Errorf("no vectors to average")
	}
	dim := len(vectors[0])
	if dim == 0 {
		return nil, fmt.Errorf("empty vector")
	}
	sums := make([]float64, dim)
	for _, vec := range vectors {
		if len(vec) != dim {
			return nil, fmt.Errorf("vector dimension mismatch: got %d want %d", len(vec), dim)
		}
		for i, v := range vec {
			sums[i] += float64(v)
		}
	}
	out := make([]float32, dim)
	for i := range sums {
		out[i] = float32(sums[i] / float64(len(vectors)))
	}
	return out, nil
}

func hasLegacyLocatorKey(metaJSON string) bool {
	metaJSON = strings.TrimSpace(metaJSON)
	if metaJSON == "" || metaJSON == "{}" {
		return false
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		return false
	}
	for _, key := range []string{"drive_link", "download_link", "drive_file_id", "local_path"} {
		if v, ok := meta[key]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return true
			}
		}
	}
	return false
}

func tableExists(ctx context.Context, db *sql.DB, name string) bool {
	var count int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count)
	return count > 0
}
