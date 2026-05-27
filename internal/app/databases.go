package app

import (
	"fmt"

	"go.uber.org/zap"
	"velox/go-master/internal/config"
	"velox/go-master/internal/storage"
)

type databases struct {
	main  *storage.SQLiteDB // General (Scripts, Runs, Jobs, Asset Index)
	media *storage.SQLiteDB // Unified Media (YouTube, Artlist, Stock, Images, Voiceovers)

	// Aliases for unified access
	jobs   *storage.SQLiteDB
	assets *storage.SQLiteDB
}

func (d *databases) Close() {
	if d.main != nil {
		d.main.Close()
	}
	if d.media != nil && d.media != d.main {
		d.media.Close()
	}
}

func initDatabases(cfg *config.Config, log *zap.Logger) (*databases, error) {
	mainDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, storage.DBVelox, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize main database: %w", err)
	}

	var mediaDB *storage.SQLiteDB
	if storage.DBMedia == storage.DBVelox {
		mediaDB = mainDB
	} else {
		mediaDB, err = storage.NewSQLiteDB(cfg.Storage.DataDir, storage.DBMedia, log)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize media database: %w", err)
		}
	}

	return &databases{
		main:  mainDB,
		media: mediaDB,

		jobs:   mainDB,
		assets: mainDB,
	}, nil
}
