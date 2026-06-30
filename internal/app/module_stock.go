package app

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/stock"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/render"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)
func WireStockPipeline(cfg *config.Config, log *zap.Logger, bundle *StockBundle) (*StockPipelineWiring, error) {
	if bundle.DriveUploader == nil {
		log.Warn("stock pipeline not wired: missing drive client")
		return nil, nil
	}

	// PR6 port wiring: render adapter + cutter adapter. The application
	// layer talks to the canonical stock ports; this composition root is
	// the only place that knows the concrete adapters exist.
	ffmpegPath := cfg.External.FfmpegPath
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	transitionRegistry := render.DefaultTransitionRegistry()
	renderer := render.NewFFmpegRenderer(ffmpegPath, transitionRegistry, log)
	cutter := render.NewFFmpegCutter(ffmpegPath, log)
	log.Info("stock pipeline ports wired",
		zap.Int("transition_catalog_size", transitionRegistry.Len()))

	metaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)
	log.Info("metadata writer wired into stock pipeline")

	// PR-D: ctor injection via Deps{} literal. Composition-root
	// pre-rejection: every required dep MUST be non-nil by the time we
	// reach this call; a nil surfaces here as a fail-fast error so the
	// operator sees the missing dep at startup rather than racing the
	// late-bind setter sequence.
	if bundle.ClipsRepo == nil {
		return nil, fmt.Errorf("WireStockPipeline: bundle.ClipsRepo is required for production stock pipeline")
	}
	if bundle.AssetIndexService == nil {
		return nil, fmt.Errorf("WireStockPipeline: bundle.AssetIndexService is required for production stock pipeline")
	}
	if bundle.Dispatcher == nil {
		return nil, fmt.Errorf("WireStockPipeline: bundle.Dispatcher is required — QDRANT-002 PR7 removed the legacy fallback")
	}
	if bundle.ClipIndexerService == nil {
		return nil, fmt.Errorf("WireStockPipeline: bundle.ClipIndexerService is required for production stock pipeline")
	}

	svc, err := stockpipeline.NewService(stockpipeline.Deps{
		Cfg:       cfg,
		Log:       log,
		Drive:     bundle.DriveUploader,
		Publisher: bundle.Publisher,
		Storage: stockpipeline.StorageDeps{
			ClipsRepo:  bundle.ClipsRepo,
			AssetIndex: bundle.AssetIndexService,
			Dispatcher: bundle.Dispatcher,
		},
		Media: stockpipeline.MediaDeps{
			Cutter:      cutter,
			Renderer:    renderer,
			ClipIndexer: bundle.ClipIndexerService,
			MetaWriter:  metaWriter,
		},
		YouTube: bundle.YoutubeClipService,
		Jobs:    bundle.Jobs,
	})
	if err != nil {
		return nil, fmt.Errorf("WireStockPipeline: stockpipeline.NewService: %w", err)
	}

	// S2b refactor (June 2026): construct the use case first so the API
	// handler holds only the use case + logger; the dispatch decision
	// (async-vs-sync, jobs-required 503) lives in stockpipeline.StockUseCase.
	useCase := stockpipeline.NewStockUseCase(svc, bundle.JobFacade, log)
	// Blocco C1-Step 6 (June 2026): Stock capability is now built via
	// the canonical stock.Build(deps) (api.Descriptor, error) contract,
	// matching the artlist / youtube / clips precedent. The HTTP Handler
	// is constructed inside Build and captured by the returned
	// StockDescriptor's Module closure. The composition site
	// type-asserts ONCE to *stock.StockDescriptor (fail-closed) and
	// reuses the concrete for the StockPipelineWiring.Module field
	// (which satisfies api.Module structurally). The canonical
	// late-bind svc.RegisterHandler(bundle.Jobs) step stays at the
	// end (matches the artlist + youtube pattern — service-side job
	// registration lives outside the Build contract because the Stock
	// Descriptor does not register its own job slot today; no
	// DescriptorJobs implementation, no Descriptor.Service field).
	descriptor, err := stock.Build(stock.Dependencies{
		UseCase:     useCase,
		EnabledFunc: func() bool { return cfg != nil && cfg.Features.StockPipelineEnabled },
		ModuleOpts:  nil, // no per-feature middleware for the stock capability (matches pre-Step-6 wiring)
		Logger:      log,
	})
	if err != nil {
		return nil, fmt.Errorf("WireStockPipeline: stock.Build: %w", err)
	}
	sd, ok := descriptor.(*stock.StockDescriptor)
	if !ok || sd == nil {
		return nil, fmt.Errorf("WireStockPipeline: stock.Build returned unexpected descriptor type %T (want *stock.StockDescriptor)", descriptor)
	}
	svc.RegisterHandler(bundle.Jobs)
	return &StockPipelineWiring{Module: sd.Module, Service: svc}, nil
}

// YouTubeClipWiring holds the YouTube Clip module wiring.
//
// Blocco C1-Step 4 (June 2026): Handler field removed. The HTTP Handler
// is constructed inside `ytsources.Build(deps)` and captured by the
// returned YouTubeDescriptor's Module closure. No caller (composition
// root, tests, internal services) needs to read the raw
// `*YouTubeClipHandler` outside the package — matches the artlist /
// channels precedent of dropping the explicit Handler field in favor of
