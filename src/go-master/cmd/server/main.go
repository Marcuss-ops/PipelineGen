package main

import (
	"context"
	"os"

	"go.uber.org/zap"
	"velox/go-master/internal/api"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

func main() {
	cfg := config.Get()
	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	defer logger.Sync()

	log.Info("Starting VeloxEditing Go Master",
		zap.String("version", "1.0.0"),
		zap.Int("port", cfg.Server.Port),
	)

	deps, err := wireServices(cfg, log)
	if err != nil {
		log.Error("Failed to wire services", zap.Error(err))
		os.Exit(1)
	}

	// Start all background services (maintenance, scanners, watchers, monitors,
	// harvesters, schedulers, async cleanup) via the unified ServiceGroup.
	// They receive a shared context that is cancelled on shutdown.
	if err := deps.ServiceGroup.Start(context.Background()); err != nil {
		log.Error("Failed to start background services", zap.Error(err))
		deps.Cleanup()
		os.Exit(1)
	}

	server := api.NewServerWithHandlers(cfg, deps.JobService, deps.WorkerService, deps.RouterDeps)

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
