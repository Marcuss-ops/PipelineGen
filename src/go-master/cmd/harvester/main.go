package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/adapters"
	"velox/go-master/internal/api/handlers"
	"velox/go-master/internal/harvester"
	"velox/go-master/internal/queue"
	pgstorage "velox/go-master/internal/storage/postgres"
	"velox/go-master/internal/storage/jsondb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/internal/youtube"
	"velox/go-master/internal/downloader"
	"velox/go-master/internal/clip"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

func main() {
	cfg := config.Get()
	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	defer logger.Sync()

	log.Info("Starting Dedicated Harvester Service", zap.String("version", "1.1.0"))

	// Initialize Queue
	var q queue.Queue = queue.NewNoopQueue()
	dsn := strings.TrimSpace(os.Getenv("VELOX_DB_DSN"))
	if dsn != "" {
		storage, err := pgstorage.NewStorage(dsn)
		if err != nil {
			log.Fatal("Failed to connect to Postgres", zap.Error(err))
		}
		defer storage.Close()
		q = queue.NewPostgresQueue(storage.GetDB())
		log.Info("Connected to Postgres Queue")
	}

	// Initialize Storage (Shared boundary)
	storage, err := jsondb.NewStorage(cfg.Storage.DataDir)
	if err != nil {
		log.Fatal("Failed to initialize storage", zap.Error(err))
	}
	defer storage.Close()

	// Initialize YouTube V2
	ytCfg := &youtube.Config{Backend: "ytdlp", YtDlpPath: cfg.Paths.YtDlpPath}
	ytClient, err := youtube.NewClient("ytdlp", ytCfg)
	if err != nil {
		log.Warn("Failed to create YouTube client", zap.Error(err))
	}

	// Initialize TikTok
	tiktokBackend := downloader.NewTikTokBackend(cfg.Paths.YtDlpPath, "", "")

	// Initialize Drive
	driveHandler := handlers.NewDriveHandler(cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err := driveHandler.InitClient(context.Background()); err != nil {
		log.Warn("Drive client failed", zap.Error(err))
	}
	driveClient := driveHandler.GetDriveClient()

	// Initialize ClipDB (Required for deduplication)
	clipDBPath := cfg.Storage.DataDir + "/clips_index.db"
	clipDB, err := clip.OpenClipDB(clipDBPath)
	if err != nil {
		log.Warn("Failed to open ClipDB", zap.Error(err))
	} else {
		defer clipDB.Close()
	}

	// Setup Harvester
	if ytClient != nil && driveClient != nil && clipDB != nil {
		ytAdapter := adapters.NewYouTubeSearcherAdapter(ytClient)
		harvesterConfig := &harvester.Config{
			Enabled:            true,
			CheckInterval:      1 * time.Hour,
			SearchQueries:      []string{"interview", "highlights", "documentary"},
			MaxResultsPerQuery: 20,
			MinViews:           10000,
			Timeframe:          "month",
			MaxConcurrentDls:   3,
			DownloadDir:        cfg.GetDownloadDir(),
			ProcessClips:       true,
			DriveFolderID:      cfg.Drive.StockRootFolderID,
		}
		clipAdapter := adapters.NewClipDBToHarvesterAdapter(clipDB)
		harvesterSvc := harvester.NewHarvester(harvesterConfig, ytAdapter, tiktokBackend, driveClient, clipAdapter, q)

		log.Info("Harvester service running...")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := harvesterSvc.Start(ctx); err != nil {
			log.Fatal("Failed to start harvester", zap.Error(err))
		}

		// Wait for signal
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit

		log.Info("Shutting down Harvester...")
		_ = harvesterSvc.Stop()
	} else {
		log.Fatal("Harvester dependencies not met (YouTube, Drive, or ClipDB missing)")
	}
}
