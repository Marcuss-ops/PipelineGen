package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/diagnostics"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/fallback"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/scraper"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	mediaproc "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

type artlistRuntime struct {
	metadataWriter       artlistPkg.MetadataWriter
	stager               appassets.SourceStager
	isLiveProbe          artlistPkg.IsLiveProbe
	runRepository        artlistPkg.RunRepository
	systemProber         artlistPkg.SystemProber
	processingRepository asset.ProcessingRepository
	versionRepository    asset.VersionRepository
	finalizer            finalization.AssetFinalizerTx
	scraperSearcher      artlistPkg.Searcher
	pixabaySearcher      artlistPkg.Searcher
	pexelsSearcher       artlistPkg.Searcher
	licenseRepository    asset.LicenseRepository
	releaseRepository    asset.ReleaseRepository
	renditionRepository  asset.RenditionRepository
}

func buildArtlistRuntime(
	cfg *config.Config,
	bundle *ArtlistBundle,
	dispatcher *outbox.Dispatcher,
	reader drivepkg.Reader,
	lifecycle drivepkg.FileLifecycle,
	metaWriter semantic.MetadataWriterPort,
	finalizerTx finalization.AssetFinalizerTx,
	log *zap.Logger,
) (*artlistRuntime, error) {
	ffmpegProc := ffmpeg.NewProcessor("")

	probeBaseURL := cfg.External.VeloxBaseURL
	if probeBaseURL == "" {
		probeBaseURL = fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
	}
	isLiveProbe := artlistPkg.NewHTTPSelfLoopProbe(
		probeBaseURL,
		"/api/artlist/stats",
		artlistPkg.DefaultProbeTimeout,
		log,
	)

	systemProber := &diagnostics.AdminSystemProber{
		Log:               log,
		ScraperURL:        cfg.External.ArtlistScraperServerURL,
		BrowserURL:        "",
		SessionURL:        "",
		DownloaderURL:     "",
		FFmpegBinaryPath:  cfg.External.FfmpegPath,
		FFmpegRunner:      diagnostics.DefaultRunner{},
		ProbeFolderAccess: nil,
		ProbeFolderRootID: artlistPkg.ResolveRootFolderID(cfg),
	}

	semanticEnricher := artlistPkg.NewSemanticEnricher(
		bundle.ClipsRepo,
		bundle.ClipIndexerService,
		metaWriter,
		bundle.Publisher,
		reader,
		dispatcher,
		lifecycle,
		log,
	)

	assetSQLiteStore := assets.NewAssetStoreSQLite(bundle.DB.DB, log)
	assetProcRepo := assetSQLiteStore.ProcessingRepository()
	assetVerRepo := assetSQLiteStore.VersionRepository()

	artlistRunsRepo, err := assets.NewArtlistRunsRepository(bundle.DB.DB, log)
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: NewArtlistRunsRepository: %w", err)
	}
	artlistRunsAdapter := NewArtlistRunsRepoAdapter(artlistRunsRepo)
	_ = (artlistPkg.RunRepository)(artlistRunsAdapter)

	artlistDownloadAuditRepo, err := assets.NewArtlistDownloadAuditRepository(bundle.DB.DB, log)
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: NewArtlistDownloadAuditRepository: %w", err)
	}
	artlistDownloadAuditAdapter := NewArtlistDownloadAuditAdapter(artlistDownloadAuditRepo)
	_ = (artlistPkg.DownloadAuditRepository)(artlistDownloadAuditAdapter)

	licenseRepo, err := assets.NewAssetLicenseRepository(bundle.DB.DB, log)
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: NewAssetLicenseRepository: %w", err)
	}
	releaseRepo, err := assets.NewAssetReleaseRepository(bundle.DB.DB, log)
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: NewAssetReleaseRepository: %w", err)
	}
	renditionRepo, err := assets.NewAssetRenditionRepository(bundle.DB.DB, log)
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: NewAssetRenditionRepository: %w", err)
	}

	artlistDownloader := buildArtlistDownloader(cfg, artlistDownloadAuditAdapter, ffmpegProc, log)
	_ = (artlistPkg.Downloader)(artlistDownloader)
	artlistStager := artlistPkg.NewArtlistStager(artlistDownloader)
	if bundle.MediaProcessor != nil {
		if mp, ok := bundle.MediaProcessor.(*mediaproc.Processor); ok {
			mp.SetArtlistDownloader(&artlistProcessorDownloadAdapter{resolver: artlistDownloader})
			log.Info("WireArtlist: ArtlistDownloader wired into media processor (Resolver bridge)")
		}
	}

	pexelsSearcher := fallback.NewPexels(fallback.Config{
		APIKey:     cfg.External.PexelsAPIKey,
		BaseURL:    cfg.External.PexelsBaseURL,
		SourceName: "pexels",
	})
	pixabaySearcher := fallback.NewPixabay(fallback.Config{
		APIKey:     cfg.External.PixabayAPIKey,
		BaseURL:    cfg.External.PixabayBaseURL,
		SourceName: "pixabay",
	})
	scraperSearcher := scraper.New(scraper.Config{
		ServerURL:  cfg.External.ArtlistScraperServerURL,
		ScraperDir: cfg.External.NodeScraperDir,
		ScriptName: "artlist_search.js",
	}, log)

	return &artlistRuntime{
		metadataWriter:       semanticEnricher,
		stager:               artlistStager,
		isLiveProbe:          isLiveProbe,
		runRepository:        artlistRunsAdapter,
		systemProber:         systemProber,
		processingRepository: assetProcRepo,
		versionRepository:    assetVerRepo,
		finalizer:            finalizerTx,
		scraperSearcher:      scraperSearcher,
		pixabaySearcher:      pixabaySearcher,
		pexelsSearcher:       pexelsSearcher,
		licenseRepository:    licenseRepo,
		releaseRepository:    releaseRepo,
		renditionRepository:  renditionRepo,
	}, nil
}

func buildArtlistDownloader(
	cfg *config.Config,
	auditRepository artlistPkg.DownloadAuditRepository,
	ffmpegProc *ffmpeg.Processor,
	log *zap.Logger,
) *downloader.Resolver {
	return downloader.NewResolver(cfg, downloader.ResolverConfig{
		ScraperURL:         cfg.External.ArtlistScraperServerURL,
		AcquisitionMode:    artlistPkg.ArtlistAcquisitionMode(cfg.External.ArtlistAcquisitionMode),
		AccountID:          cfg.External.ArtlistAccountID,
		DailyDownloadLimit: cfg.External.ArtlistDailyDownloadLimit,
		AuditRepository:    auditRepository,
		PostValidator: func(ctx context.Context, path string) error {
			mediaInfo, err := ffmpegProc.Probe(ctx, path)
			if err != nil {
				return err
			}
			if !mediaInfo.HasVideo && !mediaInfo.HasAudio {
				return fmt.Errorf("ffprobe: no media stream detected in %q (corrupt container or missing AES-128 stage upstream)", path)
			}
			return nil
		},
	}, log, downloader.NewMetrics())
}

type artlistProcessorDownloadAdapter struct {
	resolver *downloader.Resolver
}

func (a *artlistProcessorDownloadAdapter) DownloadArtlistClip(
	ctx context.Context, sourceURL, clipPageURL, clipID, destDir, filename string,
) (string, error) {
	result, err := a.resolver.Download(ctx, artlistPkg.DownloadRequest{
		SourceRef:     sourceURL,
		ClipPageURL:   clipPageURL,
		ClipID:        clipID,
		DestinationID: destDir,
		Filename:      filename,
	})
	if err != nil {
		return "", err
	}
	return result.LocalPath, nil
}
