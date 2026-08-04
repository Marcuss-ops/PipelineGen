package app

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/transcripts"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// buildDomainScriptServices constructs the artifact service, image
// search resolver, and extract-important-clips handler and populates
// the wiring.DomainBundle with them.
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
	clipWriter *assets.ClipAtomicWriterAdapter,
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

	// ExtractImportantClips adapters + handler.
	extractDl := downloader.NewYTDLP(cfg)
	extractAdapters := BuildExtractImportantClipsAdapters(ExtractImportantClipsAdapterDeps{
		Subtitles: transcripts.NewYTDLPSubtitleAdapter(transcripts.Deps{
			Ytdlp:      extractDl,
			CmdBuilder: ytdlp.NewCommandBuilder(cfg),
			UseCookies: cfg.External.ResolveYouTubeCookiesPath() != "",
			Log:        log,
		}),
		Downloader: extractDl,
		Folder:     &adminFolderManagerAdapter{admin: drive.Admin},
		Files: func(ctx context.Context, req drivePutFnRequest) (*drivePutFnResult, error) {
			if drive.Publisher == nil {
				return nil, fmt.Errorf("compose domains: extract-important upload: drive.Publisher unwired")
			}
			res, err := drive.Publisher.Publish(ctx, delivery.PublishRequest{
				Destination:         delivery.DestinationAdmin,
				LocalPath:           req.LocalPath,
				Filename:            req.Filename,
				DestinationFolderID: req.FolderID,
				ConflictPolicy:      delivery.ConflictOverwrite,
			})
			if err != nil {
				return nil, fmt.Errorf("compose domains: extract-important upload: %w", err)
			}
			return &drivePutFnResult{FileID: res.FileID, WebViewLink: res.WebViewLink}, nil
		},
	})
	extractUseCase := youtube.NewExtractImportantClipsUseCase(youtube.ExtractImportantClipsDeps{
		Log:        log,
		Subtitles:  extractAdapters.TranscriptFetcher,
		Analyzer:   nil, // analyzer=nil → failClosedAnalyzerAdapter returns ErrAnalyzerUnavailable at runtime
		Downloader: extractAdapters.SectionDownloader,
		Folder:     extractAdapters.DriveFolder,
		Uploader:   extractAdapters.DriveUploader,
		Writer:     clipWriter,
		Hasher:     extractAdapters.Hasher,
	})
	bundle.ExtractImportantClipsJobHandler = youtube.NewExtractImportantClipsJobHandler(extractUseCase, log)

	return nil
}
