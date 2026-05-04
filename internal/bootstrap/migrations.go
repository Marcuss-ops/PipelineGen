// Package bootstrap initializes the application: databases, migrations, and service wiring.
//
// Migration Strategy:
//   - Main migrations (migrations/sqlite/) → velox.db.sqlite only
//   - Scripts migrations → velox.db.sqlite only
//   - Clips migrations (internal/repository/clips/migrations/) → stock.db, clips.db, artlist.db
//   - Jobs migrations (migrations/jobs/) → jobs.db.sqlite only
//   - Harvester migrations → velox.db.sqlite only
//
// Critical: Never apply clips migrations to velox.db.sqlite.
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
	// Apply main migrations only to main DB (velox.db)
	orchestrationMigrationsDir := filepath.Join("migrations", "sqlite")
	if err := dbs.main.RunMigrations(log, orchestrationMigrationsDir); err != nil {
		return fmt.Errorf("failed to run orchestration migrations: %w", err)
	}

	scriptsMigrationsDir := filepath.Join("internal", "repository", "scripts", "migrations")
	if err := dbs.main.RunMigrations(log, scriptsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run scripts migrations: %w", err)
	}

	// Apply clips migrations only to databases that need clips tables
	clipsMigrationsDir := filepath.Join("internal", "repository", "clips", "migrations")
	// Stock DB needs clips tables (uses clipsRepo)
	if err := dbs.stock.RunMigrations(log, clipsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run stock clips migrations: %w", err)
	}
	// clips.db needs clips tables (YouTube clips)
	if err := dbs.clips.RunMigrations(log, clipsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run clips database migrations: %w", err)
	}
	// artlist.db needs clips tables (Artlist clips)
	if err := dbs.artlist.RunMigrations(log, clipsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run artlist database migrations: %w", err)
	}

	// Harvester migrations only to main DB
	harvesterMigrationsDir := filepath.Join("internal", "repository", "harvester", "migrations")
	if err := os.MkdirAll(harvesterMigrationsDir, 0755); err == nil {
		if err := dbs.main.RunMigrations(log, harvesterMigrationsDir); err != nil {
			log.Warn("Failed to run harvester migrations", zap.Error(err))
		}
	}

	// Images migrations only to images DB
	imagesMigrationsDir := filepath.Join("internal", "repository", "images", "migrations")
	if err := dbs.images.RunMigrations(log, imagesMigrationsDir); err != nil {
		return fmt.Errorf("failed to run images migrations: %w", err)
	}

	// Voiceover migrations only to voiceover DB
	voiceoversMigrationsDir := filepath.Join("internal", "repository", "voiceovers", "migrations")
	if err := dbs.voiceover.RunMigrations(log, voiceoversMigrationsDir); err != nil {
		return fmt.Errorf("failed to run voiceovers migrations: %w", err)
	}

	// Media migrations only to main DB
	mediaMigrationsDir := filepath.Join("internal", "repository", "media", "migrations")
	if err := dbs.main.RunMigrations(log, mediaMigrationsDir); err != nil {
		return fmt.Errorf("failed to run media migrations: %w", err)
	}

	return nil
}
