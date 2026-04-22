package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"go.uber.org/zap"
	"velox/go-master/internal/download"
	"velox/go-master/internal/queue"
	pgstorage "velox/go-master/internal/storage/postgres"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
)

func main() {
	cfg := config.Get()
	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	defer logger.Sync()

	workerID := os.Getenv("WORKER_ID")
	if workerID == "" {
		hostname, _ := os.Hostname()
		workerID = "downloader-" + hostname
	}

	log.Info("Starting Dedicated Downloader Service",
		zap.String("version", "1.0.1"),
		zap.String("worker_id", workerID),
	)

	// Initialize Downloader
	downloadDir := cfg.GetDownloadDir()
	log.Info("Using download directory", zap.String("dir", downloadDir))
	downloaderSvc := download.NewDownloader(downloadDir)

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
	} else {
		log.Warn("No VELOX_DB_DSN provided, running in standalone mode (no queue)")
	}

	// Setup Consumer
	// We use 3 workers by default for parallel downloads
	consumer := queue.NewConsumer(q, workerID, 3)
	
	// Register handlers for download-related jobs
	consumer.Register(string(models.TypeStockDownload), downloaderSvc.HandleDownloadJob)
	consumer.Register("video_download", downloaderSvc.HandleDownloadJob) // Alias

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start Consumer
	if err := consumer.Start(ctx); err != nil {
		log.Fatal("Failed to start job consumer", zap.Error(err))
	}

	log.Info("Downloader worker is now active and listening for jobs")

	// Wait for signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down Downloader...")
	_ = consumer.Stop()
}
