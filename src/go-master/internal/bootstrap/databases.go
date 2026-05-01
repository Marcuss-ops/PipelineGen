package bootstrap

import (
	"fmt"

	"velox/go-master/internal/storage"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

type databases struct {
	main    *storage.SQLiteDB
	stock   *storage.SQLiteDB
	clips   *storage.SQLiteDB
	artlist *storage.SQLiteDB
	images  *storage.SQLiteDB
}

func initDatabases(cfg *config.Config, log *zap.Logger) (*databases, error) {
	mainDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "velox.db.sqlite", log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize main database: %w", err)
	}

	stockDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "stock.db.sqlite", log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize stock database: %w", err)
	}

	clipsDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "clips.db.sqlite", log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize clips database: %w", err)
	}

	artlistDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "artlist.db.sqlite", log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize artlist database: %w", err)
	}

	imagesDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "images.db.sqlite", log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize images database: %w", err)
	}

	return &databases{
		main:    mainDB,
		stock:   stockDB,
		clips:   clipsDB,
		artlist: artlistDB,
		images:  imagesDB,
	}, nil
}
