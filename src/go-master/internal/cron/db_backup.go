package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/storage"
)

// DBBackupJob handles periodic database backups
type DBBackupJob struct {
	db     *storage.SQLiteDB
	log    *zap.Logger
	backupDir string
}

// NewDBBackupJob creates a new database backup job
func NewDBBackupJob(db *storage.SQLiteDB, log *zap.Logger, backupDir string) *DBBackupJob {
	return &DBBackupJob{
		db:       db,
		log:      log,
		backupDir: backupDir,
	}
}

// Run executes the backup task
func (j *DBBackupJob) Run(ctx context.Context) error {
	j.log.Info("Starting database backup")

	if j.db == nil {
		return fmt.Errorf("database not initialized")
	}

	// Ensure backup directory exists
	if err := os.MkdirAll(j.backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Create timestamped backup
	timestamp := time.Now().Format("20060102_150405")
	backupPath := filepath.Join(j.backupDir, fmt.Sprintf("velox_backup_%s.db", timestamp))

	if err := j.db.BackupTo(backupPath); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	j.log.Info("Database backup completed", zap.String("path", backupPath))

	// Clean up old backups (keep last 7 days)
	if err := j.cleanupOldBackups(7); err != nil {
		j.log.Warn("Failed to cleanup old backups", zap.Error(err))
	}

	return nil
}

// cleanupOldBackups removes backups older than daysToKeep
func (j *DBBackupJob) cleanupOldBackups(daysToKeep int) error {
	files, err := os.ReadDir(j.backupDir)
	if err != nil {
		return fmt.Errorf("failed to read backup directory: %w", err)
	}

	cutoff := time.Now().AddDate(0, 0, -daysToKeep)
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		info, err := file.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			oldPath := filepath.Join(j.backupDir, file.Name())
			if err := os.Remove(oldPath); err != nil {
				j.log.Warn("Failed to remove old backup", zap.String("path", oldPath), zap.Error(err))
			} else {
				j.log.Info("Removed old backup", zap.String("path", oldPath))
			}
		}
	}

	return nil
}

// StartCron starts the periodic backup job
func (j *DBBackupJob) StartCron(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := j.Run(ctx); err != nil {
					j.log.Error("Backup job failed", zap.Error(err))
				}
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
}
