package app

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

	// 2. Media database
	mediaMigrationsDir := filepath.Join("migrations", "media")
	if err := dbs.media.RunMigrations(log, mediaMigrationsDir); err != nil {
		return fmt.Errorf("failed to run media migrations: %w", err)
	}

	return nil
}
