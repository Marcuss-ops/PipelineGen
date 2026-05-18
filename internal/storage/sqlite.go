package storage

import (
	"database/sql"
	"fmt"
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

		// Ensure parent directory exists
		parentDir := filepath.Dir(dbPath)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory %s: %w", parentDir, err)
		}

		dsn = dbPath + "?_journal_mode=WAL&_busy_timeout=5000"
	}

	return newSQLiteConnection(dbPath, dsn, 5, log)
}

// NewSQLiteDBWithMaxConns creates a new SQLite database connection with custom max open connections.
func NewSQLiteDBWithMaxConns(dataDir, dbName string, maxOpenConns int, log *zap.Logger) (*SQLiteDB, error) {
	dbPath := filepath.Join(dataDir, dbName)
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000"
	return newSQLiteConnection(dbPath, dsn, maxOpenConns, log)
}

// OpenSQLiteDB creates a new SQLite connection from a full file path with WAL mode and connection pooling.
// Use this for databases that are not in the standard data directory.
func OpenSQLiteDB(dbPath string, log *zap.Logger) (*SQLiteDB, error) {
	if dbPath != "" && dbPath != ":memory:" {
		parentDir := filepath.Dir(dbPath)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory %s: %w", parentDir, err)
		}
	}
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000"
	return newSQLiteConnection(dbPath, dsn, 5, log)
}

// newSQLiteConnection is the common implementation for creating a configured SQLite connection.
func newSQLiteConnection(dbPath, dsn string, maxOpenConns int, log *zap.Logger) (*SQLiteDB, error) {
	// Use WAL mode for better concurrency: allows multiple readers with one writer
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database %s: %w", dbPath, err)
	}

	// Connection pooling settings - WAL mode allows more concurrent connections
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxOpenConns / 2)
	if maxOpenConns == 1 {
		db.SetMaxIdleConns(1)
	}
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

	// Verify connection and set busy_timeout explicitly
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database %s: %w", dbPath, err)
	}

	// Ensure busy_timeout is set on the connection used for Ping
	var busyTimeout int
	err = db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout)
	if err != nil {
		log.Warn("Failed to query busy_timeout", zap.Error(err))
	} else if busyTimeout == 0 {
		log.Warn("busy_timeout is 0, setting it explicitly")
		_, err = db.Exec("PRAGMA busy_timeout=5000")
		if err != nil {
			log.Warn("Failed to set busy_timeout", zap.Error(err))
		}
	}

	log.Info("Database connection established with WAL mode", zap.String("path", dbPath), zap.Int("max_open_conns", maxOpenConns))

	return &SQLiteDB{DB: db, dbPath: dbPath}, nil
}

// enableWALMode enables WAL journal mode and sets optimal SQLite pragmas.
func enableWALMode(db *sql.DB, log *zap.Logger) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA wal_autocheckpoint=1000",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",    // WAL mode: NORMAL is safe and faster
		"PRAGMA cache_size=-64000",     // 64MB cache
		"PRAGMA temp_store=MEMORY",     // Use memory for temporary tables and indices
		"PRAGMA mmap_size=30000000000", // Use memory-mapped I/O (up to 30GB)
		"PRAGMA foreign_keys=ON",       // Enable foreign key constraints
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

// Backup creates a backup of the database file in a 'backups' subdirectory.
func (s *SQLiteDB) Backup() error {
	if s.dbPath == "" || s.dbPath == ":memory:" {
		return nil
	}

	// Ensure we use the directory where the database file is located
	dataDir := filepath.Dir(s.dbPath)
	backupDir := filepath.Join(dataDir, "backups")

	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory %s: %w", backupDir, err)
	}

	dbName := filepath.Base(s.dbPath)
	backupPath := filepath.Join(backupDir, dbName+time.Now().Format(".20060102_150405.bak"))
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

// Path returns the database file path.
func (s *SQLiteDB) Path() string {
	return s.dbPath
}
