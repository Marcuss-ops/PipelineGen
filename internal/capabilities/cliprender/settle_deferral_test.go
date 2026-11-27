// Package cliprender — settle_deferral_test.go pins the deferrable settle.
//
// Before this, a settle continuation HELD its worker lane for the whole remote
// render (minutes; up to the job timeout). The lane pool is what bounds how many
// clips can be in flight, so a slow GPU backlog turned into queued settles.
//
// The contract under test:
//
//   - inside the deferral window, one settle attempt waits at most the settle
//     budget and then hands the attempt back with job.DeferredAfter — a WAIT,
//     not a failure, and one that spends no retry budget;
//   - once the window has closed, the settle stops asking and waits the render
//     out in a single attempt (the historical behaviour) — so a render that
//     never reports terminal cannot be re-dispatched forever;
//   - a boundary that does not implement the deferring port keeps the blocking
//     contract unchanged.
package cliprender

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// deferringRenderExecutor is a settle boundary that can be bounded: it records
// the budget the worker asked for and reports either a pending render or a
// certified outcome.
type deferringRenderExecutor struct {
	budgets []time.Duration
	pending bool
	outcome *RenderOutcome
}

func (f *deferringRenderExecutor) Submit(context.Context, ClipRenderPlanV1) error { return nil }

func (f *deferringRenderExecutor) Settle(context.Context, ClipRenderPlanV1) (*RenderOutcome, error) {
	return certifiedTestOutcome(f.outcome), nil
}

func (f *deferringRenderExecutor) SettleWithin(_ context.Context, _ ClipRenderPlanV1, _ string, budget time.Duration) (*RenderOutcome, error) {
	f.budgets = append(f.budgets, budget)
	if budget > 0 && f.pending {
		return nil, ErrRenderPending
	}
	return certifiedTestOutcome(f.outcome), nil
}

// blockingRenderExecutor implements ONLY the blocking port: the deferral must
// stay optional, so this is what a boundary that cannot be bounded looks like.
type blockingRenderExecutor struct {
	settleCalls int
	outcome     *RenderOutcome
}

func (f *blockingRenderExecutor) Submit(context.Context, ClipRenderPlanV1) error { return nil }

func (f *blockingRenderExecutor) Settle(context.Context, ClipRenderPlanV1) (*RenderOutcome, error) {
	f.settleCalls++
	return certifiedTestOutcome(f.outcome), nil
}

func (f *blockingRenderExecutor) SettleWithJobID(context.Context, ClipRenderPlanV1, string) (*RenderOutcome, error) {
	f.settleCalls++
	return certifiedTestOutcome(f.outcome), nil
}

// runSettle drives the settle half of a clip.render job for a job whose
// creation time the test controls (the deferral window is measured from it).
func runSettle(t *testing.T, renderer RenderExecutor, createdAt time.Time) (map[string]any, error) {
	t.Helper()
	return runSettleWith(t, renderer, createdAt, nil)
}

func runSettleWith(t *testing.T, renderer RenderExecutor, createdAt time.Time, configure func(*Worker)) (map[string]any, error) {
	t.Helper()
	ctx := context.Background()
	w, _, _ := newTestWorker(t)
	w.WithRenderExecutor(renderer)
	if configure != nil {
		configure(w)
	}

	_, enqueuer, err := handleSubmitted(t, ctx, w, "clip-settle-defer", baseRenderRequest())
	if err != nil {
		t.Fatalf("submit half: %v", err)
	}
	payload, err := encodeContinuationPayload(enqueuer.req.Continuation)
	if err != nil {
		t.Fatalf("encode continuation: %v", err)
	}
	return w.Handle(ctx, &job.Job{ID: "clip-settle-defer-settle", Payload: payload, CreatedAt: createdAt}, nil)
}

// TestSettleDefersInsteadOfHoldingTheLane is the point of the change: a render
// that outlives the attempt budget comes back as a DEFERRAL, with the budget as
// the re-dispatch delay, so the worker lane is released and no retry is spent.
func TestSettleDefersInsteadOfHoldingTheLane(t *testing.T) {
	renderer := &deferringRenderExecutor{pending: true, outcome: fullRenderOutcome()}

	_, err := runSettle(t, renderer, time.Now().UTC())
	if err == nil {
		t.Fatal("a still-running render must not complete the settle")
	}
	deferral, ok := job.AsDeferral(err)
	if !ok {
		t.Fatalf("err = %v, want a job.Deferral (a wait, not a failure)", err)
	}
	if deferral.Delay != DefaultSettleRetryDelay {
		t.Fatalf("deferral delay = %s, want the short retry delay %s (not the full attempt budget)", deferral.Delay, DefaultSettleRetryDelay)
	}
	if !errors.Is(err, job.ErrDeferred) {
		t.Fatalf("err = %v does not match job.ErrDeferred", err)
	}
	if len(renderer.budgets) != 1 || renderer.budgets[0] != DefaultSettleWait {
		t.Fatalf("budgets = %v, want exactly one bounded attempt of %s", renderer.budgets, DefaultSettleWait)
	}
}

