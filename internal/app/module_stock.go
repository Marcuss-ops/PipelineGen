package app

import (
	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/stock"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	ytService "github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/render"
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
)

// StockBundle is the capability bundle for the stock-pipeline module.
//
// PR4d-chunk2 (June 2026): wraps the 7 cross-bundle reads of WireStockPipeline.
type StockBundle struct {
	DriveClient        *gdrive.Service
	Jobs               *appjobs.Service
	JobFacade          *jobdomain.Service
	AssetIndexService  *assetindex.Service
	ClipsRepo          *assets.ClipsRepository
	YoutubeClipService *ytService.Service
	ClipIndexerService *clipindexer.Service
	Dispatcher         *outbox.Dispatcher
}

// StockPipelineWiring holds the StockPipeline module wiring.
type StockPipelineWiring struct {
	Handler *stock.Handler
	Module  api.Module
	Service *stockpipeline.Service
}

// WireStockPipeline creates the StockPipeline service, handler, and module.
//
// PR4d-chunk2 (June 2026): takes *StockBundle.
// PR6 (June 2026): also constructs the canonical StockRenderer +
// VideoCutter infra adapters and injects them via SetRenderer + SetCutter
// so the application layer never reaches into ffmpeg/process directly.
func WireStockPipeline(cfg *config.Config, log *zap.Logger, bundle *StockBundle) (*StockPipelineWiring, error) {
	if bundle.DriveClient == nil {
		log.Warn("stock pipeline not wired: missing drive client")
		return nil, nil
	}
	svc := stockpipeline.NewService(cfg, log, bundle.DriveClient)
	svc.SetJobsSvc(bundle.Jobs)
	svc.SetAssetIndex(bundle.AssetIndexService)
	if bundle.ClipsRepo != nil {
		svc.SetClipsRepo(bundle.ClipsRepo)
	}
	if bundle.YoutubeClipService != nil {
		svc.SetYoutubeService(bundle.YoutubeClipService)
	}
	if bundle.ClipIndexerService != nil {
		svc.SetClipIndexer(bundle.ClipIndexerService)
	}
	if bundle.Dispatcher != nil {
		svc.SetDispatcher(bundle.Dispatcher)
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
	svc.SetRenderer(renderer)
	svc.SetCutter(cutter)
	log.Info("stock pipeline ports wired",
		zap.Int("transition_catalog_size", transitionRegistry.Len()))

	metaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)
	svc.SetMetadataWriter(metaWriter)
	log.Info("metadata writer wired into stock pipeline")
	handler := stock.NewHandler(svc, bundle.JobFacade, log)
	stockEnabled := cfg != nil && cfg.Features.StockPipelineEnabled
	mod := api.NewRouteModule(
		"stock-pipeline",
		func() bool { return stockEnabled },
		"/stock-pipeline",
		handler,
		log,
	)
	svc.RegisterHandler(bundle.Jobs)
	return &StockPipelineWiring{Handler: handler, Module: mod, Service: svc}, nil
}
