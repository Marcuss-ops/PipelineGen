// cmd/admin/qdrant_visual_backfill.go — visual embedding backfill command
// (extracted from qdrant_readiness.go, June 2026 Fase 4).
//
// The visual backfill regenerates CLIP ViT-B-32 embeddings from 512→768
// dimensions for assets whose visual_embedding was produced by the legacy
// 512-dim model. Runs as a dry-run by default; --apply writes the new
// embeddings back to SQLite.
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
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"

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
			n, err := parseStrictPositiveIntFlag(a, "--limit")
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

	ffmpegProc := rustexec.NewVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
	schema := qdrantschema.DefaultV3Schema()
	imageEmbedder := search.NewImageEmbedderAdapter(search.ImageEmbedderConfig{
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
			SET visual_embedding = ?, metadata_json = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
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

func regenerateVisualEmbedding(ctx context.Context, ffmpegProc *rustexec.VideoProcessor, embedder search.ImageEmbedder, assetID, mediaType, localPath string) ([]float32, error) {
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
