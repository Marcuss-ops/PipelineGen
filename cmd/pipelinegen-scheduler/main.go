package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/internal/logger"
	"go.uber.org/zap"
)

func main() {
	cfg := config.Get()
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logger.Init(cfg.Logging.Level, cfg.Logging.Format)
	log := logger.Get()
	defer logger.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	_, cleanup, err := app.ExportInitCoreWithModeAndContext(cfg, log, "scheduler", ctx)
	if err != nil {
		log.Fatal("bootstrap failed", zap.Error(err))
	}
	defer cleanup()

	<-ctx.Done()
}
