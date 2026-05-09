// Package bootstrap initializes the application: databases, migrations, and service wiring.
//
// Migration Strategy:
//   - Main migrations (migrations/sqlite/) → velox.db.sqlite only
//   - Scripts migrations → velox.db.sqlite only
//   - Clips migrations (internal/repository/clips/migrations/) → stock.db, clips.db, artlist.db
//   - Clips timeline migrations (internal/repository/clips/migrations_timeline/) → clips.db only
//   - Jobs migrations (migrations/jobs/) → jobs.db.sqlite only
//   - Harvester migrations → velox.db.sqlite only
//
// Critical: Never apply clips migrations to velox.db.sqlite.
// segment_embeddings is ONLY for clips.db.sqlite (timeline cache).
// See docs/sqlite-databases.md for schema boundaries.
package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
)

// runAllMigrations applies database migrations to each database.
// Each database gets only the migrations relevant to its purpose.
func runAllMigrations(dbs *databases, log *zap.Logger) error {
	// 1. Create all tables first
	
	// Scripts migrations -> velox.db.sqlite
	scriptsMigrationsDir := filepath.Join("internal", "repository", "scripts", "migrations")
	if err := dbs.main.RunMigrations(log, scriptsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run scripts migrations: %w", err)
	}

	// Harvester migrations -> velox.db.sqlite
	harvesterMigrationsDir := filepath.Join("internal", "repository", "harvester", "migrations")
	if err := os.MkdirAll(harvesterMigrationsDir, 0755); err == nil {
		if err := dbs.main.RunMigrations(log, harvesterMigrationsDir); err != nil {
			log.Warn("Failed to run harvester migrations", zap.Error(err))
		}
	}

	// Media migrations -> velox.db.sqlite
	mediaMigrationsDir := filepath.Join("internal", "repository", "media", "migrations")
	if err := dbs.main.RunMigrations(log, mediaMigrationsDir); err != nil {
		return fmt.Errorf("failed to run media migrations: %w", err)
	}

	// Main orchestration migrations (contains table creations 001-004, 006-007)
	// NOTE: 005 contains indexes for tables created above, so we might need to split it 
	// or ensure it runs after them. Since RunMigrations runs the whole directory, 
	// we'll keep it here but we've already run the external ones.
	orchestrationMigrationsDir := filepath.Join("migrations", "sqlite")
	if err := dbs.main.RunMigrations(log, orchestrationMigrationsDir); err != nil {
		return fmt.Errorf("failed to run orchestration migrations: %w", err)
	}

	// 2. Apply migrations to other databases

	// Apply clips migrations only to databases that need clips tables
	clipsMigrationsDir := filepath.Join("internal", "repository", "clips", "migrations")
	// Stock DB needs clips tables (uses clipsRepo)
	if err := dbs.stock.RunMigrations(log, clipsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run stock clips migrations: %w", err)
	}
	// clips.db needs clips tables (YouTube clips) + segment_embeddings
	if err := dbs.clips.RunMigrations(log, clipsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run clips database migrations: %w", err)
	}
	// Apply segment_embeddings migration ONLY to clips.db.sqlite (timeline cache)
	clipsTimelineMigrationsDir := filepath.Join("internal", "repository", "clips", "migrations_timeline")
	if err := dbs.clips.RunMigrations(log, clipsTimelineMigrationsDir); err != nil {
		return fmt.Errorf("failed to run clips timeline migrations: %w", err)
	}
	// artlist.db needs clips tables (Artlist clips) but NOT segment_embeddings
	if err := dbs.artlist.RunMigrations(log, clipsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run artlist database migrations: %w", err)
	}

	// Images migrations only to images DB
	imagesMigrationsDir := filepath.Join("internal", "repository", "images", "migrations")
	if err := dbs.images.RunMigrations(log, imagesMigrationsDir); err != nil {
		return fmt.Errorf("failed to run images migrations: %w", err)
	}

	// Voiceover migrations only to voiceover DB
	// We apply both potential migration locations to ensure full schema consistency
	voiceoversRepoMigrationsDir := filepath.Join("internal", "repository", "voiceovers", "migrations")
	if err := dbs.voiceover.RunMigrations(log, voiceoversRepoMigrationsDir); err != nil {
		log.Warn("failed to run voiceover repo migrations", zap.Error(err))
	}
	voiceoversLegacyMigrationsDir := filepath.Join("migrations", "voiceovers")
	if err := dbs.voiceover.RunMigrations(log, voiceoversLegacyMigrationsDir); err != nil {
		log.Warn("failed to run voiceover legacy migrations", zap.Error(err))
	}

	// Jobs migrations -> jobs.db.sqlite
	jobsMigrationsDir := filepath.Join("migrations", "jobs")
	if err := dbs.jobs.RunMigrations(log, jobsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run jobs migrations: %w", err)
	}

	// Asset Index migrations -> assets.db.sqlite
	assetIndexMigrationsDir := filepath.Join("migrations", "asset_index")
	if err := dbs.assets.RunMigrations(log, assetIndexMigrationsDir); err != nil {
		return fmt.Errorf("failed to run asset index migrations: %w", err)
	}

	return nil
}

