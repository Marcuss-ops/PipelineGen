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
	ScriptsRepo      *scripts.ScriptRepository
	ClipsRepo        *clips.Repository
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

	// Initialize stock database with migrations
	stockDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "stock.db.sqlite", log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize stock database: %w", err)
	}
	
	migrationsDir := filepath.Join("migrations", "sqlite")
	if err := stockDB.RunMigrations(log, migrationsDir); err != nil {
		return nil, nil, fmt.Errorf("failed to run stock database migrations: %w", err)
	}

	clipsRepo := clips.NewRepository(stockDB.DB)
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
		ScriptsRepo:      scriptsRepo,
		ClipsRepo:        clipsRepo,
		VoiceoverService: voService,
		IndexingService:  indexingService,
	}, cleanup, nil
}