// TestSettleDeferralDelayPolicy pins the retry delay independently of the
// attempt budget: the budget stays what bounds lane occupancy, while the
// re-dispatch delay is shorter, configurable, and never negative.
func TestSettleDeferralDelayPolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  time.Duration
		want time.Duration
	}{
		{name: "default is the short retry delay", set: DefaultSettleRetryDelay, want: DefaultSettleRetryDelay},
		{name: "operator can shorten it", set: 250 * time.Millisecond, want: 250 * time.Millisecond},
		{name: "never longer than the attempt budget", set: 2 * DefaultSettleWait, want: DefaultSettleWait},
		{name: "zero keeps the historical budget delay", set: 0, want: DefaultSettleWait},
	} {
		t.Run(tc.name, func(t *testing.T) {
			renderer := &deferringRenderExecutor{pending: true, outcome: fullRenderOutcome()}
			_, err := runSettleWith(t, renderer, time.Now().UTC(), func(w *Worker) {
				w.WithSettleRetry(tc.set)
			})
			deferral, ok := job.AsDeferral(err)
			if !ok {
				t.Fatalf("err = %v, want a job.Deferral", err)
			}
			if deferral.Delay != tc.want {
				t.Fatalf("deferral delay = %s, want %s", deferral.Delay, tc.want)
			}
			if len(renderer.budgets) != 1 || renderer.budgets[0] != DefaultSettleWait {
				t.Fatalf("attempt budgets = %v, want one %s attempt (the delay must not change the budget)", renderer.budgets, DefaultSettleWait)
			}
		})
	}
}

// pendingOnceExecutor returns a pending render on the FIRST bounded settle and
// a certified outcome on the next dispatch. It models the concrete CRA-08 case:
// the render finished just after the attempt budget, so the next cheap attempt
// collects it.
type pendingOnceExecutor struct {
	budgets []time.Duration
	calls   int
	outcome *RenderOutcome
}

func (f *pendingOnceExecutor) Submit(context.Context, ClipRenderPlanV1) error { return nil }

func (f *pendingOnceExecutor) Settle(context.Context, ClipRenderPlanV1) (*RenderOutcome, error) {
	return certifiedTestOutcome(f.outcome), nil
}

func (f *pendingOnceExecutor) SettleWithin(_ context.Context, _ ClipRenderPlanV1, _ string, budget time.Duration) (*RenderOutcome, error) {
	f.budgets = append(f.budgets, budget)
	f.calls++
	if f.calls == 1 {
		return nil, ErrRenderPending
	}
	return certifiedTestOutcome(f.outcome), nil
}

// TestSettleRenderEndingAt61sUsesTheShortRetry pins the ticket's 61-second
// case: a job that has already outlived one full 60 s budget still asks for a
// 60 s attempt, but the next dispatch is only the short retry away — not
// another full budget.
func TestSettleRenderEndingAt61sUsesTheShortRetry(t *testing.T) {
	renderer := &deferringRenderExecutor{pending: true, outcome: fullRenderOutcome()}
	_, err := runSettle(t, renderer, time.Now().UTC().Add(-61*time.Second))
	deferral, ok := job.AsDeferral(err)
	if !ok {
		t.Fatalf("err = %v, want a job.Deferral", err)
	}
	if len(renderer.budgets) != 1 || renderer.budgets[0] != DefaultSettleWait {
		t.Fatalf("budgets = %v, want one %s attempt", renderer.budgets, DefaultSettleWait)
	}
	if deferral.Delay != DefaultSettleRetryDelay {
		t.Fatalf("deferral delay = %s, want %s (not the full budget)", deferral.Delay, DefaultSettleRetryDelay)
	}
	if deferral.Delay >= DefaultSettleWait {
		t.Fatalf("deferral delay %s is not measurably shorter than the budget %s", deferral.Delay, DefaultSettleWait)
	}
}

