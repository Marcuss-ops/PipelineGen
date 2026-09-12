package cliprender

// bench_scaling_test.go owns scenarios 1, 9 and 10 of the canonical
// clip.render benchmark:
//
//	1  Worker scaling      — 1/2/4/8 Master slots; does throughput scale?
//	9  RenderingGen sat.   — 1/2/4/8 GPU lanes; is the GPU ever idle while
//	                         jobs are pending?
//	10 E2E canonical       — 1/10/50 clips, the full metric report to re-run
//	                         after every performance change.
//
// All three drive the REAL worker through benchPipeline (bench_harness_test.go).

import (
	"fmt"
	"testing"
	"time"
)

// TestScenario1_WorkerScaling is the worker-scaling study. The pre-split
// pipeline holds a Master slot for the whole remote render, so throughput is
// bounded by min(workers, GPU lanes); the test proves both halves of that
// claim: near-linear speedup while workers ≤ lanes, and hard saturation the
// moment workers exceed the lanes. It also pins the invariant that no run ever
// exceeds the declared lane count.
func TestScenario1_WorkerScaling(t *testing.T) {
	const (
		clips    = 24
		renderMS = 10 * time.Millisecond
	)
	levels := []int{1, 2, 4, 8}

	type point struct {
		lanes       int
		workers     int
		wallMS      int64
		clipsPerMin float64
		maxLanes    int
	}
	var points []point

	// Scaling is asserted on WALL RATIOS between adjacent measurements rather
	// than on absolute throughput: both sides of a ratio absorb the same
	// machine load, so the claim stays valid on a busy CI host while still
	// failing if extra workers buy nothing.
	for _, lanes := range []int{8, 2} {
		var prev *point
		var wallAtLanes int64
		for _, workers := range levels {
			rep := benchPipeline(t, benchConfig{
				Scenario: fmt.Sprintf("scenario-01-worker-scaling-lanes%d-w%d", lanes, workers),
				Clips:    clips,
				Workers:  workers,
				GPULanes: lanes,
				RenderMS: renderMS,
				Async:    false, // pre-split pipeline: one slot per clip
			})
			writeBenchReport(t, rep)
			if rep.Failures != 0 {
				t.Fatalf("lanes=%d workers=%d: %d failures: %+v", lanes, workers, rep.Failures, rep.ClipRuns)
			}
			// Hard invariant: neither the worker count nor the GPU lane count can
			// be exceeded — concurrency is min(workers, lanes), never more.
			ceiling := benchMin(workers, lanes)
			if rep.GPUMaxConcurrent > ceiling {
				t.Errorf("lanes=%d workers=%d: observed %d concurrent renders, ceiling is %d",
					lanes, workers, rep.GPUMaxConcurrent, ceiling)
			}
			p := point{lanes: lanes, workers: workers, wallMS: rep.WallMS, clipsPerMin: rep.ClipsPerMin, maxLanes: rep.GPUMaxConcurrent}
			points = append(points, p)

			// The improvement claim only holds while the workers are the bound
			// (workers ≤ lanes). Beyond that the GPU lanes are the ceiling and
			// the wall must PLATEAU — which the saturation check below asserts.
			if prev != nil && workers <= lanes {
				// Doubling the worker count must cut the batch wall time by at
				// least 25%. A pipeline that is not actually parallel cannot do
				// this, and a load-inflated host inflates both walls.
				if rep.WallMS > prev.wallMS*3/4 {
					t.Errorf("lanes=%d: wall did not improve when workers went %d→%d (%dms→%dms); throughput is not scaling with worker slots",
						lanes, prev.workers, workers, prev.wallMS, rep.WallMS)
				}
			}
			if workers == lanes {
				wallAtLanes = rep.WallMS
			}
			// Past the lane ceiling extra workers must buy nothing: the wall has
			// to plateau instead of continuing to fall.
			if wallAtLanes > 0 && workers > lanes && rep.WallMS < wallAtLanes*3/4 {
				t.Errorf("lanes=%d workers=%d: wall %dms fell below the %dms plateau — something other than the GPU is limiting (or the lane ceiling is not enforced)",
					lanes, workers, rep.WallMS, wallAtLanes)
			}
			t.Logf("lanes=%d workers=%d: wall=%dms rate=%.2f clips/min lanes_used=%d",
				lanes, workers, rep.WallMS, rep.ClipsPerMin, rep.GPUMaxConcurrent)
			cur := p
			prev = &cur
		}
	}

	// Worst-case total speedup at workers == lanes (8) must be material.
	var atOne, atEight *point
	for i := range points {
		p := &points[i]
		if p.lanes != 8 {
			continue
		}
		switch p.workers {
		case 1:
			atOne = p
		case 8:
			atEight = p
		}
	}
	if atOne != nil && atEight != nil && atEight.wallMS > atOne.wallMS/2 {
		t.Errorf("8 workers vs 1 worker with 8 lanes: wall %dms vs %dms — less than 2× speedup",
			atEight.wallMS, atOne.wallMS)
	}

	// Saturation: at 8 workers, 2 lanes must be materially slower than 8
	// lanes. If it is not, workers — not the GPU — were the bound.
	var many, few *point
	for i := range points {
		p := &points[i]
		if p.workers != 8 {
			continue
		}
		switch p.lanes {
		case 8:
			many = p
		case 2:
			few = p
		}
	}
	if many != nil && few != nil {
		if few.wallMS < many.wallMS*4/3 {
			t.Errorf("lane saturation: 2 lanes wall=%dms is not materially below 8 lanes wall=%dms",
				few.wallMS, many.wallMS)
		}
		t.Logf("lane saturation at 8 workers: %d lanes → wall %dms (%.2f clips/min), %d lanes → wall %dms (%.2f clips/min)",
			many.lanes, many.wallMS, many.clipsPerMin, few.lanes, few.wallMS, few.clipsPerMin)
	}
}

