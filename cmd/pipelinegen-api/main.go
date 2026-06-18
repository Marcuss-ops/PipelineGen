package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	internalworker "github.com/Marcuss-ops/PipelineGen/internal/api/handlers/internalworker"
	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/internal/logger"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/domain"
	jobrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/workernodes"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobbroker/local"
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

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	coreDeps, cleanup, err := app.ExportInitCoreWithModeAndContext(cfg, log, "api", rootCtx)
	if err != nil {
		log.Fatal("bootstrap failed", zap.Error(err))
	}
	defer cleanup()

	registry, err := app.WireRegistry(rootCtx, cfg, log, coreDeps)
	if err != nil {
		log.Fatal("wire registry failed", zap.Error(err))
	}

	router := api.NewRouter(cfg)
	router.SetRegistry(registry.Registry)
	if coreDeps.DB != nil && coreDeps.DB.DB != nil {
		jobRepo := jobrepo.NewRepository(coreDeps.DB.DB, log)
		workerRepo := workernodes.NewRepository(coreDeps.DB.DB)
		broker := local.New(domain.NewSQLiteJobRepository(jobRepo), workerRepo)
		router.SetWorkerHandler(internalworker.NewHandler(broker, log))
	}
	engine := router.Setup()

	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           engine,
		ReadTimeout:       time.Duration(cfg.Server.ReadTimeout) * time.Second,
		ReadHeaderTimeout: 15 * time.Second,
		WriteTimeout:      time.Duration(cfg.Server.WriteTimeout) * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatal("server failed", zap.Error(err))
		}
	case <-rootCtx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}
