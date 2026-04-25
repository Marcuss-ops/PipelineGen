package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// SQLiteDB holds the database connection and configuration.
type SQLiteDB struct {
	DB *sql.DB
}

// NewSQLiteDB creates a new SQLite database connection with connection pooling.
func NewSQLiteDB(dataDir, dbName string, log *zap.Logger) (*SQLiteDB, error) {
	dbPath := filepath.Join(dataDir, dbName)

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database %s: %w", dbPath, err)
	}

	// Connection pooling settings for SQLite
	db.SetMaxOpenConns(1) // SQLite only supports one writer at a time
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // Connections don't expire

	// Verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database %s: %w", dbPath, err)
	}

	log.Info("Database connection established", zap.String("path", dbPath))

	return &SQLiteDB{DB: db}, nil
}

// RunMigrations runs all pending migrations for the database.
func (s *SQLiteDB) RunMigrations(log *zap.Logger, migrationsDir string) error {
	runner := NewMigrationRunner(s.DB, log, migrationsDir)
	return runner.RunMigrations()
}

// Close closes the database connection.
func (s *SQLiteDB) Close() error {
	if s.DB != nil {
		return s.DB.Close()
	}
	return nil
}