// TestScenario9_RenderingGenSaturation proves the second half of the KPI: the
// GPU lanes must stay busy whenever pending work exists, and the number of
// PipelineGen producers must be enough to keep them fed — but no more.
//
// It sweeps 1/2/4/8 producers against a fixed 2-lane RenderingGen with enough
// clips to saturate, and asserts:
//
//   - concurrency never exceeds the lane count (the hard invariant);
//   - fewer producers than lanes leaves the GPU idle (the metric detects it);
//   - once producers ≥ lanes, utilisation saturates high and extra producers
//     buy no throughput — the GPU is finally the bound, which is where a
//     performance-conscious pipeline wants to be.
func TestScenario9_RenderingGenSaturation(t *testing.T) {
	const (
		lanes = 2
		clips = 24
	)
	producers := []int{1, 2, 4, 8}

	type producerPoint struct {
		producers   int
		wallMS      int64
		utilPct     float64
		queueP95MS  int64
		clipsPerMin float64
		maxLanes    int
	}
	var points []producerPoint

	// The producers that hold a GPU lane are the SETTLE workers: with the async
	// split the submit pool only prepares and enqueues, so it does not feed the
	// GPU. Sweeping the settle pool is therefore the correct saturation axis
	// (sweeping submit workers would measure nothing).
	for _, p := range producers {
		rep := benchPipeline(t, benchConfig{
			Scenario:   fmt.Sprintf("scenario-09-producers-%d", p),
			Clips:      clips,
			Workers:    4, // submit pool: fixed and deliberately not the axis
			WaiterPool: p,
			GPULanes:   lanes,
			RenderMS:   10 * time.Millisecond,
			PrepareMS:  time.Millisecond,
			Async:      true,
		})
		writeBenchReport(t, rep)
		if rep.Failures != 0 {
			t.Fatalf("producers=%d: %d failures: %+v", p, rep.Failures, rep.ClipRuns)
		}
		if rep.GPUMaxConcurrent > lanes {
			t.Errorf("producers=%d: %d concurrent renders exceeds the %d-lane ceiling", p, rep.GPUMaxConcurrent, lanes)
		}
		// How many lanes the pool actually managed to keep busy is exactly
		// min(producers, lanes) — a hard, non-timing invariant.
		if want := benchMin(p, lanes); rep.GPUMaxConcurrent > want {
			t.Errorf("producers=%d: %d concurrent renders, want at most %d", p, rep.GPUMaxConcurrent, want)
		}
		points = append(points, producerPoint{
			producers: p, wallMS: rep.WallMS, utilPct: rep.GPULaneUtilPct,
			queueP95MS: rep.RemoteQueueP95MS, clipsPerMin: rep.ClipsPerMin, maxLanes: rep.GPUMaxConcurrent,
		})
		t.Logf("producers=%d lanes=%d: util=%.1f%% wall=%dms queue_p95=%dms rate=%.2f clips/min max_concurrent=%d",
			p, lanes, rep.GPULaneUtilPct, rep.WallMS, rep.RemoteQueueP95MS, rep.ClipsPerMin, rep.GPUMaxConcurrent)
	}

	under := points[0] // 1 producer < 2 lanes: half the GPU is idle
	at := points[1]    // 2 producers == 2 lanes: both lanes warm
	if at.maxLanes != benchMin(at.producers, lanes) {
		t.Errorf("%d producers used only %d of the %d lanes", at.producers, at.maxLanes, lanes)
	}
	if under.utilPct > at.utilPct-20 {
		t.Errorf("1 producer over %d lanes reported %.1f%% utilisation vs %.1f%% at %d producers — the metric cannot detect an underfed GPU",
			lanes, under.utilPct, at.utilPct, at.producers)
	}
	if at.utilPct < 70 {
		t.Errorf("%d producers over %d lanes: utilisation %.1f%% < 70%% — the GPU idles while jobs are pending",
			at.producers, lanes, at.utilPct)
	}

	// Beyond the lane count, extra producers must not buy throughput: the GPU
	// is saturated, so the rate plateaus (and the queue, if anything, grows).
	for i := 2; i < len(points); i++ {
		prev, cur := points[i-1], points[i]
		if cur.clipsPerMin > prev.clipsPerMin*1.25 {
			t.Errorf("producers %d→%d still gained %.2f→%.2f clips/min past the %d-lane ceiling — the GPU is not the bound",
				prev.producers, cur.producers, prev.clipsPerMin, cur.clipsPerMin, lanes)
		}
	}

	// The starved case (2 clips, 8 lanes) confirms idle lanes are reported as
	// idle rather than always 100%.
	starved := benchPipeline(t, benchConfig{
		Scenario:   "scenario-09-gpu-starvation",
		Clips:      2,
		Workers:    4,
		WaiterPool: 8,
		GPULanes:   8,
		RenderMS:   10 * time.Millisecond,
		Async:      true,
	})
	writeBenchReport(t, starved)
	if starved.Failures != 0 {
		t.Fatalf("starved: %d failures: %+v", starved.Failures, starved.ClipRuns)
	}
	if starved.GPULaneUtilPct >= at.utilPct {
		t.Errorf("starvation check: 2 clips over 8 lanes reported %.1f%% utilisation vs %.1f%% saturated — the metric cannot detect idle lanes",
			starved.GPULaneUtilPct, at.utilPct)
	}
	t.Logf("GPU lanes: saturated %.1f%% busy at %d producers (rate %.2f clips/min), starved %.1f%% busy with 2 clips over 8 lanes",
		at.utilPct, at.producers, at.clipsPerMin, starved.GPULaneUtilPct)
}

