package localization

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// schedulerPlan returns a valid plan for the given language/priority.
func schedulerPlan(lang string, priority int) LocalizedClipPlan {
	p := validPlan()
	p.TargetLanguage = lang
	p.Priority = priority
	p.Fingerprint = Fingerprint(p)
	return p
}

// trackingRender returns a RenderFunc that counts live/max concurrency, sleeps
// per task, and returns an artifact (or a configured error).
func trackingRender(
	live *int32,
	max *int32,
	sleeps map[string]time.Duration,
	errs map[string]error,
) RenderFunc {
	return func(_ context.Context, plan LocalizedClipPlan) (LocalizedClipArtifact, error) {
		cur := atomic.AddInt32(live, 1)
		for {
			prev := atomic.LoadInt32(max)
			if cur <= prev || atomic.CompareAndSwapInt32(max, prev, cur) {
				break
			}
		}
		if d := sleeps[plan.TargetLanguage]; d > 0 {
			time.Sleep(d)
		}
		atomic.AddInt32(live, -1)
		if err := errs[plan.TargetLanguage]; err != nil {
			return LocalizedClipArtifact{}, err
		}
		return LocalizedClipArtifact{
			Language: plan.TargetLanguage,
			ClipID:   plan.ClipID,
			Status:   LocalizedClipRendered,
		}, nil
	}
}

// TestScheduler_RendersAllTasksInPriorityOrder verifies every submitted task
// renders and results come back in submission (priority) order.
func TestScheduler_RendersAllTasksInPriorityOrder(t *testing.T) {
	s, err := NewScheduler(context.Background(), func(_ context.Context, plan LocalizedClipPlan) (LocalizedClipArtifact, error) {
		return LocalizedClipArtifact{Language: plan.TargetLanguage, Status: LocalizedClipRendered}, nil
	}, 3)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	for i, lang := range []string{"en", "es", "it"} {
		s.Submit(LocalizedClipTask{Priority: i, Plan: schedulerPlan(lang, i)})
	}
	results := s.Wait()

	if len(results) != 3 {
		t.Fatalf("results: got %d, want 3", len(results))
	}
	for i, lang := range []string{"en", "es", "it"} {
		if results[i].Priority != i {
			t.Errorf("results[%d].Priority: got %d, want %d", i, results[i].Priority, i)
		}
		if results[i].Artifact.Language != lang {
			t.Errorf("results[%d].Artifact.Language: got %q, want %q", i, results[i].Artifact.Language, lang)
		}
		if results[i].Err != nil {
			t.Errorf("results[%d].Err: %v", i, results[i].Err)
		}
	}
}

// TestScheduler_PreservesPriorityOrderDespiteCompletionOrder verifies a
// lower-priority language finishing first never reorders the report: the
// source (priority 0, slow) stays first even though es (priority 1, fast)
// completes earlier.
func TestScheduler_PreservesPriorityOrderDespiteCompletionOrder(t *testing.T) {
	var live, max int32
	s, err := NewScheduler(context.Background(), trackingRender(&live, &max, map[string]time.Duration{
		"en": 60 * time.Millisecond, // source is slow
		"es": 1 * time.Millisecond,  // target finishes first
		"it": 2 * time.Millisecond,
	}, nil), 3)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	s.Submit(LocalizedClipTask{Priority: 0, Plan: schedulerPlan("en", 0)})
	s.Submit(LocalizedClipTask{Priority: 1, Plan: schedulerPlan("es", 1)})
	s.Submit(LocalizedClipTask{Priority: 2, Plan: schedulerPlan("it", 2)})
	results := s.Wait()

	for i, lang := range []string{"en", "es", "it"} {
		if results[i].Artifact.Language != lang {
			t.Fatalf("results[%d].Language: got %q, want %q (completion order leaked into the report)", i, results[i].Artifact.Language, lang)
		}
	}
}

