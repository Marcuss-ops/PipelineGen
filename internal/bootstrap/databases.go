package bootstrap

import (
	"fmt"

	"go.uber.org/zap"
	"velox/go-master/internal/storage"
	"velox/go-master/pkg/config"
)

type databases struct {
	main      *storage.SQLiteDB // General (Scripts, Runs, Jobs, Asset Index)
	media     *storage.SQLiteDB // Unified Media (YouTube, Artlist, Stock, Images, Voiceovers)

	// Aliases for unified access
	jobs   *storage.SQLiteDB
	assets *storage.SQLiteDB
}

func (d *databases) Close() {
	if d.main != nil {
		d.main.Close()
	}
	if d.media != nil {
		d.media.Close()
	}
}

func initDatabases(cfg *config.Config, log *zap.Logger) (*databases, error) {
	// Initialize the main database
	mainDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, storage.DBVelox, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize main database: %w", err)
	}

	// Initialize the unified media database
	mediaDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, storage.DBMedia, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize media database: %w", err)
	}

	return &databases{
		main:      mainDB,
		media:     mediaDB,

		// Map aliases to the main database
		jobs:   mainDB,
		assets: mainDB,
	}, nil
}
