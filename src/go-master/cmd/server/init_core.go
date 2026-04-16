package main

import (
	"context"
	"os"

	"go.uber.org/zap"
	"velox/go-master/internal/audio/tts"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/clipdb"
	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/core/job"
	"velox/go-master/internal/core/worker"
	"velox/go-master/internal/download"
	"velox/go-master/internal/downloader"
	"velox/go-master/internal/gpu"
	"velox/go-master/internal/harvester"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/nvidia"
	"velox/go-master/internal/script"
	"velox/go-master/internal/service/channelmonitor"
	"velox/go-master/internal/service/pipeline"
	"velox/go-master/internal/service/scriptclips"
	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/internal/service/stockorchestrator"
	"velox/go-master/internal/stock"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/stockjob"
	"velox/go-master/internal/storage/jsondb"
	"velox/go-master/internal/textgen"
	"velox/go-master/internal/video"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

// initCoreServices initializes the core infrastructure: storage, job/worker services,
// Ollama, TTS, video processor, YouTube client, stock manager, entity service, pipeline.
func initCoreServices(cfg *config.Config, log *zap.Logger) (
	storage *jsondb.Storage,
	jobService *job.Service,
	workerService *worker.Service,
	ollamaClient *ollama.Client,
	scriptGen *ollama.Generator,
	edgeTTS *tts.EdgeTTS,
	videoProc *video.Processor,
	youtubeClientV2 youtube.Client,
	stockMgr *stock.Manager,
	entityService *entities.EntityService,
	pipelineService *pipeline.VideoCreationService,
	videoDownloader *download.Downloader,
	err error,
) {
	storage, err = jsondb.NewStorage(cfg.Storage.DataDir)
	if err != nil {
		log.Fatal("Failed to initialize storage", zap.Error(err))
		return
	}

	jobService = job.NewService(storage, cfg)
	workerService = worker.NewService(storage, cfg)

	ctx := context.Background()
	if err := jobService.LoadQueue(ctx); err != nil {
		log.Warn("Failed to load job queue", zap.Error(err))
	}
	if err := workerService.LoadWorkers(ctx); err != nil {
		log.Warn("Failed to load workers", zap.Error(err))
	}

	ollamaClient = ollama.NewClient(cfg.External.OllamaURL, "")
	scriptGen = ollama.NewGenerator(ollamaClient)
	edgeTTS = tts.NewEdgeTTS(cfg.GetVoiceoverDir())

	videoProc, err = video.NewProcessor("", cfg.GetVideoWorkDir())
	if err != nil {
		log.Warn("Failed to create video processor", zap.Error(err))
		videoProc = nil
	}

	ytCfg := &youtube.Config{Backend: "ytdlp", YtDlpPath: cfg.Paths.YtDlpPath}
	youtubeClientV2, err = youtube.NewClient("ytdlp", ytCfg)
	if err != nil {
		log.Warn("Failed to create YouTube client v2", zap.Error(err))
	} else {
		log.Info("YouTube client v2 initialized")
	}

	stockMgr, err = stock.NewManager(cfg.GetStockDir(), youtubeClientV2)
	if err != nil {
		log.Warn("Failed to create stock manager", zap.Error(err))
		stockMgr = nil
	}

	extractor := entities.NewOllamaExtractor(ollamaClient)
	segmenter := entities.NewNLPSegmenter()
	entityService = entities.NewEntityService(extractor, segmenter)

	ttsAdapter := tts.NewTTSAdapter(edgeTTS)
	videoAdapter := video.NewVideoProcessorAdapter(videoProc)
	pipelineService = pipeline.NewVideoCreationServiceWithOutputDir(
		scriptGen, entityService, ttsAdapter, videoAdapter, cfg.GetOutputDir(),
	)
	videoDownloader = download.NewDownloader(cfg.GetDownloadDir())

	return
}

// initGPUAndTextGen initializes GPU manager, NVIDIA client, and text generator.
func initGPUAndTextGen(cfg *config.Config, log *zap.Logger) (
	gpuMgr *gpu.Manager,
	nvidiaClient *nvidia.Client,
	textGen *textgen.Generator,
	err error,
) {
	var nErr error
	nvCfg := nvidia.DefaultConfig()
	if nvCfg.APIKey != "" {
		nvidiaClient, nErr = nvidia.NewClient(nvCfg)
		if nErr != nil {
			log.Warn("Failed to initialize NVIDIA AI client", zap.Error(nErr))
		} else {
			log.Info("NVIDIA AI client initialized",
				zap.String("model", nvCfg.Model),
				zap.String("base_url", nvCfg.BaseURL),
			)
		}
	}

	gpuCfg := &gpu.GPUConfig{Enabled: true}
	gpuMgr = gpu.NewManager(gpuCfg)
	if err := gpuMgr.Initialize(context.Background()); err != nil {
		log.Warn("GPU manager initialization failed (continuing without GPU acceleration)", zap.Error(err))
	} else {
		selectedGPU, _ := gpuMgr.GetSelectedGPU()
		if selectedGPU != nil {
			log.Info("GPU manager initialized", zap.String("gpu_name", selectedGPU.Name))
		} else {
			log.Warn("GPU manager initialized but no GPU detected")
		}
	}

	textGenCfg := &textgen.GeneratorConfig{
		DefaultModel: cfg.TextGen.DefaultModel,
		Timeout:      timeDuration(cfg.TextGen.Timeout) * timeSecond,
		GPUSupported: true,
	}
	textGen = textgen.NewGenerator(gpuMgr, textGenCfg)
	if textGen != nil {
		log.Info("Text generator initialized")
	}

	return
}

// initDatabases initializes StockDB and ClipDB.
func initDatabases(cfg *config.Config, log *zap.Logger) (
	stockDB *stockdb.StockDB,
	clipDB *clipdb.ClipDB,
	err error,
) {
	// StockDB
	stockDBPaths := []string{
		cfg.Storage.DataDir + "/stock.db.json",
		"src/go-master/data/stock.db.json",
		"data/stock.db.json",
	}
	for _, stockDBPath := range stockDBPaths {
		if _, statErr := os.Stat(stockDBPath); statErr == nil {
			stockDB, err = stockdb.Open(stockDBPath)
			if err != nil {
				log.Warn("Failed to open StockDB", zap.String("path", stockDBPath), zap.Error(err))
			} else {
				log.Info("StockDB opened", zap.String("path", stockDBPath))
			}
			break
		}
	}

	// ClipDB
	clipDBPath := cfg.Storage.DataDir + "/clip_index.json"
	clipDB, err = clipdb.Open(clipDBPath)
	if err != nil {
		log.Warn("Failed to open ClipDB", zap.Error(err))
	} else {
		log.Info("ClipDB opened", zap.Int("clips", clipDB.GetClipCount()))
	}

	return
}

// initScriptDocsService initializes the ScriptDocs handler, Artlist index, DB, clip search.
func initScriptDocsService(
	cfg *config.Config, log *zap.Logger,
	scriptGen *ollama.Generator, ollamaClient *ollama.Client,
	stockDB *stockdb.StockDB, artlistSrc *clip.ArtlistSource,
	driveHandler *handlers,
) (
	scriptDocsHandler *handlers,
	artlistPipelineHandler *handlers,
	artlistIdx *scriptdocs.ArtlistIndex,
	artlistDB *artlistdb.ArtlistDB,
	clipSearch *clipsearch.Service,
) {
	artlistIndexPath := cfg.Storage.DataDir + "/artlist_stock_index.json"
	if idx, err := scriptdocs.LoadArtlistIndex(artlistIndexPath); err == nil {
		artlistIdx = idx
		log.Info("Artlist index loaded", zap.Int("clips", len(artlistIdx.Clips)))

		// Open local ArtlistDB
		artlistDB, err = artlistdb.Open(cfg.Storage.DataDir + "/artlist_local.db.json")
		if err != nil {
			log.Warn("Failed to open ArtlistDB", zap.Error(err))
		} else {
			log.Info("ArtlistDB opened", zap.String("path", cfg.Storage.DataDir+"/artlist_local.db.json"))
		}

		// Initialize dynamic clip search service
		if driveHandler.GetDriveClient() != nil && stockDB != nil {
			clipSearch = clipsearch.New(
				driveHandler.GetDriveClient(), stockDB, artlistDB,
				cfg.GetDownloadDir(), cfg.Paths.YtDlpPath,
			)
			log.Info("Dynamic clip search service initialized")
		}
	}
	return
}

// Placeholder type aliases for time imports used in init functions
type timeDuration = timeDuration
