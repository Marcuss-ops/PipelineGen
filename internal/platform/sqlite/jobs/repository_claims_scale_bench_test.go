package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// BenchmarkClaimNextMatchingQueueSizes measures the complete filtered claim
// selection path with a compatible job at the tail of the ordered queue. The
// sparse distribution restores its single matching row between iterations
// while the timer is stopped, so larger benchtime/count values still measure
// the same queue shape instead of failing after consuming the only match.
func BenchmarkClaimNextMatchingQueueSizes(b *testing.B) {
	for _, size := range []int{1_000, 10_000, 100_000} {
		for _, distribution := range []struct {
			name        string
			settleCount int
			ageDue      bool
		}{{"one-eligible", 1, false}, {"all-eligible", size, false}, {"hour-boundary", size, true}} {
			b.Run(fmt.Sprintf("queued=%d/%s", size, distribution.name), func(b *testing.B) {
				db := newBrokerTestDB(b)
				seedClaimBenchmarkJobs(b, db, size, distribution.settleCount, distribution.ageDue)
				store := NewSQLiteStore(db, zap.NewNop())
				ctx := context.Background()
				b.ReportAllocs()
				b.ResetTimer()
				lastClaimID := ""
				for i := 0; i < b.N; i++ {
					if i > 0 {
						b.StopTimer()
						resetClaimBenchmarkJob(b, db, lastClaimID)
						b.StartTimer()
					}
					if distribution.ageDue {
						b.StopTimer()
						if _, err := db.Exec(`UPDATE jobs SET claim_next_age_millis = 0 WHERE status = 'QUEUED'`); err != nil {
							b.Fatal(err)
						}
						b.StartTimer()
					}
					claimed, err := store.ClaimNextMatching(ctx, "bench-settle", time.Minute,
						[]string{"clip.render"}, job.PayloadMatch{"render_phase": "settle"})
					if err != nil {
						b.Fatal(err)
					}
					if claimed == nil {
						b.Fatal("claim missed compatible job")
					}
					lastClaimID = claimed.ID
				}
				b.StopTimer()
				logClaimQueryPlan(b, db)
			})
		}
	}
}

// BenchmarkClaimOrderingQuery compares the former per-row julianday aging
// expression with the persisted effective-priority index on identical queues.
// It isolates candidate ordering from payload hydration and the later CAS
// transition, so the query plans and timings explain the scheduler change
// itself. Queue sizes match the architecture audit's 1k/10k/100k matrix.
func BenchmarkClaimOrderingQuery(b *testing.B) {
	for _, size := range []int{1_000, 10_000, 100_000} {
		db := newBrokerTestDB(b)
		seedClaimBenchmarkJobs(b, db, size, size, false)
		queries := []struct {
			name  string
			query string
		}{
			{
				name: "legacy-dynamic-aging",
				query: `SELECT id FROM jobs WHERE status = 'QUEUED' AND type = 'clip.render'
					AND (available_at IS NULL OR available_at <= ?)
					ORDER BY priority + COALESCE(CAST((julianday('now') - julianday(created_at)) * 24.0 AS INTEGER), 0) DESC,
					created_at ASC, id ASC LIMIT 1`,
			},
			{
				name: "persisted-indexed-aging",
				query: `SELECT id FROM jobs WHERE status = 'QUEUED' AND type = 'clip.render'
					AND (available_at IS NULL OR available_at <= ?)
					ORDER BY claim_effective_priority DESC, created_at ASC, id ASC LIMIT 1`,
			},
		}
		for _, candidate := range queries {
			b.Run(fmt.Sprintf("queued=%d/%s", size, candidate.name), func(b *testing.B) {
				availableAt := time.Now().UTC().Format(time.RFC3339)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					var id string
					if err := db.QueryRow(candidate.query, availableAt).Scan(&id); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				logOrderingQueryPlan(b, db, candidate.query, availableAt)
			})
		}
	}
}

func logOrderingQueryPlan(b *testing.B, db *sql.DB, query string, args ...any) {
	b.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		b.Fatalf("explain claim-order query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			b.Fatalf("scan claim-order query plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		b.Fatalf("read claim-order query plan: %v", err)
	}
	b.Logf("claim-order query plan: %v", details)
}

func resetClaimBenchmarkJob(tb testing.TB, db *sql.DB, id string) {
	tb.Helper()
	if _, err := db.Exec(`UPDATE jobs SET status = 'QUEUED', worker_id = '',
		lease_id = '', lease_expiry = NULL, updated_at = ?, revision = revision + 1
		WHERE id = ?`, time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
		tb.Fatalf("reset sparse claim row: %v", err)
	}
}

func seedClaimBenchmarkJobs(tb testing.TB, db *sql.DB, n, settleCount int, ageDue bool) {
	tb.Helper()
	tx, err := db.Begin()
	if err != nil {
		tb.Fatalf("begin seed transaction: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO jobs
		(id, type, status, priority, payload_json, created_at, updated_at, revision, execution_lane, available_at,
		 claim_effective_priority, claim_next_age_millis)
		VALUES (?, 'clip.render', 'QUEUED', 10, ?, ?, ?, 1, ?, ?, 10, ?)`)
	if err != nil {
		_ = tx.Rollback()
		tb.Fatalf("prepare seed: %v", err)
	}
	createdAge := -time.Minute
	if ageDue {
		createdAge = -2 * time.Hour
	}
	createdAtTime := time.Now().UTC().Add(createdAge)
	createdAt := createdAtTime.Format(time.RFC3339Nano)
	for i := 0; i < n; i++ {
		phase := `{"render_phase":"submit"}`
		lane := "submit"
		if i >= n-settleCount {
			phase = `{"render_phase":"settle"}`
			lane = "settle"
		}
		nextAge := createdAtTime.UnixMilli() + 3_600_000
		if _, err := stmt.Exec(fmt.Sprintf("bench-job-%08d", i), phase, createdAt, createdAt, lane, createdAt, nextAge); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			tb.Fatalf("seed row %d: %v", i, err)
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		tb.Fatalf("close seed statement: %v", err)
	}
	if err := tx.Commit(); err != nil {
		tb.Fatalf("commit seed transaction: %v", err)
	}
}

func logClaimQueryPlan(b *testing.B, db *sql.DB) {
	b.Helper()
	availableAt := time.Now().UTC().Format(time.RFC3339)
	for _, lane := range []string{"settle", ""} {
		rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT id, claim_effective_priority, created_at
			FROM jobs WHERE status = 'QUEUED' AND type = 'clip.render' AND execution_lane = ?
			AND (available_at IS NULL OR available_at <= ?)
			ORDER BY claim_effective_priority DESC, created_at ASC, id ASC LIMIT 64`, lane, availableAt)
		if err != nil {
			b.Fatalf("explain claim query for lane %q: %v", lane, err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				_ = rows.Close()
				b.Fatalf("scan query plan: %v", err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			b.Fatalf("read query plan: %v", err)
		}
		_ = rows.Close()
		b.Logf("claim lane %q query plan: %v", lane, details)
	}
}
