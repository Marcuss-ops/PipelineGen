package jobs

import (
	"context"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestSpeculationScheduler_AdmitsNPlus1ToNPlus3WithLimits(t *testing.T) {
	cfg := DefaultSpeculationConfig()
	cfg.MaxUnits = 3
	cfg.MaxByCost = map[SpeculationCost]int{CostWarm: 1, CostPrepare: 1, CostSpeculate: 1}
	gate := ActiveWorkFunc(func() bool { return true })
	scheduler := NewSpeculationScheduler(cfg, gate)
	candidates := []SpeculationCandidate{
		{Job: &job.Job{ID: "n4"}, Depth: 4, Unit: PreparationUnit{ID: "too-far", CostClass: string(CostWarm), ResourceClass: string(ResourceNetwork)}},
		{Job: &job.Job{ID: "n3"}, Depth: DepthNPlus3, Unit: PreparationUnit{ID: "spec", CostClass: string(CostSpeculate), ResourceClass: string(ResourceLLM), Priority: 1}, EstimatedTimeSavedMS: 100, EstimatedCostMS: 10},
		{Job: &job.Job{ID: "n1"}, Depth: DepthNPlus1, Unit: PreparationUnit{ID: "warm", CostClass: string(CostWarm), ResourceClass: string(ResourceNetwork), Priority: 1}, EstimatedTimeSavedMS: 10, EstimatedCostMS: 1},
		{Job: &job.Job{ID: "n2"}, Depth: DepthNPlus2, Unit: PreparationUnit{ID: "prepare", CostClass: string(CostPrepare), ResourceClass: string(ResourceCPU), Priority: 1}, EstimatedTimeSavedMS: 50, EstimatedCostMS: 5},
	}
	got := scheduler.Admit(candidates)
	if len(got) != 3 {
		t.Fatalf("admitted %d candidates, want 3", len(got))
	}
	for _, candidate := range got {
		if candidate.Depth > DepthNPlus3 {
			t.Fatalf("admitted depth %d", candidate.Depth)
		}
	}
}

func TestSpeculationScheduler_ActiveWorkPreemptsSpeculation(t *testing.T) {
	active := false
	scheduler := NewSpeculationScheduler(DefaultSpeculationConfig(), ActiveWorkFunc(func() bool { return active }))
	candidate := SpeculationCandidate{Job: &job.Job{ID: "n1"}, Depth: DepthNPlus1, Unit: PreparationUnit{ID: "warm", CostClass: string(CostWarm), ResourceClass: string(ResourceNetwork)}}
	if got := scheduler.Admit([]SpeculationCandidate{candidate}); len(got) != 0 {
		t.Fatalf("admitted work while active execution unavailable: %#v", got)
	}
	active = true
	run := 0
	if err := scheduler.Run(context.Background(), []SpeculationCandidate{candidate}, func(context.Context, SpeculationCandidate) error { run++; return nil }); err != nil {
		t.Fatal(err)
	}
	if run != 1 {
		t.Fatalf("executed %d candidates, want 1", run)
	}
}

func TestSpeculationScheduler_ResourceAndCostLimits(t *testing.T) {
	cfg := DefaultSpeculationConfig()
	cfg.MaxUnits = 10
	cfg.MaxByCost = map[SpeculationCost]int{CostWarm: 1}
	cfg.MaxByResource = map[SpeculationResource]int{ResourceNetwork: 1}
	scheduler := NewSpeculationScheduler(cfg, ActiveWorkFunc(func() bool { return true }))
	candidates := []SpeculationCandidate{
		{Job: &job.Job{ID: "a"}, Depth: DepthNPlus1, Unit: PreparationUnit{ID: "a", CostClass: string(CostWarm), ResourceClass: string(ResourceNetwork)}},
		{Job: &job.Job{ID: "b"}, Depth: DepthNPlus1, Unit: PreparationUnit{ID: "b", CostClass: string(CostWarm), ResourceClass: string(ResourceNetwork)}},
	}
	if got := scheduler.Admit(candidates); len(got) != 1 {
		t.Fatalf("admitted %d candidates, want 1", len(got))
	}
}
