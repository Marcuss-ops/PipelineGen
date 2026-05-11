package bootstrap

import (
	"fmt"

	"go.uber.org/zap"
	"velox/go-master/internal/storage"
	"velox/go-master/pkg/config"
)

type databases struct {
	main      *storage.SQLiteDB // General (Scripts, Runs, Jobs, Asset Index)
	stock     *storage.SQLiteDB // Stock footage
	clips     *storage.SQLiteDB // YouTube clips
	artlist   *storage.SQLiteDB // Artlist assets
	images    *storage.SQLiteDB // Images
	voiceover *storage.SQLiteDB // Voiceovers

	// Aliases for unified access
	jobs   *storage.SQLiteDB
	assets *storage.SQLiteDB
}

func (d *databases) Close() {
	if d.main != nil {
		d.main.Close()
	}
	if d.stock != nil {
		d.stock.Close()
	}
	if d.clips != nil {
		d.clips.Close()
	}
	if d.artlist != nil {
		d.artlist.Close()
	}
	if d.images != nil {
		d.images.Close()
	}
	if d.voiceover != nil {
		d.voiceover.Close()
	}
}

func initDatabases(cfg *config.Config, log *zap.Logger) (*databases, error) {
	// Initialize the 6 main databases
	mainDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, storage.DBVelox, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize main database: %w", err)
	}

	stockDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, storage.DBStock, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize stock database: %w", err)
	}

	clipsDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, storage.DBClips, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize clips database: %w", err)
	}

	artlistDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, storage.DBArtlist, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize artlist database: %w", err)
	}

	imagesDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, storage.DBImages, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize images database: %w", err)
	}

	voiceoverDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, storage.DBVoiceover, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize voiceover database: %w", err)
	}

	return &databases{
		main:      mainDB,
		stock:     stockDB,
		clips:     clipsDB,
		artlist:   artlistDB,
		images:    imagesDB,
		voiceover: voiceoverDB,
		
		// Map aliases to the main database
		jobs:   mainDB,
		assets: mainDB,
	}, nil
}
