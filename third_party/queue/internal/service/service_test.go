package service

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/metrics"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestServiceRecordsQueueMetrics(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	m := metrics.New()
	svc.SetMetrics(m)

	if err := svc.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Submit(context.Background(), model.Job{ID: "job-2"}); err != nil {
		t.Fatal(err)
	}
	func() {
		svc.RefreshPendingGauge()
	}()
	if got := testutil.ToFloat64(m.JobsPending); got != 2 {
		t.Fatalf("pending: want 2, got %v", got)
	}

	if _, _, err := svc.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	svc.RefreshPendingGauge()
	if got := testutil.ToFloat64(m.JobsPending); got != 1 {
		t.Fatalf("pending after claim: want 1, got %v", got)
	}

	if err := svc.Complete(context.Background(), "job-1", "w1", model.Artifact{StorageKey: "sha", ArtifactHash: "sha", SizeBytes: 1, ContentType: "video/mp4"}); err != nil {
		t.Fatal(err)
	}
	svc.RefreshPendingGauge()
	if got := testutil.ToFloat64(m.JobsPending); got != 1 {
		t.Fatalf("pending after complete: want 1 (job-2 still queued), got %v", got)
	}

	if n := histogramSamples(t, m, "renderinggen_queue_wait_seconds"); n != 1 {
		t.Fatalf("queue_wait samples: want 1, got %d", n)
	}
	if n := histogramSamples(t, m, "renderinggen_render_duration_seconds"); n != 1 {
		t.Fatalf("render_duration samples: want 1, got %d", n)
	}
}

func TestPendingGaugeThrottleSkipsFullScans(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	m := metrics.New()
	svc.SetMetrics(m)

	if err := svc.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	svc.RefreshPendingGauge() // establishes the throttle clock
	// Within the throttle window the gauge must NOT be re-read from the
	// repository: this claim moves the job to running, so an unthrottled
	// observePending would drop the gauge to 0.
	if _, _, err := svc.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(m.JobsPending); got != 1 {
		t.Fatalf("throttled gauge changed within window: want 1 (stale), got %v", got)
	}
	// Forcing the refresh picks up the new state.
	svc.RefreshPendingGauge()
	if got := testutil.ToFloat64(m.JobsPending); got != 0 {
		t.Fatalf("forced refresh: want 0, got %v", got)
	}
}

func TestServiceRejectsCompletionWithoutArtifact(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Complete(context.Background(), "job-1", "w1", model.Artifact{}); err == nil {
		t.Fatal("expected empty artifact to be rejected")
	}
	job, err := svc.Get(context.Background(), "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.StateRunning {
		t.Fatalf("job state after rejected completion = %q, want running", job.State)
	}
}

func TestServiceLeaseExpiredMetric(t *testing.T) {
	svc := New(memory.New(10*time.Millisecond, 3))
	m := metrics.New()
	svc.SetMetrics(m)

	if err := svc.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(30 * time.Millisecond)
	n, err := svc.RequeueExpired(context.Background(), time.Now())
	if err != nil || n != 1 {
		t.Fatalf("requeue expired: n=%d err=%v", n, err)
	}
	if got := testutil.ToFloat64(m.LeaseExpired); got != 1 {
		t.Fatalf("lease_expired: want 1, got %v", got)
	}
}

func histogramSamples(t *testing.T, m *metrics.Metrics, name string) uint64 {
	t.Helper()
	families, err := m.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		if len(f.Metric) == 0 {
			return 0
		}
		return f.Metric[0].GetHistogram().GetSampleCount()
	}
	return 0
}

// TestOldestPendingGaugeTracksHeadAge pins the head-of-line backlog gauge the
// audit added: with a non-empty queue it must report a positive age, and it
// reads the same Stats snapshot as jobs_pending (no second scan of the jobs
// table on the refresh path).
func TestOldestPendingGaugeTracksHeadAge(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	m := metrics.New()
	svc.SetMetrics(m)
	ctx := context.Background()

	if err := svc.Submit(ctx, model.Job{ID: "job-old"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Submit(ctx, model.Job{ID: "job-new"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	svc.RefreshPendingGauge()
	if age := testutil.ToFloat64(m.OldestPending); age < 0.04 {
		t.Fatalf("oldest_pending with a non-empty queue: want >= 0.04s, got %v", age)
	}
	if got := testutil.ToFloat64(m.JobsPending); got != 2 {
		t.Fatalf("jobs_pending: want 2, got %v", got)
	}

	// A completed queue drains the age signal to zero: an autoscaler must be
	// able to distinguish "empty" from "stuck at a positive age".
	if _, _, err := svc.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Claim(context.Background(), "w2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Complete(ctx, "job-old", "w1", model.Artifact{StorageKey: "sha", ArtifactHash: "sha", SizeBytes: 1, ContentType: "video/mp4"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Complete(ctx, "job-new", "w2", model.Artifact{StorageKey: "sha", ArtifactHash: "sha", SizeBytes: 1, ContentType: "video/mp4"}); err != nil {
		t.Fatal(err)
	}
	svc.RefreshPendingGauge()
	if age := testutil.ToFloat64(m.OldestPending); age != 0 {
		t.Fatalf("oldest_pending on an empty queue: want 0, got %v", age)
	}
}
