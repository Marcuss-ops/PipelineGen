// Package health provides concrete health-check adapters.
package health

import (
	"context"
	"database/sql"
	"time"

	healthport "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
)

// JobsChecker verifies the jobs table in the primary DB.
//
// The *sql.DB is supplied by the composition root so this checker
// reuses the canonical connection pool and does not open/close a
// fresh sqlite handle on every health probe.
type JobsChecker struct {
	db *sql.DB
}

// NewJobsChecker creates a job-broker-health checker for the supplied *sql.DB.
func NewJobsChecker(db *sql.DB) *JobsChecker {
	return &JobsChecker{db: db}
}

// CheckJobs verifies the jobs table exists and is reachable.
// Uses c.db.PingContext + c.db.QueryRowContext — no per-call open/close.
func (c *JobsChecker) CheckJobs(ctx context.Context) healthport.CheckResult {
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
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'",
	).Scan(&count); err != nil || count == 0 {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       "jobs table not found",
		}
	}
	return healthport.CheckResult{
		"ok":          true,
		"duration_ms": time.Since(start).Milliseconds(),
	}
}
