package main

import (
	"context"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/audio/tts"
	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/core/job"
	"velox/go-master/internal/core/worker"
	"velox/go-master/internal/download"
	"velox/go-master/internal/downloader"
	"velox/go-master/internal/gpu"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/nvidia"
	"velox/go-master/internal/queue"
	"velox/go-master/internal/runtime"
	"velox/go-master/internal/service/maintenance"
	"velox/go-master/internal/service/pipeline"
	"velox/go-master/internal/stock"
	"velox/go-master/internal/textgen"
	"velox/go-master/internal/video"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/config"
)

// CoreDeps holds the core infrastructure services that most other modules depend on.
type CoreDeps struct {
	Storage         runtimeStorage
	Queue           queue.Queue
	JobService      *job.Service
	WorkerService   *worker.Service
	OllamaClient    *ollama.Client
	ScriptGen       *ollama.Generator
	EdgeTTS         *tts.EdgeTTS
	VideoProc       *video.Processor
	YouTubeClientV2 youtube.Client
	StockMgr        *stock.StockManager
	EntityService   *entities.EntityService
	PipelineService *pipeline.VideoCreationService
	Downloader      *download.Downloader
	NvidiaClient    *nvidia.Client
	GpuMgr          *gpu.Manager
	TextGen         *textgen.Generator
	TikTokClient    downloader.Downloader
}

// initCore initializes the foundational services: storage, job/worker management,
// AI clients (Ollama, NVIDIA, GPU), video pipeline, YouTube, stock, and downloaders.
//
// The Maintenance service is returned as a BackgroundService for registration
// with the ServiceGroup — it is NOT started here.
func initCore(cfg *config.Config, log *zap.Logger) (*CoreDeps, []runtime.BackgroundService, CleanupFunc, error) {
	var services []runtime.BackgroundService

	// === Storage / Queue ===
	storage, err := buildRuntimeStorage(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	q := buildQueueBackend(storage)
	log.Info("Runtime backend selected",
		zap.String("storage_backend", selectStorageBackend(cfg)),
		zap.String("queue_backend", string(selectQueueBackend())),
	)

	jobService := job.NewService(storage, cfg)
	workerService := worker.NewService(storage, cfg)

	ctx := context.Background()
	if err := jobService.LoadQueue(ctx); err != nil {
		log.Warn("Failed to load job queue", zap.Error(err))
	}
	if err := workerService.LoadWorkers(ctx); err != nil {
		log.Warn("Failed to load workers", zap.Error(err))
	}

	// === Maintenance (returned as BackgroundService, NOT started) ===
	maintSvc := maintenance.New(cfg, jobService, workerService)
	services = append(services, maintSvc)

	// === AI: Ollama ===
	ollamaClient := ollama.NewClient(cfg.External.OllamaURL, "")
	scriptGen := ollama.NewGenerator(ollamaClient)

	// === TTS ===
	edgeTTS := tts.NewEdgeTTS(cfg.GetVoiceoverDir())

	// === Video Processor ===
	videoProc, err := video.NewProcessor("", cfg.GetVideoWorkDir())
	if err != nil {
		log.Warn("Failed to create video processor", zap.Error(err))
		videoProc = nil
	}

	// === YouTube Client ===
	var youtubeClientV2 youtube.Client
	ytCfg := &youtube.Config{Backend: "ytdlp", YtDlpPath: cfg.Paths.YtDlpPath}
	youtubeClientV2, err = youtube.NewClient("ytdlp", ytCfg)
	if err != nil {
		log.Warn("Failed to create YouTube client v2", zap.Error(err))
	} else {
		log.Info("YouTube client v2 initialized")
		// Inject YouTube client into script generator for transcript-based generation
		scriptGen.SetYouTubeClient(youtubeClientV2)
	}

	// === Stock Manager ===
	stockMgr, err := stock.NewManager(cfg.GetStockDir(), youtubeClientV2)
	if err != nil {
		log.Warn("Failed to create stock manager", zap.Error(err))
		stockMgr = nil
	}

	// === Entity Service ===
	extractor := entities.NewOllamaExtractor(ollamaClient)
	segmenter := entities.NewNLPSegmenter()
	entityService := entities.NewEntityService(extractor, segmenter)

	// === Pipeline Service ===
	ttsAdapter := tts.NewTTSAdapter(edgeTTS)
	videoAdapter := video.NewVideoProcessorAdapter(videoProc)
	pipelineService := pipeline.NewVideoCreationServiceWithOutputDir(
		scriptGen, entityService, ttsAdapter, videoAdapter, cfg.GetOutputDir(),
	)

	// === Downloader ===
	videoDownloader := download.NewDownloader(cfg.GetDownloadDir())

	// === TikTok ===
	var tiktokClient downloader.Downloader
	tiktokBackend := downloader.NewTikTokBackend(cfg.Paths.YtDlpPath, "", "")
	if err := tiktokBackend.IsAvailable(context.Background()); err == nil {
		tiktokClient = tiktokBackend
		log.Info("TikTok client initialized")
	} else {
		log.Warn("TikTok client not available", zap.Error(err))
	}

	// === GPU & NVIDIA ===
	gpuCfg := &gpu.GPUConfig{Enabled: true}
	gpuMgr := gpu.NewManager(gpuCfg)
	if err := gpuMgr.Initialize(ctx); err != nil {
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
		Timeout:      time.Duration(cfg.TextGen.Timeout) * time.Second,
		GPUSupported: true,
	}
	textGen := textgen.NewGenerator(gpuMgr, textGenCfg)
	if textGen != nil {
		log.Info("Text generator initialized")
	}

	var nvidiaClient *nvidia.Client
	nvCfg := nvidia.DefaultConfig()
	if nvCfg.APIKey != "" {
		nvidiaClient, err = nvidia.NewClient(nvCfg)
		if err != nil {
			log.Warn("Failed to initialize NVIDIA AI client", zap.Error(err))
		} else {
			log.Info("NVIDIA AI client initialized",
				zap.String("model", nvCfg.Model),
				zap.String("base_url", nvCfg.BaseURL),
			)
		}
	}

	cleanup := func() {
		_ = q.Close()
		_ = storage.Close()
	}

	return &CoreDeps{
		Storage:         storage,
		Queue:           q,
		JobService:      jobService,
		WorkerService:   workerService,
		OllamaClient:    ollamaClient,
		ScriptGen:       scriptGen,
		EdgeTTS:         edgeTTS,
		VideoProc:       videoProc,
		YouTubeClientV2: youtubeClientV2,
		StockMgr:        stockMgr,
		EntityService:   entityService,
		PipelineService: pipelineService,
		Downloader:      videoDownloader,
		NvidiaClient:    nvidiaClient,
		GpuMgr:          gpuMgr,
		TextGen:         textGen,
		TikTokClient:    tiktokClient,
	}, services, cleanup, nil
}
