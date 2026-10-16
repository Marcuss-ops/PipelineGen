package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/migrate"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// openRepos opens n independent *sql.DB connection pools against the same
// TEST_DATABASE_URL, applies the schema once and truncates the queue tables,
// returning one Repository per pool. This simulates multiple queue replicas
// or workers sharing a database rather than a single in-process pool.
func openRepos(t *testing.T, lease time.Duration, maxAttempts, n int) []*Repository {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping postgres integration test")
	}

	ctx := context.Background()
	repos := make([]*Repository, 0, n)
	for i := 0; i < n; i++ {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })

		if err := db.PingContext(ctx); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := migrate.Apply(ctx, db); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `TRUNCATE render_jobs CASCADE`); err != nil {
				t.Fatal(err)
			}
		}
		repos = append(repos, New(db, lease, maxAttempts))
	}
	return repos
}

// TestClaimConcurrentExclusiveAcrossRepositories runs many workers pulling
// jobs concurrently through several independent connection pools and asserts
// no job is ever handed to two workers (FOR UPDATE SKIP LOCKED holds across
// connections, not just within one pool).
func TestClaimConcurrentExclusiveAcrossRepositories(t *testing.T) {
	const jobs = 100
	const reposN = 5
	const workers = 25

	repos := openRepos(t, 30*time.Second, 3, reposN)
	for i := 0; i < jobs; i++ {
		if err := repos[0].Submit(context.Background(), model.Job{ID: fmt.Sprintf("job-%03d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	seen := make(map[string]string) // jobID -> workerID
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker string, repo *Repository) {
			defer wg.Done()
			for {
				job, _, err := repo.Claim(context.Background(), worker)
				if err != nil {
					t.Errorf("claim(%s): %v", worker, err)
					return
				}
				if job == nil {
					return
				}
				mu.Lock()
				if prev, dup := seen[job.ID]; dup {
					t.Errorf("job %s claimed by both %s and %s", job.ID, prev, worker)
				}
				seen[job.ID] = worker
				mu.Unlock()
			}
		}(fmt.Sprintf("w%d", w), repos[w%reposN])
	}
	wg.Wait()

	if len(seen) != jobs {
		t.Fatalf("want %d distinct claims, got %d", jobs, len(seen))
	}
	if s := repos[0].Stats(context.Background()); s.Running != jobs || s.Pending != 0 {
		t.Fatalf("want %d running / 0 pending, got %+v", jobs, s)
	}
}

// TestLeaseExpiryRequeuesAcrossRepositories verifies a job claimed through one
// connection pool is requeued and re-claimable through different pools once
// its lease expires, with the attempt history preserved.
func TestLeaseExpiryRequeuesAcrossRepositories(t *testing.T) {
	const lease = 30 * time.Millisecond
	repos := openRepos(t, lease, 3, 3)

	if err := repos[0].Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if job, _, err := repos[0].Claim(context.Background(), "w1"); err != nil || job == nil || job.ID != "job-1" {
		t.Fatalf("initial claim: job=%v err=%v", job, err)
	}

	time.Sleep(2 * lease) // let the lease expire

	// Requeue the expired lease through a different repository, then re-claim
	// through a third one.
	n, err := repos[1].RequeueExpired(context.Background(), time.Now())
	if err != nil || n != 1 {
		t.Fatalf("requeue expired: n=%d err=%v", n, err)
	}

	job, _, err := repos[2].Claim(context.Background(), "w2")
	if err != nil || job == nil || job.ID != "job-1" {
		t.Fatalf("re-claim: job=%v err=%v", job, err)
	}
	if job.Attempts != 2 {
		t.Fatalf("want attempts=2, got %d", job.Attempts)
	}
}
