package app

import (
	"fmt"
	"path/filepath"

	"go.uber.org/zap"
	"velox/go-master/internal/config"
	"velox/go-master/internal/storage"
)

type databases struct {
	main    *storage.SQLiteDB // General (Scripts, Runs, Jobs, Asset Index)
	media   *storage.SQLiteDB // Unified Media (YouTube, Artlist, Stock, Images, Voiceovers)
	stock   *storage.SQLiteDB // Stock-specific clips
	artlist *storage.SQLiteDB // Artlist-specific clips

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
	if d.stock != nil {
		d.stock.Close()
	}
	if d.artlist != nil {
		d.artlist.Close()
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

	stockDB, err := openConfiguredSQLite(cfg.Storage.DataDir, cfg.Storage.StockDBFullPath(), log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize stock database: %w", err)
	}

	artlistDB, err := openConfiguredSQLite(cfg.Storage.DataDir, cfg.Storage.ArtlistDBFullPath(), log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize artlist database: %w", err)
	}

	return &databases{
		main:    mainDB,
		media:   mediaDB,
		stock:   stockDB,
		artlist: artlistDB,

		// Map aliases to the main database
		jobs:   mainDB,
		assets: mainDB,
	}, nil
}

func openConfiguredSQLite(dataDir, dbPath string, log *zap.Logger) (*storage.SQLiteDB, error) {
	if filepath.IsAbs(dbPath) {
		return storage.OpenSQLiteDB(dbPath, log)
	}
	return storage.NewSQLiteDB(dataDir, dbPath, log)
}
