package observability

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAcquireSlot_ContentionAcrossConcurrencyLevels(t *testing.T) {
	for _, concurrency := range []int{1, 2, 3, 5} {
		t.Run("concurrency_"+itoa(concurrency), func(t *testing.T) {
			run := NewRunObserver(nil).StartRun(context.Background(), RunInfo{JobID: "job", AttemptID: "attempt"})
			ctx := WithRun(context.Background(), run)
			sem := make(chan struct{}, concurrency)
			var releases []func()
			for i := 0; i < concurrency; i++ {
				release, err := AcquireSlot(ctx, sem, ComponentRenderQueue, WaitSemaphore)
				if err != nil {
					t.Fatal(err)
				}
				releases = append(releases, release)
			}
			blocked := make(chan error, 1)
			go func() {
				release, err := AcquireSlot(ctx, sem, ComponentRenderQueue, WaitSemaphore)
				if release != nil {
					defer release()
				}
				blocked <- err
			}()
			select {
			case <-blocked:
				t.Fatal("contending acquisition completed before a slot was released")
			case <-time.After(5 * time.Millisecond):
			}
			for _, release := range releases {
				release()
			}
			select {
			case err := <-blocked:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("contending acquisition did not complete")
			}
			report := run.Report()
			if len(report.Waits) != concurrency+1 {
				t.Fatalf("waits=%d, want %d (initial acquisitions plus one contender)", len(report.Waits), concurrency+1)
			}
			contended := report.Waits[len(report.Waits)-1]
			if contended.Kind != WaitSemaphore || contended.Component != string(ComponentRenderQueue) {
				t.Fatalf("wait=%+v", contended)
			}
			if contended.DurationMs <= 0 {
				t.Fatalf("wait duration=%d, want positive", contended.DurationMs)
			}
		})
	}
}

func TestAcquireSlot_CancellationDoesNotLeak(t *testing.T) {
	run := NewRunObserver(nil).StartRun(context.Background(), RunInfo{JobID: "job", AttemptID: "attempt"})
	ctx, cancel := context.WithCancel(WithRun(context.Background(), run))
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	cancel()
	release, err := AcquireSlot(ctx, sem, ComponentDrive, WaitSemaphore)
	if release != nil || err == nil {
		t.Fatalf("release_is_nil=%v err=%v, want cancellation without release", release == nil, err)
	}
	if len(run.Report().Waits) != 1 {
		t.Fatalf("waits=%d, want cancellation wait recorded", len(run.Report().Waits))
	}
}

func TestAcquireSlot_ConcurrentContendersRaceSafe(t *testing.T) {
	run := NewRunObserver(nil).StartRun(context.Background(), RunInfo{JobID: "job", AttemptID: "attempt"})
	ctx := WithRun(context.Background(), run)
	sem := make(chan struct{}, 1)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := AcquireSlot(ctx, sem, ComponentRenderQueue, WaitSemaphore)
			if err == nil {
				time.Sleep(time.Millisecond)
				release()
			}
		}()
	}
	wg.Wait()
	if len(run.Report().Waits) < 1 {
		t.Fatal("expected contention waits")
	}
}

func itoa(v int) string {
	if v == 1 {
		return "1"
	}
	if v == 2 {
		return "2"
	}
	if v == 3 {
		return "3"
	}
	return "5"
}
