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
	StockDB          *storage.SQLiteDB
	ArtlistDB        *storage.SQLiteDB
	ClipsDB          *storage.SQLiteDB
	ScriptsRepo      *scripts.ScriptRepository
	StockDriveRepo   *clips.Repository
	ArtlistRepo      *clips.Repository
	ClipsOnlyRepo   *clips.Repository
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

	// Initialize stock_drive database (for Stock Drive clips)
	stockDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "stock_drive.db.sqlite", log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize stock_drive database: %w", err)
	}

	clipsRepo := clips.NewRepository(stockDB.DB)

	// Initialize artlist database (for Artlist clips)
	artlistDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "artlist.db.sqlite", log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize artlist database: %w", err)
	}

	artlistRepo := clips.NewRepository(artlistDB.DB)

	// Initialize clips database (for Clips)
	clipsOnlyDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "clips.db.sqlite", log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize clips database: %w", err)
	}

	clipsOnlyRepo := clips.NewRepository(clipsOnlyDB.DB)

	indexingService := indexing.NewService(clipsRepo, log)

	// Start indexing cron (e.g., every 15 minutes)
	downloadDir := filepath.Join(cfg.Storage.DataDir, "downloads")
	indexingService.StartCron(context.Background(), downloadDir, 15*time.Minute)

	// Initialize scripts database with migrations
	scriptsDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "scripts.db.sqlite", log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize scripts database: %w", err)
	}
	
	scriptsMigrationsDir := filepath.Join("internal", "repository", "scripts", "migrations")
	if err := scriptsDB.RunMigrations(log, scriptsMigrationsDir); err != nil {
		return nil, nil, fmt.Errorf("failed to run scripts database migrations: %w", err)
	}

	scriptsRepo := scripts.NewScriptRepository(scriptsDB.DB)

	cleanup := func() {
		if stockDB != nil {
			if err := stockDB.Close(); err != nil {
				log.Error("Failed to close stock database", zap.Error(err))
			}
		}
		if artlistDB != nil {
			if err := artlistDB.Close(); err != nil {
				log.Error("Failed to close artlist database", zap.Error(err))
			}
		}
		if clipsOnlyDB != nil {
			if err := clipsOnlyDB.Close(); err != nil {
				log.Error("Failed to close clips database", zap.Error(err))
			}
		}
		if scriptsDB != nil {
			if err := scriptsDB.Close(); err != nil {
				log.Error("Failed to close scripts database", zap.Error(err))
			}
		}
	}

	return &CoreDeps{
		ScriptGen:        scriptGen,
		DocClient:        docClient,
		Utility:          common.NewUtilityHandler(),
		StockDB:          stockDB,
		ArtlistDB:        artlistDB,
		ClipsDB:          clipsOnlyDB,
		ScriptsRepo:      scriptsRepo,
		StockDriveRepo:   clipsRepo,
		ArtlistRepo:      artlistRepo,
		ClipsOnlyRepo:    clipsOnlyRepo,
		VoiceoverService: voService,
		IndexingService:  indexingService,
	}, cleanup, nil
}

