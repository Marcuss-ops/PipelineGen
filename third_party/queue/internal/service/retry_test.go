package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/metrics"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// flakyRepo wraps the in-memory repository and fails RequeueExpired a fixed
// number of times before delegating, to exercise the service retry loop.
type flakyRepo struct {
	*memory.Repository
	failures int // remaining failures before delegating
	calls    int
}

func (f *flakyRepo) RequeueExpired(ctx context.Context, now time.Time) (int, error) {
	f.calls++
	if f.failures > 0 {
		f.failures--
		return 0, errors.New("transient failure")
	}
	return f.Repository.RequeueExpired(ctx, now)
}

func TestBackoffDelayDeterministic(t *testing.T) {
	cfg := RetryConfig{BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second}

	want := []time.Duration{
		100 * time.Millisecond, // after 1st failure
		200 * time.Millisecond, // after 2nd
		400 * time.Millisecond, // after 3rd
		800 * time.Millisecond, // after 4th
		time.Second,            // after 5th (capped)
		time.Second,            // after 6th (capped)
	}
	for failed, wantDelay := range want {
		if got := backoffDelay(cfg, failed+1); got != wantDelay {
			t.Fatalf("failed=%d: want %s, got %s", failed+1, wantDelay, got)
		}
	}
}

func TestBackoffDelayJitterBounds(t *testing.T) {
	cfg := RetryConfig{BaseDelay: 100 * time.Millisecond, Jitter: 0.5}

	lower := 50 * time.Millisecond
	upper := 100 * time.Millisecond
	for i := 0; i < 1000; i++ {
		got := backoffDelay(cfg, 1)
		if got < lower || got > upper {
			t.Fatalf("jittered delay %s out of bounds [%s, %s]", got, lower, upper)
		}
	}
}

func TestRequeueExpiredRetriesThenSucceeds(t *testing.T) {
	repo := &flakyRepo{Repository: memory.New(5*time.Millisecond, 3), failures: 2}

	// Lease one job so a successful requeue reports n=1.
	if err := repo.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // let the lease expire

	svc := New(repo)
	svc.SetRequeueRetry(RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond})

	n, err := svc.RequeueExpired(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if repo.calls != 3 {
		t.Fatalf("want 3 attempts, got %d", repo.calls)
	}
	if n != 1 {
		t.Fatalf("want 1 requeued job, got %d", n)
	}
}

func TestRequeueExpiredGivesUpAfterMaxAttempts(t *testing.T) {
	repo := &flakyRepo{Repository: memory.New(5*time.Millisecond, 3), failures: 100}

	svc := New(repo)
	svc.SetRequeueRetry(RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond})

	if _, err := svc.RequeueExpired(context.Background(), time.Now()); err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if repo.calls != 3 {
		t.Fatalf("want 3 attempts before giving up, got %d", repo.calls)
	}
}

// TestRequeueExpiredMetricCountedOnce ensures the lease_expired counter is
// incremented only on the successful attempt, not once per retry.
func TestRequeueExpiredMetricCountedOnce(t *testing.T) {
	repo := &flakyRepo{Repository: memory.New(5*time.Millisecond, 3), failures: 2}

	if err := repo.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	svc := New(repo)
	m := metrics.New()
	svc.SetMetrics(m)
	svc.SetRequeueRetry(RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond})

	if _, err := svc.RequeueExpired(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(m.LeaseExpired); got != 1 {
		t.Fatalf("lease_expired: want 1, got %v", got)
	}
}

// TestRequeueExpiredBackoffIsCancellable pins the shutdown behavior of the
// retry loop: a cancelled context must interrupt the BACKOFF, not wait it out.
// The delay is deliberately long (30s) so a regression to a blocking sleep
// would hang this test instead of passing quietly.
func TestRequeueExpiredBackoffIsCancellable(t *testing.T) {
	repo := &flakyRepo{Repository: memory.New(5*time.Millisecond, 3), failures: 100}

	svc := New(repo)
	svc.SetRequeueRetry(RetryConfig{MaxAttempts: 3, BaseDelay: 30 * time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond) // let the first attempt fail
		cancel()
	}()

	start := time.Now()
	n, err := svc.RequeueExpired(ctx, time.Now())
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want the context error, got n=%d err=%v", n, err)
	}
	if n != 0 {
		t.Fatalf("a cancelled sweep must not report jobs as requeued, got %d", n)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("cancellation took %s; the backoff is not waiting on the context", elapsed)
	}
}
