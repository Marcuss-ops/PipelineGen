package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"go.uber.org/zap"
	"velox/go-master/internal/api"
	"velox/go-master/internal/bootstrap"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

// Variables set via ldflags during build
var (
	buildVersion = "dev"
	commitHash   = "unknown"
)

// @title PipelineGen API
// @version 1.0
// @description The central API for video content generation and management.
// @BasePath /api
func main() {
	// Parse command-line flags
	mode := flag.String("mode", "", "Server mode: api, api-cron, worker, scheduler, maintenance (default: all)")
	flag.Parse()

	if *mode == "" {
		*mode = os.Getenv("VELOX_MODE")
	}
	if *mode == "" {
		*mode = "all"
	}

	// Ignore SIGHUP to prevent shutdown when parent shell exits
	signal.Ignore(syscall.SIGHUP)

	cfg := config.Get()
	if err := cfg.Validate(); err != nil {
		fmt.Printf("Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	defer logger.Sync()

	// Override commitHash from VERSION.txt if available
	if data, err := os.ReadFile("VERSION.txt"); err == nil {
		commitHash = strings.TrimSpace(string(data))
	}

	log.Info("Starting PipelineGen",
		zap.String("version", buildVersion),
		zap.String("commit", commitHash),
		zap.String("mode", *mode),
		zap.Int("port", cfg.Server.Port),
		zap.String("data_dir", cfg.Storage.DataDir),
	)

	deps, err := bootstrap.WireServices(cfg, log, *mode)
	if err != nil {
		log.Error("Failed to wire services", zap.Error(err))
		os.Exit(1)
	}

	server := api.NewServerWithHandlers(cfg, deps.Handlers)

	// Run server (blocks until signal or error)
	if err := server.Start(); err != nil {
		log.Error("Server error", zap.Error(err))
		// Still run cleanup even on error
		deps.Cleanup()
		os.Exit(1)
	}

	// Graceful cleanup: ServiceGroup.Stop() cancels shared context + calls
	// Stop() on every background service in reverse order, then we flush
	// caches and close storage.
	log.Info("Running cleanup...")

	deps.Cleanup()
	log.Info("Shutdown complete")
}
