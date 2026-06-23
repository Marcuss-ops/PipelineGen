// Package health provides concrete health-check adapters.
package health

import (
	"context"
	"database/sql"
	"time"

	healthport "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
)

// SQLiteChecker verifies the primary SQLite database.
//
// The *sql.DB is supplied by the composition root so this checker
// reuses the canonical connection pool (WAL, busy_timeout, pool size)
// and does not open/close a fresh sqlite handle on every health probe.
type SQLiteChecker struct {
	db *sql.DB
}

// NewSQLiteChecker creates a DB-health checker for the supplied *sql.DB.
func NewSQLiteChecker(db *sql.DB) *SQLiteChecker {
	return &SQLiteChecker{db: db}
}

// CheckDB verifies the database is reachable and the canonical
// media_assets table exists. The probe reuses c.db.PingContext +
// c.db.QueryRowContext — no sql.Open/defer Close per call.
func (c *SQLiteChecker) CheckDB(ctx context.Context) healthport.CheckResult {
	start := time.Now()
	if err := c.db.PingContext(ctx); err != nil {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       "database unreachable",
		}
	}
	var count int
	if err := c.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='media_assets'",
	).Scan(&count); err != nil || count == 0 {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       "migrations may not be applied",
		}
	}
	return healthport.CheckResult{
		"ok":          true,
		"duration_ms": time.Since(start).Milliseconds(),
	}
}
