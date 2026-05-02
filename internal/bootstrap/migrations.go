package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
)

func runAllMigrations(dbs *databases, log *zap.Logger) error {
	orchestrationMigrationsDir := filepath.Join("migrations", "sqlite")
	if err := dbs.main.RunMigrations(log, orchestrationMigrationsDir); err != nil {
		return fmt.Errorf("failed to run orchestration migrations: %w", err)
	}
	if err := dbs.stock.RunMigrations(log, orchestrationMigrationsDir); err != nil {
		return fmt.Errorf("failed to run stock orchestration migrations: %w", err)
	}

	scriptsMigrationsDir := filepath.Join("internal", "repository", "scripts", "migrations")
	if err := dbs.main.RunMigrations(log, scriptsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run scripts migrations: %w", err)
	}
	if err := dbs.stock.RunMigrations(log, scriptsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run stock scripts migrations: %w", err)
	}

	clipsMigrationsDir := filepath.Join("internal", "repository", "clips", "migrations")
	if err := dbs.main.RunMigrations(log, clipsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run clips migrations: %w", err)
	}
	if err := dbs.stock.RunMigrations(log, clipsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run stock clips migrations: %w", err)
	}
	if err := dbs.clips.RunMigrations(log, clipsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run clips database migrations: %w", err)
	}
	if err := dbs.artlist.RunMigrations(log, clipsMigrationsDir); err != nil {
		return fmt.Errorf("failed to run artlist database migrations: %w", err)
	}

	harvesterMigrationsDir := filepath.Join("internal", "repository", "harvester", "migrations")
	if err := os.MkdirAll(harvesterMigrationsDir, 0755); err == nil {
		if err := dbs.main.RunMigrations(log, harvesterMigrationsDir); err != nil {
			log.Warn("Failed to run harvester migrations", zap.Error(err))
		}
		if err := dbs.stock.RunMigrations(log, harvesterMigrationsDir); err != nil {
			log.Warn("Failed to run stock harvester migrations", zap.Error(err))
		}
	}

	imagesMigrationsDir := filepath.Join("internal", "repository", "images", "migrations")
	if err := dbs.images.RunMigrations(log, imagesMigrationsDir); err != nil {
		return fmt.Errorf("failed to run images migrations: %w", err)
	}

	voiceoversMigrationsDir := filepath.Join("internal", "repository", "voiceovers", "migrations")
	if err := dbs.voiceover.RunMigrations(log, voiceoversMigrationsDir); err != nil {
		return fmt.Errorf("failed to run voiceovers migrations: %w", err)
	}

	return nil
}
