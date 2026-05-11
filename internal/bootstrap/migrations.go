package bootstrap

import (
	"fmt"
	"path/filepath"

	"go.uber.org/zap"
)

// runAllMigrations applies database migrations to each database.
// Each database gets only the migrations relevant to its purpose.
func runAllMigrations(dbs *databases, log *zap.Logger) error {
	// 1. Generic/Main database (Velox)
	// This now includes Scripts, Pipeline, Jobs, and Asset Index migrations
	mainMigrationsDir := filepath.Join("migrations", "sqlite")
	if err := dbs.main.RunMigrations(log, mainMigrationsDir); err != nil {
		return fmt.Errorf("failed to run main migrations: %w", err)
	}

	// 2. Clips migrations (Stock, YouTube, Artlist)
	clipsMigrationsDir := filepath.Join("internal", "repository", "clips", "migrations")
	
	// Stock footage DB
	if err := dbs.stock.RunMigrations(log, clipsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run stock clips migrations: %w", err)
	}
	
	// YouTube clips DB
	if err := dbs.clips.RunMigrations(log, clipsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run clips database migrations: %w", err)
	}
	
	// Artlist assets DB
	if err := dbs.artlist.RunMigrations(log, clipsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run artlist database migrations: %w", err)
	}

	// 3. Images migrations
	imagesMigrationsDir := filepath.Join("internal", "repository", "images", "migrations")
	if err := dbs.images.RunMigrations(log, imagesMigrationsDir); err != nil {
		return fmt.Errorf("failed to run images migrations: %w", err)
	}

	// 4. Voiceover migrations
	voiceoverMigrationsDir := filepath.Join("internal", "repository", "voiceovers", "migrations")
	if err := dbs.voiceover.RunMigrations(log, voiceoverMigrationsDir); err != nil {
		return fmt.Errorf("failed to run voiceover migrations: %w", err)
	}

	return nil
}
