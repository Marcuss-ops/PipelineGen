package storage

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// SQLiteDB holds the database connection and configuration.
type SQLiteDB struct {
	DB     *sql.DB
	dbPath string
}

// NewSQLiteDB creates a new SQLite database connection with WAL mode and connection pooling.
func NewSQLiteDB(dataDir, dbName string, log *zap.Logger) (*SQLiteDB, error) {
	dbPath := filepath.Join(dataDir, dbName)

	// Use WAL mode for better concurrency: allows multiple readers with one writer
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database %s: %w", dbPath, err)
	}

	// Connection pooling settings - WAL mode allows more concurrent connections
	db.SetMaxOpenConns(10) // WAL mode supports multiple readers
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(0) // Connections don't expire

	// Enable WAL mode explicitly and optimize settings
	if err := enableWALMode(db, log); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	// Verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database %s: %w", dbPath, err)
	}

	log.Info("Database connection established with WAL mode", zap.String("path", dbPath))

	return &SQLiteDB{DB: db, dbPath: dbPath}, nil
}

// enableWALMode enables WAL journal mode and sets optimal SQLite pragmas.
func enableWALMode(db *sql.DB, log *zap.Logger) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA wal_autocheckpoint=1000",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL", // WAL mode: NORMAL is safe and faster
		"PRAGMA cache_size=-2000",   // 2MB cache
		"PRAGMA foreign_keys=ON",    // Enable foreign key constraints
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			log.Warn("Failed to set pragma", zap.String("pragma", pragma), zap.Error(err))
		}
	}

	// Verify WAL mode is enabled
	var journalMode string
	err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		return fmt.Errorf("failed to check journal mode: %w", err)
	}

	if journalMode != "wal" {
		log.Warn("WAL mode not enabled", zap.String("current_mode", journalMode))
	}

	return nil
}

// RunMigrations runs all pending migrations for the database.
func (s *SQLiteDB) RunMigrations(log *zap.Logger, migrationsDir string) error {
	runner := NewMigrationRunner(s.DB, log, migrationsDir)
	return runner.RunMigrations()
}

// Backup creates a backup of the database file.
func (s *SQLiteDB) Backup() error {
	if s.dbPath == "" {
		return fmt.Errorf("database path not set")
	}

	backupPath := s.dbPath + time.Now().Format(".20060102_150405.bak")
	return s.BackupTo(backupPath)
}

// BackupTo creates a backup of the database file to the specified path.
func (s *SQLiteDB) BackupTo(backupPath string) error {
	if s.dbPath == "" {
		return fmt.Errorf("database path not set")
	}

	// Use file copy for backup (simple and reliable)
	src, err := os.Open(s.dbPath)
	if err != nil {
		return fmt.Errorf("failed to open source database: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(backupPath)
	if err != nil {
		return fmt.Errorf("failed to create backup file: %w", err)
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	if err != nil {
		return fmt.Errorf("failed to copy database: %w", err)
	}

	return nil
}

// Close closes the database connection.
func (s *SQLiteDB) Close() error {
	if s.DB != nil {
		// Checkpoint WAL before closing to ensure all data is written
		if _, err := s.DB.Exec("PRAGMA wal_checkpoint(FULL)"); err != nil {
			return fmt.Errorf("failed to checkpoint WAL: %w", err)
		}
		return s.DB.Close()
	}
	return nil
}
