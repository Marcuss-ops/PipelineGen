// Package storage provides SQLite database connectivity and migration
// infrastructure for PipelineGen. The package exposes a single SQLiteDB
// type that wraps *sql.DB with WAL mode, foreign keys, and a migration
// runner attached.
//
// All primary database operations flow through a single
// <DataDir>/media/media.db.sqlite file. Observability has its own
// explicitly separate database under <DataDir>/observability/.
package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3" // SQLite3 driver (CGO)
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/storage"
)

func GetAllDBs() []string {
	return []string{filepath.Join(storage.MediaDBDirectory, storage.MediaDBFilename)}
}

// GetDBPath resolves only the canonical primary database relative path.
// Returning an empty path makes unsupported or legacy health probes fail
// closed instead of opening another SQLite file. The canonical relative
// path is owned by storage.StorageTopology; this function only validates
// the caller-supplied path against it.
func GetDBPath(dataDir, dbRelPath string) string {
	canonical := filepath.Join(storage.MediaDBDirectory, storage.MediaDBFilename)
	if filepath.Clean(dbRelPath) != canonical {
		return ""
	}
	return filepath.Join(dataDir, canonical)
}

func OpenSQLiteDB(path string, log *zap.Logger) (*SQLiteDB, error) {
	if log == nil {
		log = zap.NewNop()
	}
	dsn := path + "?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("storage: ping %s: %w", path, err)
	}

	// SQLite serialises writes; a single connection avoids "database is locked"
	// under concurrent workloads (WAL mode allows concurrent readers).
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	return &SQLiteDB{DB: db, path: path, log: log}, nil
}

// DBMedia is the canonical database filename for the unified media database.
// Deprecated: prefer storage.MediaDBFilename from the SSOT topology package;
// this alias is retained for in-package callers that have not yet migrated.
const DBMedia = storage.MediaDBFilename

// SQLiteDB wraps a *sql.DB handle with the database file path and logger.
// It is the single connection point for the unified media database.
type SQLiteDB struct {
	*sql.DB

	path string
	log  *zap.Logger
}

// NewSQLiteDB opens (or creates) a SQLite database at
// <dataDir>/<dbName>. The database is configured with:
//   - WAL journal mode (better concurrency)
//   - foreign_keys = ON
//   - busy_timeout = 5000ms
//
// Returns an error if the data directory cannot be created or the
// database cannot be opened.
func NewSQLiteDB(dataDir, dbName string, log *zap.Logger) (*SQLiteDB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("storage: create data dir %s: %w", dataDir, err)
	}

	fullPath := filepath.Join(dataDir, dbName)
	dsn := fullPath + "?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000"

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", dbName, err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: ping %s: %w", dbName, err)
	}

	// Enable WAL mode explicitly (belt-and-suspenders with DSN param)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: enable WAL on %s: %w", dbName, err)
	}

	// Enable foreign keys explicitly
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: enable foreign keys on %s: %w", dbName, err)
	}

	// SQLite serialises writes; a single connection avoids "database is locked"
	// under concurrent workloads (WAL mode allows concurrent readers).
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	log.Info("SQLite database opened",
		zap.String("path", fullPath),
		zap.String("dbName", dbName),
	)

	return &SQLiteDB{
		DB:   db,
		path: fullPath,
		log:  log,
	}, nil
}

// Path returns the absolute filesystem path to the database file.
func (s *SQLiteDB) Path() string { return s.path }

// DBName returns the database filename component (e.g. "media.db.sqlite").
func (s *SQLiteDB) DBName() string { return filepath.Base(s.path) }