// TestScheduler_BoundsConcurrency verifies the pool never exceeds
// render_concurrency simultaneous renders.
func TestScheduler_BoundsConcurrency(t *testing.T) {
	const concurrency = 2
	var live, max int32
	s, err := NewScheduler(context.Background(), trackingRender(&live, &max, map[string]time.Duration{
		"en": 5 * time.Millisecond,
		"es": 5 * time.Millisecond,
		"it": 5 * time.Millisecond,
		"fr": 5 * time.Millisecond,
	}, nil), concurrency)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	for i, lang := range []string{"en", "es", "it", "fr"} {
		s.Submit(LocalizedClipTask{Priority: i, Plan: schedulerPlan(lang, i)})
	}
	s.Wait()

	if got := atomic.LoadInt32(&max); got > concurrency {
		t.Fatalf("max concurrency: got %d, want <= %d", got, concurrency)
	}
	if got := atomic.LoadInt32(&max); got <= 0 {
		t.Fatalf("max concurrency never observed")
	}
}

// TestScheduler_PerTaskErrorDoesNotAbort verifies one failing task is
// recorded and the remaining tasks still render.
func TestScheduler_PerTaskErrorDoesNotAbort(t *testing.T) {
	s, err := NewScheduler(context.Background(), func(_ context.Context, plan LocalizedClipPlan) (LocalizedClipArtifact, error) {
		if plan.TargetLanguage == "es" {
			return LocalizedClipArtifact{}, errors.New("ffmpeg failed")
		}
		return LocalizedClipArtifact{Language: plan.TargetLanguage, Status: LocalizedClipRendered}, nil
	}, 3)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	s.Submit(LocalizedClipTask{Priority: 0, Plan: schedulerPlan("en", 0)})
	s.Submit(LocalizedClipTask{Priority: 1, Plan: schedulerPlan("es", 1)})
	s.Submit(LocalizedClipTask{Priority: 2, Plan: schedulerPlan("it", 2)})
	results := s.Wait()

	if results[0].Err != nil || results[0].Artifact.Status != LocalizedClipRendered {
		t.Errorf("results[0]: en must succeed, got err=%v", results[0].Err)
	}
	if results[1].Err == nil {
		t.Error("results[1]: es must record its error")
	}
	if results[2].Err != nil || results[2].Artifact.Status != LocalizedClipRendered {
		t.Errorf("results[2]: it must succeed despite es failing, got err=%v", results[2].Err)
	}
}

// TestScheduler_NilRenderFailsConstruction verifies a pool cannot be built
// without a render function.
func TestScheduler_NilRenderFailsConstruction(t *testing.T) {
	if _, err := NewScheduler(context.Background(), nil, 3); err == nil {
		t.Fatal("NewScheduler must reject a nil render func")
	}
}

// TestScheduler_ClampsConcurrencyToMinimum verifies concurrency < 1 is
// clamped to 1.
func TestScheduler_ClampsConcurrencyToMinimum(t *testing.T) {
	s, err := NewScheduler(context.Background(), func(_ context.Context, plan LocalizedClipPlan) (LocalizedClipArtifact, error) {
		return LocalizedClipArtifact{}, nil
	}, 0)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	if s.Concurrency() != 1 {
		t.Fatalf("Concurrency: got %d, want 1", s.Concurrency())
	}
}

// TestScheduler_ContextCancellation verifies cancelling the parent context
// propagates to the render funcs (the render observes ctx.Err()).
func TestScheduler_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, err := NewScheduler(ctx, func(ctx context.Context, plan LocalizedClipPlan) (LocalizedClipArtifact, error) {
		<-ctx.Done()
		return LocalizedClipArtifact{}, ctx.Err()
	}, 1)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	s.Submit(LocalizedClipTask{Priority: 0, Plan: schedulerPlan("en", 0)})
	cancel()
	results := s.Wait()

	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("cancelled render must record an error, got %+v", results)
	}
	if !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("error must be context.Canceled, got %v", results[0].Err)
	}
}

// TestScheduler_EmptyWaitReturnsEmpty verifies Wait on a pool with no tasks
// returns an empty slice (not nil surprise).
func TestScheduler_EmptyWaitReturnsEmpty(t *testing.T) {
	s, err := NewScheduler(context.Background(), func(_ context.Context, plan LocalizedClipPlan) (LocalizedClipArtifact, error) {
		return LocalizedClipArtifact{}, nil
	}, 3)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	results := s.Wait()
	if results == nil || len(results) != 0 {
		t.Fatalf("empty Wait: got %+v, want empty slice", results)
	}
}
