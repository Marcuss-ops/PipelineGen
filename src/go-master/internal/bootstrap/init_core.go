package bootstrap

import (
	"context"
	"path/filepath"

	"go.uber.org/zap"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/storage"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
)

// CoreDeps holds the minimal runtime dependencies needed by the stripped-down server.
type CoreDeps struct {
	ScriptGen *ollama.Generator
	DocClient *drive.DocClient
	Utility   *common.UtilityHandler
	StockDB   *storage.SQLiteDB
}

// initCoreMinimal creates only the services needed by the text/doc server.
func initCoreMinimal(cfg *config.Config, log *zap.Logger) (*CoreDeps, CleanupFunc, error) {
	ollamaClient := ollama.NewClient(cfg.External.OllamaURL, "")
	scriptGen := ollama.NewGenerator(ollamaClient)

	docClient, err := drive.NewDocClient(context.Background(), cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err != nil {
		log.Warn("Docs client not initialized", zap.Error(err))
	}

	// Initialize stock database with migrations
	stockDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "stock.db.sqlite", log)
	if err != nil {
		log.Warn("Stock database not initialized", zap.Error(err))
	} else {
		migrationsDir := filepath.Join("internal", "storage", "migrations", "sqlite")
		if err := stockDB.RunMigrations(log, migrationsDir); err != nil {
			log.Warn("Failed to run migrations", zap.Error(err))
		}
	}

	cleanup := func() {
		if stockDB != nil {
			if err := stockDB.Close(); err != nil {
				log.Error("Failed to close stock database", zap.Error(err))
			}
		}
	}

	return &CoreDeps{
		ScriptGen: scriptGen,
		DocClient: docClient,
		Utility:   common.NewUtilityHandler(),
		StockDB:   stockDB,
	}, cleanup, nil
}
