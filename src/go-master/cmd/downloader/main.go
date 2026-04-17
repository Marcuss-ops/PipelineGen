package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	"velox/go-master/internal/download"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

func main() {
	cfg := config.Get()
	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	defer logger.Sync()

	log.Info("Starting Dedicated Downloader Service", zap.String("version", "1.0.0"))

	// Initialize Downloader
	downloadDir := cfg.GetDownloadDir()
	log.Info("Using download directory", zap.String("dir", downloadDir))
	
	downloaderSvc := download.NewDownloader(downloadDir)

	// In a real microservices scenario, this would listen to a Queue (Redis/Postgres)
	// For now, we prepare the entry point as a standalone worker.
	log.Info("Downloader service ready.")

	// Wait for signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down Downloader...")
}
