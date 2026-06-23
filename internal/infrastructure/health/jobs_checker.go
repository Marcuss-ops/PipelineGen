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

// JobsChecker verifies the jobs table in the primary DB.
type JobsChecker struct {
	dbPath string
}

// NewJobsChecker creates a job-broker-health checker.
func NewJobsChecker(dataDir string) *JobsChecker {
	return &JobsChecker{
		dbPath: filepath.Join(dataDir, "media", "media.db.sqlite"),
	}
}

// CheckJobs verifies the jobs table exists and is reachable.
func (c *JobsChecker) CheckJobs(ctx context.Context) healthport.CheckResult {
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
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'",
	).Scan(&count); err != nil || count == 0 {
		return healthport.CheckResult{
			"ok": false, "duration_ms": time.Since(start).Milliseconds(),
			"error": "jobs table not found",
		}
	}

	return healthport.CheckResult{
		"ok":          true,
		"duration_ms": time.Since(start).Milliseconds(),
	}
}
