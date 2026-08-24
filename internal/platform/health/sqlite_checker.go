// Package health provides concrete health-check adapters.
package health

import (
	"context"
	"time"

	healthport "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

// SQLiteChecker verifies the primary SQLite database.
//
// The *storage.SQLiteDB is supplied by the composition root so this
// checker reuses the canonical connection pool (WAL, busy_timeout, pool
// size) and does not open/close a fresh sqlite handle on every health
// probe. All *sql.DB methods are promoted through the embedded field,
// so c.db.PingContext / c.db.QueryRowContext resolve at compile time
// without leaking the underlying *sql.DB handle to the caller.
type SQLiteChecker struct {
	db *storage.SQLiteDB
}

// NewSQLiteChecker creates a DB-health checker for the supplied
// *storage.SQLiteDB.
func NewSQLiteChecker(db *storage.SQLiteDB) *SQLiteChecker {
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
