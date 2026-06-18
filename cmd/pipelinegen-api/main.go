package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	internalworker "github.com/Marcuss-ops/PipelineGen/internal/api/handlers/internalworker"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobbroker/local"
	workerassets "github.com/Marcuss-ops/PipelineGen/internal/application/workerassets"
	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/internal/logger"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	clipsrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/domain"
	imagerepo "github.com/Marcuss-ops/PipelineGen/internal/repository/images"
	jobrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/jobs"
	vorepo "github.com/Marcuss-ops/PipelineGen/internal/repository/voiceovers"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/workernodes"
	"github.com/Marcuss-ops/PipelineGen/internal/storage"
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

	db, err := storage.NewSQLiteDB(cfg.Storage.DataDir, storage.DBMedia, log)
	if err != nil {
		log.Fatal("database bootstrap failed", zap.Error(err))
	}
	defer db.Close()
	if err := db.RunMigrations(log, filepath.Join("migrations", "sqlite")); err != nil {
		log.Fatal("migration failed", zap.Error(err))
	}

	router := api.NewRouter(cfg)
	jobRepo := jobrepo.NewRepository(db.DB, log)
	workerRepo := workernodes.NewRepository(db.DB)
	assetIndexSvc := assetindex.NewService(assetindex.NewRepository(db.DB))
	clipRepo := clipsrepo.NewRepository(db.DB, log)
	imageRepo := imagerepo.NewRepository(db.DB)
	voiceoverRepo := vorepo.NewRepository(db.DB)
	broker := local.New(domain.NewSQLiteJobRepository(jobRepo), workerRepo)
	assetSvc := workerassets.NewServiceWithUploadRoot(assetIndexSvc, clipRepo, imageRepo, voiceoverRepo, filepath.Join(cfg.Storage.DataDir, "worker-uploads"), log)
	router.SetWorkerHandler(internalworker.NewHandler(broker, assetSvc, log))
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
