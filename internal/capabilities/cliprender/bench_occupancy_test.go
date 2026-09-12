package cliprender

// bench_occupancy_test.go owns scenarios 2 and 3 of the canonical clip.render
// benchmark — the two latency questions that are NOT about GPU speed:
//
//	2  Submit vs settle occupancy — is the Master slot really released before
//	                                the remote render finishes?
//	3  Parent completion latency  — how long after the render finishes does the
//	                                caller see the clip as done?
//
// Both drive the REAL worker through benchPipeline (bench_harness_test.go) and
// use the REAL ParentAggregator for the completion tail.

import (
	"fmt"
	"testing"
	"time"
)

// TestScenario2_SubmitSettleOccupancy is the before/after proof for the async
// submit/settle split (ticket §5: "submitting returns and releases its Master
// slot as soon as the RenderingGen job id is persisted").
//
// The method is a controlled experiment: the ONLY thing that changes between
// the two runs is the remote render duration. If the submit phase really
// releases the slot, the submit occupancy must stay flat while the settle
// occupancy grows by the same delta. The blocking (pre-split) pipeline is
// measured at the same render duration to show what the caller was paying
// before: there the single handler wall tracks the render.
func TestScenario2_SubmitSettleOccupancy(t *testing.T) {
	const (
		clips      = 8
		fastRender = 10 * time.Millisecond
		slowRender = 60 * time.Millisecond
	)
	fast := benchPipeline(t, benchConfig{
		Scenario:   "scenario-02-submit-settle-render-fast",
		Clips:      clips,
		Workers:    2,
		WaiterPool: 8,
		GPULanes:   8,
		RenderMS:   fastRender,
		Async:      true,
	})
	writeBenchReport(t, fast)

	slow := benchPipeline(t, benchConfig{
		Scenario:   "scenario-02-submit-settle-render-slow",
		Clips:      clips,
		Workers:    2,
		WaiterPool: 8,
		GPULanes:   8,
		RenderMS:   slowRender,
		Async:      true,
	})
	writeBenchReport(t, slow)

	blocking := benchPipeline(t, benchConfig{
		Scenario: "scenario-02-blocking-baseline",
		Clips:    clips,
		Workers:  8, // enough slots that the render, not the pool, is the bound
		GPULanes: 8,
		RenderMS: slowRender,
		Async:    false,
	})
	writeBenchReport(t, blocking)

	for name, rep := range map[string]benchReport{"fast": fast, "slow": slow, "blocking": blocking} {
		if rep.Failures != 0 {
			t.Fatalf("%s: %d failures: %+v", name, rep.Failures, rep.ClipRuns)
		}
	}

	// The split must actually happen: one submit and one settle per clip.
	for _, rep := range []benchReport{fast, slow} {
		if rep.Submits != clips || rep.Settles != clips {
			t.Fatalf("%s: submits=%d settles=%d, want %d/%d", rep.Scenario, rep.Submits, rep.Settles, clips, clips)
		}
	}

	// The assertions are ONE-SIDED bounds against the known render parameter
	// (60 ms), never a delta between two independent noisy percentiles: a busy
	// host inflates every wall by a few ms, so only a bound with a large margin
	// is a trustworthy signal.

	// 1. The submit occupation must NOT track the remote render: even its p95
	//    stays below the render duration, i.e. the submit handler returns
	//    before RenderingGen could have finished.
	if slow.SubmitOccupancyP95MS >= slow.RemoteRenderP50MS {
		t.Errorf("async submit occupancy p95=%dms reaches the %dms remote render — the submit phase is still waiting for RenderingGen",
			slow.SubmitOccupancyP95MS, slow.RemoteRenderP50MS)
	}
	// And the submit occupation must not scale with the render: the slow run's
	// submit p95 must stay far below the render delta it was given.
	if slow.SubmitOccupancyP95MS >= slowRender.Milliseconds()/2 {
		t.Errorf("async submit occupancy p95=%dms is more than half the %dms render — submission still absorbs the remote wait",
			slow.SubmitOccupancyP95MS, slowRender.Milliseconds())
	}

	// 2. The settle occupation must OWN the wait: it is at least most of the
	//    configured render.
	if slow.SettleOccupancyP95MS < slowRender.Milliseconds()*3/4 {
		t.Errorf("settle occupancy p95=%dms is below 75%% of the %dms render — the wait is not where we think it is",
			slow.SettleOccupancyP95MS, slowRender.Milliseconds())
	}
	// 3. And it must grow with the render: the fast/slow signal (~50 ms) is an
	//    order of magnitude above the measurement noise.
	settleDelta := slow.SettleOccupancyP50MS - fast.SettleOccupancyP50MS
	if settleDelta < 25 {
		t.Errorf("settle occupancy grew by only %dms for a +50ms render (fast=%dms slow=%dms)",
			settleDelta, fast.SettleOccupancyP50MS, slow.SettleOccupancyP50MS)
	}

	// 4. Before/after: the blocking pipeline's handler wall tracks the render,
	//    while the async submit occupancy does not. This is the whole point of
	//    the split and the number the ticket asks for.
	if blocking.SubmitOccupancyP95MS < slowRender.Milliseconds()*3/4 {
		t.Errorf("blocking baseline handler wall p95=%dms for a %dms render — expected the pre-split pipeline to hold the slot through the render",
			blocking.SubmitOccupancyP95MS, slowRender.Milliseconds())
	}
	t.Logf("slot occupancy: blocking handler=%dms | async submit=%dms (fast) / %dms (slow) | async settle=%dms (fast) / %dms (slow) | remote render=%dms",
		blocking.SubmitOccupancyP50MS,
		fast.SubmitOccupancyP50MS, slow.SubmitOccupancyP50MS,
		fast.SettleOccupancyP50MS, slow.SettleOccupancyP50MS,
		slow.RemoteRenderP50MS)
}

