// Package bootstrap initializes the application: databases, migrations, and service wiring.
//
// Database Architecture:
//   - velox.db.sqlite  → mainDB: scripts, media_items, monitored_sources, harvester_jobs
//   - stock.db.sqlite  → stockDB: stock footage clips
//   - clips.db.sqlite  → clipsDB: YouTube clips + segment_embeddings
//   - artlist.db.sqlite → artlistDB: Artlist assets
//   - images.db.sqlite → imagesDB: placeholder for image tables
//   - voiceover.db.sqlite → voiceoverDB: placeholder for voiceover tables
//   - jobs.db.sqlite   → jobsDB: job queue
//
// See docs/sqlite-databases.md for schema boundaries and migration strategy.
package bootstrap

import (
	"fmt"

	"velox/go-master/internal/storage"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

type databases struct {
	main      *storage.SQLiteDB
	stock     *storage.SQLiteDB
	clips     *storage.SQLiteDB
	artlist   *storage.SQLiteDB
	images    *storage.SQLiteDB
	voiceover *storage.SQLiteDB
	assets    *storage.SQLiteDB
	jobs      *storage.SQLiteDB
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

	voiceoverDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "voiceover.db.sqlite", log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize voiceover database: %w", err)
	}

	jobsDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "jobs.db.sqlite", log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize jobs database: %w", err)
	}
	if err := jobsDB.RunMigrations(log, "migrations/jobs"); err != nil {
		return nil, fmt.Errorf("failed to run jobs migrations: %w", err)
	}

	assetsDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "assets.db.sqlite", log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize assets database: %w", err)
	}
	if err := assetsDB.RunMigrations(log, "migrations/asset_index"); err != nil {
		return nil, fmt.Errorf("failed to run asset index migrations: %w", err)
	}

	// Log FTS5 status once (driver-dependent, not DB-dependent)
	storage.LogFTS5Status(log, mainDB)

	return &databases{
		main:      mainDB,
		stock:     stockDB,
		clips:     clipsDB,
		artlist:   artlistDB,
		images:    imagesDB,
		voiceover: voiceoverDB,
		assets:    assetsDB,
		jobs:      jobsDB,
	}, nil
}
