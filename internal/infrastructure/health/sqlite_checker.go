// Package health provides concrete health-check adapters.
package health

import (
	"context"
	"database/sql"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"

	healthport "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
)

// SQLiteChecker verifies the primary SQLite database.
type SQLiteChecker struct {
	dbPath string
}

// NewSQLiteChecker creates a DB-health checker for the given path.
func NewSQLiteChecker(dataDir string) *SQLiteChecker {
	return &SQLiteChecker{
		dbPath: filepath.Join(dataDir, "media", "media.db.sqlite"),
	}
}

// CheckDB verifies the database is reachable and has core tables.
func (c *SQLiteChecker) CheckDB(ctx context.Context) healthport.CheckResult {
	start := time.Now()
	dsn := c.dbPath + "?_journal_mode=WAL&_busy_timeout=2000"

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": "cannot open database",
		}
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": "database unreachable",
		}
	}

	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='media_assets'",
	).Scan(&count); err != nil || count == 0 {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": "migrations may not be applied",
		}
	}

	return healthport.CheckResult{
		"ok":          true,
		"duration_ms": time.Since(start).Milliseconds(),
	}
}