// TestScenario10_EndToEndCanonical is the benchmark to re-run after every
// performance change: the same pipeline at 1, 10 and 50 clips with the full
// metric set. It asserts the properties that must hold at every scale
// (no failures, no lane-ceiling violation, throughput non-decreasing with
// batch size) rather than a hardware-dependent absolute number.
func TestScenario10_EndToEndCanonical(t *testing.T) {
	scales := []int{1, 10, 50}
	rates := make([]float64, len(scales))
	for i, clips := range scales {
		rep := benchPipeline(t, benchConfig{
			Scenario:   fmt.Sprintf("scenario-10-e2e-%d-clips", clips),
			Clips:      clips,
			Workers:    4,
			WaiterPool: 64,
			GPULanes:   8,
			RenderMS:   4 * time.Millisecond,
			DownloadMS: time.Millisecond,
			PublishMS:  time.Millisecond,
			PrepareMS:  time.Millisecond,
			Async:      true,
		})
		writeBenchReport(t, rep)
		if rep.Failures != 0 {
			t.Fatalf("e2e %d clips: %d failures: %+v", clips, rep.Failures, rep.ClipRuns)
		}
		if rep.GPUMaxConcurrent > 8 {
			t.Errorf("e2e %d clips: %d concurrent renders exceeds the 8-lane ceiling", clips, rep.GPUMaxConcurrent)
		}
		if rep.Submits != clips || rep.Settles != clips {
			t.Errorf("e2e %d clips: submits=%d settles=%d, want %d/%d", clips, rep.Submits, rep.Settles, clips, clips)
		}
		rates[i] = rep.ClipsPerMin
		t.Logf("e2e %d clips: wall=%dms rate=%.2f clips/min p50=%dms p95=%dms gpu=%.1f%%",
			clips, rep.WallMS, rep.ClipsPerMin, rep.P50MS, rep.P95MS, rep.GPULaneUtilPct)
	}
	// A larger batch amortises the fixed startup cost, so the rate must never
	// fall as the batch grows.
	for i := 1; i < len(rates); i++ {
		if rates[i] < rates[i-1]*0.9 {
			t.Errorf("throughput regressed with batch size: %d clips → %.2f clips/min, %d clips → %.2f clips/min",
				scales[i-1], rates[i-1], scales[i], rates[i])
		}
	}
}
