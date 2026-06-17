package app

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"velox/go-master/internal/api/middleware"
	"velox/go-master/internal/config"
	"velox/go-master/internal/module"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// AppDeps holds the minimal initialized dependencies for the server.
type AppDeps struct {
	Registry *module.Registry
	Cleanup  func()
}

// openLogDB creates a separate SQLite database for API request logs.
// Isolating logs from the operational DB reduces contention, write amplification,
// and backup blast radius.
func openLogDB(dataDir string) (*sql.DB, error) {
	logDir := filepath.Join(dataDir, "logs")
	// Ensure directory exists; on failure, fall back to dataDir root
	_ = os.MkdirAll(logDir, 0755)

	logPath := filepath.Join(logDir, "api_requests.db.sqlite")
	dsn := logPath + "?_journal_mode=WAL&_busy_timeout=2000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open log db: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping log db: %w", err)
	}

	// Create the api_requests table if it doesn't exist.
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS api_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts DATETIME DEFAULT CURRENT_TIMESTAMP,
			request_id TEXT,
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			status INTEGER,
			duration_ms REAL,
			client_ip TEXT,
			user_id TEXT,
			bytes_in INTEGER,
			bytes_out INTEGER,
			user_agent TEXT,
			error TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_api_requests_ts ON api_requests(ts);
	`)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create log schema: %w", err)
	}
	return db, nil
}

// WireServices initializes the full server composition root.
func WireServices(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	coreDeps, coreClean, err := initCoreMinimal(cfg, log, mode)
	if err != nil {
		return nil, err
	}

	// Initialize persistent API request logging in a dedicated SQLite database.
	// This isolates operational data from high-volume log writes.
	var logDB *sql.DB
	if cfg.Storage.DataDir != "" {
		logDB, err = openLogDB(cfg.Storage.DataDir)
		if err != nil {
			log.Warn("failed to open dedicated log database, falling back to main DB", zap.Error(err))
			logDB = coreDeps.DB.DB
		}
	} else {
		logDB = coreDeps.DB.DB
	}
	middleware.SetLogDB(logDB)

	// Wire up the registry with all modules
	registryWiring, err := WireRegistry(coreDeps.Context, cfg, log, coreDeps)
	if err != nil {
		// Leak prevention: registry wiring failed, but core services and DB
		// are already open. Clean them up before returning the error.
		coreClean()
		return nil, err
	}

	// Freeze the registry and start the job runner after all modules are wired,
	// ensuring no new modules or job handlers can be registered while workers are active.
	registryWiring.Registry.Freeze()
	if coreDeps.startJobRunner != nil {
		coreDeps.startJobRunner()
	}

	// Build a LIFO cleanup stack so every new resource is freed in reverse
	// construction order.
	cleanupStack := make([]func(), 0, 8)
	cleanupStack = append(cleanupStack, coreClean)
	cleanupStack = append(cleanupStack, func() {
		if registryWiring.ArtlistSvc != nil && registryWiring.ArtlistSvc.Service != nil {
			registryWiring.ArtlistSvc.Service.Close()
		}
	})
	// Close log DB if it was opened separately (not the main DB)
	cleanupStack = append(cleanupStack, func() {
		if logDB != nil && logDB != coreDeps.DB.DB {
			if err := logDB.Close(); err != nil {
				log.Warn("failed to close log database", zap.Error(err))
			}
		}
	})
	// StopLogger must be last so it flushes any logs emitted by earlier cleanup.
	cleanupStack = append(cleanupStack, middleware.StopLogger)

	cleanup := func() {
		for i := len(cleanupStack) - 1; i >= 0; i-- {
			cleanupStack[i]()
		}
	}

	return &AppDeps{
		Registry: registryWiring.Registry,
		Cleanup:  cleanup,
	}, nil
}

// WireMinimal creates a minimal server with core services only.
// This is the recommended entry point for local tools and minimal deployments.
func WireMinimal(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	_, coreClean, err := initCoreMinimal(cfg, log, mode)
	if err != nil {
		return nil, err
	}
	return &AppDeps{
		Registry: nil,
		Cleanup:  coreClean,
	}, nil
}
