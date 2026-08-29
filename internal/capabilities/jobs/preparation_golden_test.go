package jobs

import (
	"context"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestPreparationGolden_ThreeIdenticalJobs verifies the core ahead-of-queue
// contract without external providers: the first job performs each unit once;
// jobs two and three see the same fingerprints as READY and adopt them,
// reducing their simulated critical path to zero.
func TestPreparationGolden_ThreeIdenticalJobs(t *testing.T) {
	ctx := context.Background()
	planner := NewScriptPreparationPlanner()
	store := newGoldenPreparationStore()
	const unitWorkMS int64 = 100

	var totalWork int64
	var totalSaved int64
	for index := 1; index <= 3; index++ {
		j := &job.Job{ID: "golden-job-" + string(rune('0'+index)), Type: job.TypeScriptGenerate, Payload: []byte(`{"title":"identical"}`)}
		plan, err := planner.Plan(ctx, j)
		if err != nil {
			t.Fatalf("job %d plan: %v", index, err)
		}
		ready := 0
		for _, unit := range plan.Units {
			prepared, owned, err := store.acquire(unit)
			if err != nil {
				t.Fatalf("job %d acquire %s: %v", index, unit.ID, err)
			}
			if prepared {
				ready++
				totalSaved += unitWorkMS
				continue
			}
			if !owned {
				t.Fatalf("job %d unit %s neither ready nor owned", index, unit.ID)
			}
			totalWork += unitWorkMS
			store.markReady(unit.Fingerprint)
		}
		if index == 1 && ready != 0 {
			t.Fatalf("first job adopted %d units", ready)
		}
		if index > 1 && ready != len(plan.Units) {
			t.Fatalf("job %d adopted %d/%d units", index, ready, len(plan.Units))
		}
	}
	if totalWork != int64(len(mustGoldenPlan(t, planner).Units))*unitWorkMS {
		t.Fatalf("simulated work=%d, want one execution of each unit", totalWork)
	}
	if totalSaved != int64(len(mustGoldenPlan(t, planner).Units))*unitWorkMS*2 {
		t.Fatalf("saved critical path=%d, want both subsequent jobs saved", totalSaved)
	}
}

type goldenPreparationStore struct{ ready map[string]bool }

func newGoldenPreparationStore() *goldenPreparationStore {
	return &goldenPreparationStore{ready: make(map[string]bool)}
}
func (s *goldenPreparationStore) acquire(unit PreparationUnit) (bool, bool, error) {
	if s.ready[unit.Fingerprint] {
		return true, false, nil
	}
	return false, true, nil
}
func (s *goldenPreparationStore) markReady(fingerprint string) { s.ready[fingerprint] = true }

func mustGoldenPlan(t *testing.T, planner *ScriptPreparationPlanner) PreparationPlan {
	t.Helper()
	plan, err := planner.Plan(context.Background(), &job.Job{ID: "golden-reference", Type: job.TypeScriptGenerate, Payload: []byte(`{"title":"identical"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