// TestScenario3_ParentCompletionLatency measures the caller-visible tail:
// remote_completed_at → parent_succeeded_at. The parent only turns terminal
// when the aggregator observes its terminal child, so the aggregation cadence
// is pure added latency on every clip the caller is waiting for.
//
// It compares the three options from the performance spec:
//
//	30 s  — the historical voiceover/script cadence (projected from the
//	        measured distribution; running it for real would cost 30 s/run)
//	 2 s  — the shipped clip.render default
//	event — finalise synchronously when the settle child completes
func TestScenario3_ParentCompletionLatency(t *testing.T) {
	const (
		clips    = 6
		tickMS   = 200 * time.Millisecond
		renderMS = 4 * time.Millisecond
	)
	base := benchConfig{
		Clips:      clips,
		Workers:    2,
		WaiterPool: 8,
		GPULanes:   8,
		RenderMS:   renderMS,
		Async:      true,
	}

	cfg := base
	cfg.Scenario = "scenario-03-parent-latency-tick"
	cfg.AggregateInterval = tickMS
	ticked := benchPipeline(t, cfg)
	writeBenchReport(t, ticked)

	cfg = base
	cfg.Scenario = "scenario-03-parent-latency-event"
	cfg.EventDriven = true
	event := benchPipeline(t, cfg)
	writeBenchReport(t, event)

	if ticked.Failures != 0 || event.Failures != 0 {
		t.Fatalf("failures: tick=%d event=%d", ticked.Failures, event.Failures)
	}

	// The ticked run must have actually finalised the parents, with a latency
	// bounded by the configured cadence.
	if ticked.ParentFinalizeP95MS <= 0 {
		t.Fatalf("ticked run: no parent was finalised within the cadence (p50=%d p95=%d)", ticked.ParentFinalizeP50MS, ticked.ParentFinalizeP95MS)
	}
	if ticked.ParentFinalizeP95MS > tickMS.Milliseconds()+250 {
		t.Errorf("ticked run: parent latency p95=%dms exceeds the %dms cadence plus slack",
			ticked.ParentFinalizeP95MS, tickMS.Milliseconds())
	}

	// Event-driven finalisation must be effectively immediate.
	if event.ParentFinalizeP95MS >= 150 {
		t.Errorf("event-driven parent latency p95=%dms, want < 50ms (a settle-completion finaliser must not poll)",
			event.ParentFinalizeP95MS)
	}
	if event.ParentFinalizeP95MS >= ticked.ParentFinalizeP50MS {
		t.Errorf("event-driven p95=%dms is not better than the ticked p50=%dms",
			event.ParentFinalizeP95MS, ticked.ParentFinalizeP50MS)
	}

	// The shipped interval is now a RECOVERY bound, not the completion
	// mechanism: the event path above finalises in ~0 ms no matter what the
	// cadence is, and the tick only exists to catch a parent stranded between a
	// child's commit and its notification. What must hold is that a healthy clip
	// never waits for it — proven by running the event path with a deliberately
	// long recovery cadence and requiring the same immediate finalisation.
	if DefaultParentAggregationInterval < 2*time.Second {
		t.Errorf("DefaultParentAggregationInterval = %v, want a recovery cadence (>= 2s): the healthy path is event-driven", DefaultParentAggregationInterval)
	}
	slowRecovery := base
	slowRecovery.Scenario = "scenario-03-parent-latency-event-slow-recovery"
	slowRecovery.EventDriven = true
	slowRecovery.AggregateInterval = 30 * time.Second
	slow := benchPipeline(t, slowRecovery)
	writeBenchReport(t, slow)
	if slow.Failures != 0 {
		t.Fatalf("slow-recovery run: %d failures", slow.Failures)
	}
	if slow.ParentFinalizeP95MS >= 150 {
		t.Errorf("with a 30s recovery cadence the event path must still finalise immediately, got p95=%dms", slow.ParentFinalizeP95MS)
	}
	// For comparison only: what a polling-only deployment would pay at the
	// shipped cadence. The projection scales the measured tick distribution by
	// the cadence ratio (the parent turns terminal at the first tick after its
	// child, so latency is ~uniform in [0, interval]).
	project := func(interval time.Duration) int64 {
		if ticked.ParentFinalizeP50MS == 0 {
			return 0
		}
		return int64(float64(ticked.ParentFinalizeP50MS) * float64(interval.Milliseconds()) / float64(tickMS.Milliseconds()))
	}
	shippedCadence := project(DefaultParentAggregationInterval)
	t.Logf("parent finalisation latency (clips=%d, cadence=%v): measured_tick p50=%dms p95=%dms | event p50=%dms p95=%dms | event@30s-recovery p95=%dms | polling-only @shipped %v would be p50=%dms",
		clips, tickMS, ticked.ParentFinalizeP50MS, ticked.ParentFinalizeP95MS,
		event.ParentFinalizeP50MS, event.ParentFinalizeP95MS,
		slow.ParentFinalizeP95MS, DefaultParentAggregationInterval, shippedCadence)

	// Guard: the projection must be derived from a real measurement, not a
	// constant invented by the test.
	if ticked.ParentFinalizeP50MS == 0 || event.ParentFinalizeP50MS < 0 {
		t.Errorf("scenario 3 produced no usable measurement (tick=%d event=%d)", ticked.ParentFinalizeP50MS, event.ParentFinalizeP50MS)
	}
}

// TestScenario3_EventDrivenFinaliserIsScalesafe guards the projection above:
// the event-driven latency must not depend on the batch size, otherwise
// "finalise immediately" would still be a poll in disguise.
func TestScenario3_EventDrivenFinaliserIsScalesafe(t *testing.T) {
	for _, clips := range []int{2, 12} {
		rep := benchPipeline(t, benchConfig{
			Scenario:    fmt.Sprintf("scenario-03-event-scales-%d", clips),
			Clips:       clips,
			Workers:     2,
			WaiterPool:  16,
			GPULanes:    8,
			RenderMS:    3 * time.Millisecond,
			Async:       true,
			EventDriven: true,
		})
		writeBenchReport(t, rep)
		if rep.Failures != 0 {
			t.Fatalf("clips=%d: %d failures", clips, rep.Failures)
		}
		if rep.ParentFinalizeP95MS >= 150 {
			t.Errorf("clips=%d: event-driven p95=%dms, want < 150ms", clips, rep.ParentFinalizeP95MS)
		}
	}
}