// TestSettleCollectsOnTheNextShortAttempt drives the two dispatches the short
// delay enables: the first returns pending and counts one deferral, the second
// collects the certified artifact. It also pins the explicit deferral metrics.
func TestSettleCollectsOnTheNextShortAttempt(t *testing.T) {
	ctx := context.Background()
	w, _, _ := newTestWorker(t)
	renderer := &pendingOnceExecutor{outcome: fullRenderOutcome()}
	w.WithRenderExecutor(renderer).WithSettleWait(25 * time.Millisecond).WithSettleRetry(5 * time.Millisecond)

	_, enqueuer, err := handleSubmitted(t, ctx, w, "clip-settle-after-budget", baseRenderRequest())
	if err != nil {
		t.Fatalf("submit half: %v", err)
	}
	payload, err := encodeContinuationPayload(enqueuer.req.Continuation)
	if err != nil {
		t.Fatalf("encode continuation: %v", err)
	}
	settleJob := &job.Job{ID: "clip-settle-after-budget-settle", Payload: payload, CreatedAt: time.Now().UTC().Add(-61 * time.Second)}

	_, err = w.Handle(ctx, settleJob, nil)
	deferral, ok := job.AsDeferral(err)
	if !ok {
		t.Fatalf("first attempt err = %v, want a deferral", err)
	}
	if deferral.Delay != 5*time.Millisecond {
		t.Fatalf("first deferral delay = %s, want the configured short retry 5ms", deferral.Delay)
	}
	if count, added := w.SettleDeferralStats(); count != 1 || added != 5 {
		t.Fatalf("deferral metrics = (%d, %d ms), want (1, 5 ms)", count, added)
	}

	result, err := w.Handle(ctx, settleJob, nil)
	if err != nil {
		t.Fatalf("second attempt must collect the render: %v", err)
	}
	if result["phase"] != "rendered" {
		t.Fatalf("second attempt phase = %v, want rendered", result["phase"])
	}
	if renderer.calls != 2 {
		t.Fatalf("settle calls = %d, want 2 (budget then short retry)", renderer.calls)
	}
}

// TestSettleStopsAskingOnceTheWindowCloses: past the window the attempt asks for
// the blocking wait (budget 0) instead of deferring again, so a render that never
// reports terminal degrades to the historical behaviour rather than looping.
func TestSettleStopsAskingOnceTheWindowCloses(t *testing.T) {
	renderer := &deferringRenderExecutor{pending: true, outcome: fullRenderOutcome()}

	// 21 minutes: one minute past the 20-minute window, the ticket's second
	// simulated case. The settle must wait the render out, not defer again.
	if _, err := runSettle(t, renderer, time.Now().UTC().Add(-21*time.Minute)); err != nil {
		t.Fatalf("past the window the settle must wait the render out: %v", err)
	}
	if len(renderer.budgets) != 1 || renderer.budgets[0] != 0 {
		t.Fatalf("budgets = %v, want exactly one unlimited (0) wait", renderer.budgets)
	}
}

// TestSettleKeepsTheBlockingContractWithoutTheDeferringPort: the port is
// optional, so a boundary that cannot be bounded is still driven exactly as
// before.
func TestSettleKeepsTheBlockingContractWithoutTheDeferringPort(t *testing.T) {
	renderer := &blockingRenderExecutor{outcome: fullRenderOutcome()}

	if _, err := runSettle(t, renderer, time.Now().UTC()); err != nil {
		t.Fatalf("blocking settle: %v", err)
	}
	if renderer.settleCalls != 1 {
		t.Fatalf("settle calls = %d, want 1", renderer.settleCalls)
	}
}

// TestSettleWaitBudgetPolicy pins the policy itself: two independent off
// switches (wait <= 0 disables deferral; window <= 0 disables it too), the
// bounded window, and the no-age fallback.
func TestSettleWaitBudgetPolicy(t *testing.T) {
	now := time.Now().UTC()
	fresh := &job.Job{ID: "j", CreatedAt: now}
	old := &job.Job{ID: "j", CreatedAt: now.Add(-DefaultSettleWindow - time.Second)}

	withPolicy := func(wait, window time.Duration) *Worker {
		return &Worker{settleWait: wait, settleWindow: window, log: zap.NewNop()}
	}

	for _, tc := range []struct {
		name string
		w    *Worker
		j    *job.Job
		want time.Duration
	}{
		{name: "fresh job inside the window", w: withPolicy(30*time.Second, time.Minute), j: fresh, want: 30 * time.Second},
		{name: "job past the window waits it out", w: withPolicy(30*time.Second, time.Minute), j: old, want: 0},
		{name: "wait 0 disables deferral", w: withPolicy(0, time.Minute), j: fresh, want: 0},
		{name: "window 0 disables deferral", w: withPolicy(30*time.Second, 0), j: fresh, want: 0},
		{name: "no durable age holds the bounded wait", w: withPolicy(30*time.Second, time.Minute), j: &job.Job{ID: "j"}, want: 30 * time.Second},
		{name: "nil job holds the bounded wait", w: withPolicy(30*time.Second, time.Minute), j: nil, want: 30 * time.Second},
		{name: "nil worker waits it out", w: nil, j: fresh, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.w.settleWaitBudget(tc.j); got != tc.want {
				t.Fatalf("settleWaitBudget = %s, want %s", got, tc.want)
			}
		})
	}
}
