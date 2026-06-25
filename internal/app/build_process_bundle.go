package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// BuildProcessBundle builds media-processing adapters. driveUploader
// passed in directly.
//
// PG-034 (June 2026): Qdrant removed entirely. VectorSvc field,
// startQdrantCollection closure, and the IOpaqueStartFunc return are
// gone — the process bundle is now a pure struct with no deferred
// startup closure. The clip indexer is the canonical semantic-search
// backend.
func BuildProcessBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, driveUploader *drive.Uploader) (*ProcessBundle, error) {
	_ = ctx
	mediaProcessor := initMediaProcessor(cfg, dbs.main, repos.Assets.Repository(), repos.Assets,
		repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), log, driveUploader)

	vlmClient := vlm.NewClient(vlm.Config{
		Enabled:   cfg.VLM.Enabled,
		Endpoint:  cfg.VLM.URL,
		Model:     cfg.VLM.Model,
		TimeoutMs: cfg.VLM.TimeoutMs,
		Weight:    cfg.VLM.Weight,
	})

	clipIndexerService := clipindexer.NewService(&clipindexer.Config{
		Enabled:               cfg.ClipIndexer.Enabled,
		ServerURL:             cfg.ClipIndexer.ServerURL,
		ScriptPath:            cfg.ClipIndexer.ScriptPath,
		PythonBin:             cfg.ClipIndexer.PythonBin,
		AutoIndexAfterArtlist: cfg.ClipIndexer.AutoIndexAfterArtlist,
		MaxConcurrentIndexing: cfg.ClipIndexer.MaxConcurrentIndexing,
		DBPath:                dbs.main.Path(),
	}, dbs.main, dbs.main.Path(), log)

	return &ProcessBundle{
		MediaProcessor:     mediaProcessor,
		ClipIndexerService: clipIndexerService,
		VLMClient:          vlmClient,
	}, nil
}
