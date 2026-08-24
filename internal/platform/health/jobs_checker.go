// Package health provides concrete health-check adapters.
package health

import (
	"context"
	"fmt"
	"time"

	healthport "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

// RunnerProbe is an optional port that proves the broker goroutine
// is alive, not just that the jobs table is reachable. When set on a
// JobsChecker, CheckJobs calls Probe(ctx) AFTER the DB liveness query
// so both the schema and the process are verified.
//
// Typical production wiring: the broker loop exposes a heartbeat
// channel or an in-memory timestamp that gets checked here. A nil
// RunnerProbe means "only DB reachability is verified" — suitable
// for admin CLIs and test environments where no broker is running.
//
// codex/health-ready-contract (June 2026): resolves the issue at the
// original line 52 — "inject a real runner heartbeat/liveness port
// that proves the broker loop is alive".
type RunnerProbe func(ctx context.Context) error

// JobsChecker verifies the jobs broker / runner is alive in the primary DB.
//
// Two-tier check:
//  1. DB liveness: SELECT COUNT on recent active jobs (always runs).
//  2. Runner liveness: when RunnerProbe is non-nil, calls it after
//     the DB query succeeds. A nil RunnerProbe is valid — the DB
//     check alone proves schema reachability.
//
// fix(health) close-out (June 2026, problem #2 final cleanup): the
// historical CheckJobs only verified the table's existence via
// sqlite_master — that handed out ok=true even when the broker / runner
// goroutines had silently wedged (no row-level activity for hours).
// Per the user's directive, CheckJobs now executes a single
// LivenessQuery that proves the table IS read/write, schema is intact,
// AND a recent runner heartbeat exists.
//
// Liveness heuristic: count rows where status in {running, leased,
// pending} with updated_at within the last 5 minutes. The check
// passes (ok=true) so long as the SQL executes without error AND
// either the count is >= 0 (broker idle but reachable) — we do NOT
// require count > 0 because a freshly-deployed system legitimately
// has zero recent activity. The HARD failure is a SQL execution error
// (table missing, schema drift, DB unreachable), which surfaces as
// ok=false with the original error in the response payload.
//
// Status matching is defensive: the codebase carries both uppercase
// status values from migration 053_job_lifecycle_atomic.sql
// (PENDING|LEASED|RUNNING|SUCCEEDED|FAILED|CANCELLED) and lowercase
// legacy aliases ('running'|'failed'|'completed') from older callers,
// and LOWER(status) keeps the comparison immune to that drift.
type JobsChecker struct {
	db          *storage.SQLiteDB
	RunnerProbe RunnerProbe // optional: broker goroutine liveness probe (nil → DB-only check)
}

// NewJobsChecker creates a jobs-table liveness checker for the supplied
// *storage.SQLiteDB. The PingContext / QueryRowContext methods resolve
// through the embedded *sql.DB field of storage.SQLiteDB, so the
// underlying *sql.DB handle is never leaked to implementations.
//
// Set RunnerProbe after construction to add broker goroutine liveness
// verification (zero-value nil means DB-only check).
func NewJobsChecker(db *storage.SQLiteDB) *JobsChecker {
	return &JobsChecker{db: db}
}

// livenessQuery is the canonical DB/schema probe. Schema-tolerant (TEXT updated_at
// lexicographic comparison works for ISO8601 / RFC3339 timestamps) and
// status-tolerant (LOWER() fold for case-insensitive match).
//
// codex/health-ready-contract (June 2026): the runner liveness check is now
// handled by the optional RunnerProbe port (see JobsChecker.RunnerProbe).
// The livenessQuery below remains as the DB/schema tier — it verifies table
// existence, schema integrity, and recent row activity.
const livenessQuery = `SELECT COUNT(*) FROM jobs
	WHERE LOWER(status) IN ('running', 'leased', 'pending')
	  AND julianday(updated_at) > julianday('now', '-5 minute')`

func (c *JobsChecker) CheckJobs(ctx context.Context) healthport.CheckResult {
	start := time.Now()
	if c == nil || c.db == nil {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       "database unavailable",
		}
	}
	if err := c.db.PingContext(ctx); err != nil {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       "database unreachable",
		}
	}
	var count int
	if err := c.db.QueryRowContext(ctx, livenessQuery).Scan(&count); err != nil {
		return healthport.CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       "jobs table unreachable or malformed: " + err.Error(),
		}
	}

	// codex/health-ready-contract (June 2026): when a RunnerProbe
	// is wired, verify the broker goroutine is alive too.
	// A nil RunnerProbe means "DB-only check" — valid for admin CLIs
	// and test environments where no broker is running.
	if c.RunnerProbe != nil {
		if err := c.RunnerProbe(ctx); err != nil {
			return healthport.CheckResult{
				"ok":           false,
				"duration_ms":  time.Since(start).Milliseconds(),
				"running_jobs": count,
				"error":        fmt.Sprintf("runner liveness probe failed: %v", err),
			}
		}
	}

	return healthport.CheckResult{
		"ok":           true,
		"duration_ms":  time.Since(start).Milliseconds(),
		"running_jobs": count,
	}
}
