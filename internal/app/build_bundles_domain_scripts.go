package app

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/transcripts"
	capyoutubeusecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	youtubeinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/youtube"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// buildDomainScriptServices constructs the artifact service, image
// search resolver, and the canonical segment-selection resolver and
// populates the wiring.DomainBundle with them.
//
// godlike/06 SSOT: each service constructor is the canonical SOLE
// owner of its composition.
func buildDomainScriptServices(
	ctx context.Context,
	cfg *config.Config,
	dbs *wiring.Databases,
	log *zap.Logger,
	drive *wiring.DriveBundle,
	repos *wiring.RepoBundle,
	search *wiring.SearchBundle,
	process *wiring.ProcessBundle,
	ai *wiring.AIBundle,
	bundle *wiring.DomainBundle,
	imageSvc *imgservice.Service,
) error {
	artifactBlobStore, err := artifacts.NewLocalBlobStore(cfg.Storage.DataDir)
	if err != nil {
		return fmt.Errorf("compose domains: artifact blob store: %w", err)
	}
	artifactRepo := artifacts.NewSQLiteRepository(dbs.DualPool.Writer)
	bundle.ArtifactService = artifacts.NewService(artifactBlobStore, artifactRepo, log)
	log.Info("P0.1: artifact blob service wired (content-addressed staging + verify + promote)",
		zap.String("data_dir", cfg.Storage.DataDir))

	// ImageSearchResolver: the orchestrator provides the concrete image
	// service so this helper stays free of the images import.
	imageSearchResolver, err := buildImageSearchResolver(imageSvc, repos.ImageRepo, log)
	if err != nil {
		return fmt.Errorf("compose images: %w", err)
	}
	bundle.ImageSearchResolver = imageSearchResolver

	// SegmentSelectionResolver: the canonical explicit|important strategy
	// owner behind POST /api/clips/process selection.mode. The resolver
	// owns NO publishing behaviour — it only maps the selection mode onto
	// []dto.Segment, which then flows through the SAME canonical
	// extraction pipeline (ExtractionService → extractFanOut →
	// ProcessYouTubeSegmentUseCase). This retires the former duplicate
	// extract-important ingest system (download/upload/hash/commit loop).
	extractDl := downloader.NewYTDLP(cfg)
	extractSubtitleSource := transcripts.NewCachingTranscriptProvider(
		youtubeinfra.NewYTDLPSubtitleAdapter(youtubeinfra.Deps{
			Ytdlp:      extractDl,
			CmdBuilder: ytdlp.NewCommandBuilder(cfg),
			UseCookies: cfg.External.ResolveYouTubeCookiesPath() != "",
			Log:        log,
		}),
	)
	transcriptFetcher := &transcriptFetcherAdapter{sub: extractSubtitleSource}
	// Analyzer is the nil-tolerant forward-pointer: failClosedAnalyzerAdapter
	// surfaces ErrAnalyzerUnavailable until the real LLM analyzer lands.
	analyzer := &failClosedAnalyzerAdapter{}
	segmentSelectionResolver := capyoutubeusecase.NewSegmentSelectionResolver(log, transcriptFetcher, analyzer)
	bundle.YoutubeClipService.SetSegmentSelectionResolver(segmentSelectionResolver)

	return nil
}
