package scheduling

import (
	"context"
	"sync"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func candidate(id string, resource string, cost string, depth SpeculationDepth) SpeculationCandidate {
	return SpeculationCandidate{
		Job:   &job.Job{ID: "job-" + id},
		Depth: depth,
		Unit: PreparationUnit{
			ID:            id,
			ResourceClass: resource,
			CostClass:     cost,
			Priority:      1,
		},
	}
}

// TestResourceScheduler_PerClassPoolsAreSeparate verifies the scheduler keeps
// SEPARATE pools per resource class: exhausting the LLM pool does not block
// network units and vice-versa.
func TestResourceScheduler_PerClassPoolsAreSeparate(t *testing.T) {
	cfg := DefaultSpeculationConfig()
	cfg.MaxUnits = 10
	cfg.ResourceLimits = map[SpeculationResource]ResourceBudget{
		ResourceLLM:     {Capacity: 2, ActiveReserved: 1, SpeculativeMax: 1},
		ResourceNetwork: {Capacity: 4, ActiveReserved: 1, SpeculativeMax: 3},
	}
	cfg.MaxByCost = map[SpeculationCost]int{CostWarm: 10, CostPrepare: 10, CostSpeculate: 10}
	scheduler := NewResourceScheduler(cfg, ActiveWorkFunc(func() bool { return true }))

	candidates := []SpeculationCandidate{
		candidate("llm-1", string(job.ResourceLLM), string(CostPrepare), DepthNPlus1),
		candidate("llm-2", string(job.ResourceLLM), string(CostPrepare), DepthNPlus2),
		candidate("net-1", string(job.ResourceNetwork), string(CostWarm), DepthNPlus1),
		candidate("net-2", string(job.ResourceNetwork), string(CostWarm), DepthNPlus2),
		candidate("net-3", string(job.ResourceNetwork), string(CostWarm), DepthNPlus3),
	}
	admitted := scheduler.Admit(candidates)

	llm, net := 0, 0
	for _, c := range admitted {
		switch resourceClassOf(c.Unit) {
		case ResourceLLM:
			llm++
		case ResourceNetwork:
			net++
		}
	}
	if llm != 1 {
		t.Errorf("LLM pool admitted %d, want 1 (SpeculativeMax=1)", llm)
	}
	if net != 3 {
		t.Errorf("network pool admitted %d, want 3 (SpeculativeMax=3)", net)
	}
}

// TestResourceScheduler_ActiveWorkAbsolutePriority verifies active work has
// ABSOLUTE priority: when the gate reports active work, no speculative unit
// is admitted and Run stops immediately; when active work clears, speculation
// resumes.
func TestResourceScheduler_ActiveWorkAbsolutePriority(t *testing.T) {
	active := false
	scheduler := NewResourceScheduler(DefaultSpeculationConfig(), ActiveWorkFunc(func() bool { return active }))
	cands := []SpeculationCandidate{
		candidate("warm", string(job.ResourceNetwork), string(CostWarm), DepthNPlus1),
		candidate("spec", string(job.ResourceLLM), string(CostSpeculate), DepthNPlus1),
	}

	// 1. Active work present → nothing admitted (absolute priority).
	if got := scheduler.Admit(cands); len(got) != 0 {
		t.Fatalf("admitted %d candidates while active work present, want 0", len(got))
	}

	// 2. Active work clears → speculation admitted and executed.
	active = true
	executed := 0
	if err := scheduler.Run(context.Background(), cands, func(context.Context, SpeculationCandidate) error {
		executed++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if executed != 2 {
		t.Fatalf("executed %d, want 2 once active work cleared", executed)
	}

	// 3. Active work arrives MID-RUN → remaining speculation preempted.
	// The gate starts TRUE (no active work → speculation allowed) and flips
	// to FALSE while the first unit is in-flight.
	var mu sync.Mutex
	activeMid := true
	scheduler2 := NewResourceScheduler(DefaultSpeculationConfig(), ActiveWorkFunc(func() bool {
		mu.Lock()
		defer mu.Unlock()
		return activeMid
	}))
	resume := make(chan struct{})
	started := make(chan struct{})
	executedMid := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = scheduler2.Run(context.Background(), []SpeculationCandidate{
			candidate("a", string(job.ResourceNetwork), string(CostWarm), DepthNPlus1),
			candidate("b", string(job.ResourceNetwork), string(CostWarm), DepthNPlus1),
		}, func(ctx context.Context, c SpeculationCandidate) error {
			mu.Lock()
			executedMid++
			first := executedMid == 1
			mu.Unlock()
			if first {
				close(started) // signal: first unit is in-flight
				<-resume       // hold the first unit until the gate flips
			}
			return nil
		})
	}()

	// Wait until the first unit is in-flight, then flip the gate.
	<-started
	mu.Lock()
	activeMid = false // active work arrives while a speculative unit is in-flight
	mu.Unlock()
	close(resume)
	<-done
	// After the in-flight unit completes, the second should NOT run.
	mu.Lock()
	defer mu.Unlock()
	if executedMid != 1 {
		t.Fatalf("executed %d units after active work arrived, want 1 (preempted)", executedMid)
	}
}

// TestResourceScheduler_BudgetReservesActiveShare verifies the budget's
// active-reserved share is not consumed by speculation: with Capacity=2 and
// ActiveReserved=1, at most 1 speculative unit is admitted per class, so the
// active slot always stays free.
func TestResourceScheduler_BudgetReservesActiveShare(t *testing.T) {
	cfg := DefaultSpeculationConfig()
	cfg.MaxUnits = 10
	cfg.ResourceLimits = map[SpeculationResource]ResourceBudget{
		ResourceTTS: {Capacity: 2, ActiveReserved: 1, SpeculativeMax: 0}, // SpeculativeMax=0 → derive Capacity-ActiveReserved=1
	}
	cfg.MaxByCost = map[SpeculationCost]int{CostWarm: 10}
	scheduler := NewResourceScheduler(cfg, ActiveWorkFunc(func() bool { return true }))

	admitted := scheduler.Admit([]SpeculationCandidate{
		candidate("tts-1", string(job.ResourceTTS), string(CostWarm), DepthNPlus1),
		candidate("tts-2", string(job.ResourceTTS), string(CostWarm), DepthNPlus2),
		candidate("tts-3", string(job.ResourceTTS), string(CostWarm), DepthNPlus3),
	})
	if len(admitted) != 1 {
		t.Fatalf("admitted %d TTS units, want 1 (capacity 2 - active reserved 1)", len(admitted))
	}
}

// TestResourceScheduler_RunningTracksLivePool verifies the live per-class
// concurrency projection is accurate while units execute.
func TestResourceScheduler_RunningTracksLivePool(t *testing.T) {
	cfg := DefaultSpeculationConfig()
	cfg.MaxUnits = 10
	cfg.MaxByResource = map[SpeculationResource]int{ResourceNetwork: 2}
	cfg.ResourceLimits = map[SpeculationResource]ResourceBudget{
		ResourceNetwork: {Capacity: 4, ActiveReserved: 1, SpeculativeMax: 2},
	}
	scheduler := NewResourceScheduler(cfg, ActiveWorkFunc(func() bool { return true }))

	started := make(chan struct{})
	var startedOnce sync.Once
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = scheduler.Run(context.Background(), []SpeculationCandidate{
			candidate("n1", string(job.ResourceNetwork), string(CostWarm), DepthNPlus1),
			candidate("n2", string(job.ResourceNetwork), string(CostWarm), DepthNPlus1),
		}, func(ctx context.Context, c SpeculationCandidate) error {
			startedOnce.Do(func() { close(started) }) // first unit in-flight
			<-release
			return nil
		})
	}()

	<-started
	// While the first unit executes, the live pool must show it.
	running := scheduler.Running()
	if running[ResourceNetwork] != 1 {
		t.Fatalf("live network pool = %d, want 1 while in-flight", running[ResourceNetwork])
	}
	close(release)
	<-done
	running = scheduler.Running()
	if running[ResourceNetwork] != 0 {
		t.Fatalf("live network pool after completion = %d, want 0", running[ResourceNetwork])
	}
}
