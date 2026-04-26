package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/service/indexing"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/storage"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
)

// CoreDeps holds the minimal runtime dependencies needed by the stripped-down server.
type CoreDeps struct {
	ScriptGen        *ollama.Generator
	DocClient        *drive.DocClient
	Utility          *common.UtilityHandler
	DB               *storage.SQLiteDB // Unified database
	ScriptsRepo      *scripts.ScriptRepository
	StockDriveRepo   *clips.Repository
	ArtlistRepo      *clips.Repository
	ClipsOnlyRepo    *clips.Repository
	VoiceoverService *voiceover.Service
	IndexingService  *indexing.Service
}

// initCoreMinimal creates only the services needed by the text/doc server.
func initCoreMinimal(cfg *config.Config, log *zap.Logger) (*CoreDeps, CleanupFunc, error) {
	ollamaClient := client.NewClient(cfg.External.OllamaURL, "")
	scriptGen := ollama.NewGenerator(ollamaClient)

	docClient, err := drive.NewDocClient(context.Background(), cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err != nil {
		log.Warn("Docs client not initialized", zap.Error(err))
	}

	// Initialize voiceover service
	voDir := filepath.Join(cfg.Storage.DataDir, "voiceovers")
	if err := os.MkdirAll(voDir, 0755); err != nil {
		log.Warn("Failed to create voiceovers directory", zap.Error(err))
	}
	voService := voiceover.NewService(cfg.Paths.PythonScriptsDir, voDir, log)

	// Initialize unified database with WAL mode
	mainDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "velox.db.sqlite", log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize main database: %w", err)
	}

	// Run all migrations on the unified database
	// 1. Orchestration tables (jobs, workers, etc.)
	orchestrationMigrationsDir := filepath.Join("migrations", "sqlite")
	if err := mainDB.RunMigrations(log, orchestrationMigrationsDir); err != nil {
		return nil, nil, fmt.Errorf("failed to run orchestration migrations: %w", err)
	}

	// 2. Scripts tables
	scriptsMigrationsDir := filepath.Join("internal", "repository", "scripts", "migrations")
	if err := mainDB.RunMigrations(log, scriptsMigrationsDir); err != nil {
		return nil, nil, fmt.Errorf("failed to run scripts migrations: %w", err)
	}

	// Create repositories sharing the same database
	clipsRepo := clips.NewRepository(mainDB.DB)
	artlistRepo := clips.NewRepository(mainDB.DB)
	clipsOnlyRepo := clips.NewRepository(mainDB.DB)

	scriptsRepo := scripts.NewScriptRepository(mainDB.DB)

	indexingService := indexing.NewService(clipsRepo, log)

	// Start indexing cron (e.g., every 15 minutes)
	downloadDir := filepath.Join(cfg.Storage.DataDir, "downloads")
	indexingService.StartCron(context.Background(), downloadDir, 15*time.Minute)

	cleanup := func() {
		if mainDB != nil {
			// Create a backup before closing
			if err := mainDB.Backup(); err != nil {
				log.Warn("Failed to create backup on shutdown", zap.Error(err))
			}
			if err := mainDB.Close(); err != nil {
				log.Error("Failed to close main database", zap.Error(err))
			}
		}
	}

	return &CoreDeps{
		ScriptGen:        scriptGen,
		DocClient:        docClient,
		Utility:          common.NewUtilityHandler(),
		DB:               mainDB,
		ScriptsRepo:      scriptsRepo,
		StockDriveRepo:   clipsRepo,
		ArtlistRepo:      artlistRepo,
		ClipsOnlyRepo:    clipsOnlyRepo,
		VoiceoverService: voService,
		IndexingService:  indexingService,
	}, cleanup, nil
}

