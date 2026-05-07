package storage

import (
	"database/sql"
	"fmt"
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
// If dbName is ":memory:", an in-memory database is created (useful for testing).
func NewSQLiteDB(dataDir, dbName string, log *zap.Logger) (*SQLiteDB, error) {
	var dbPath string
	var dsn string

	if dbName == ":memory:" {
		// In-memory database - uses shared cache to allow multiple connections
		dsn = "file::memory:?cache=shared&_journal_mode=MEMORY&_busy_timeout=5000"
		dbPath = ":memory:"
		log.Info("Creating in-memory SQLite database")
	} else {
		dbPath = filepath.Join(dataDir, dbName)
		dsn = dbPath + "?_journal_mode=WAL&_busy_timeout=5000"
	}

	return newSQLiteConnection(dbPath, dsn, log)
}

// OpenSQLiteDB creates a new SQLite connection from a full file path with WAL mode and connection pooling.
// Use this for databases that are not in the standard data directory.
func OpenSQLiteDB(dbPath string, log *zap.Logger) (*SQLiteDB, error) {
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000"
	return newSQLiteConnection(dbPath, dsn, log)
}

// newSQLiteConnection is the common implementation for creating a configured SQLite connection.
func newSQLiteConnection(dbPath, dsn string, log *zap.Logger) (*SQLiteDB, error) {
	// Use WAL mode for better concurrency: allows multiple readers with one writer
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database %s: %w", dbPath, err)
	}

	// Connection pooling settings - WAL mode allows more concurrent connections
	db.SetMaxOpenConns(10) // WAL mode supports multiple readers
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(0) // Connections don't expire

	// Enable WAL mode explicitly and optimize settings (skip for in-memory)
	if dbPath != ":memory:" {
		if err := enableWALMode(db, log); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
		}
	} else {
		log.Info("Skipping WAL mode for in-memory database")
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
// For in-memory databases, this is a no-op.
func (s *SQLiteDB) Backup() error {
	if s.dbPath == "" || s.dbPath == ":memory:" {
		return nil // No file to backup for in-memory databases
	}

	backupPath := s.dbPath + time.Now().Format(".20060102_150405.bak")
	return s.BackupTo(backupPath)
}

// BackupTo creates a backup of the database using VACUUM INTO (safe with WAL mode).
func (s *SQLiteDB) BackupTo(backupPath string) error {
	if s.dbPath == "" {
		return fmt.Errorf("database path not set")
	}

	// Use VACUUM INTO for safe backup even with WAL mode
	// This creates a consistent snapshot without copying WAL files
	_, err := s.DB.Exec(fmt.Sprintf("VACUUM INTO '%s'", backupPath))
	if err != nil {
		return fmt.Errorf("failed to backup database using VACUUM INTO: %w", err)
	}

	return nil
}

// Close closes the database connection.
// For in-memory databases, this just closes the connection.
func (s *SQLiteDB) Close() error {
	if s.DB != nil {
		// Checkpoint WAL before closing to ensure all data is written (skip for in-memory)
		if s.dbPath != ":memory:" {
			if _, err := s.DB.Exec("PRAGMA wal_checkpoint(FULL)"); err != nil {
				return fmt.Errorf("failed to checkpoint WAL: %w", err)
			}
		}
		return s.DB.Close()
	}
	return nil
}
