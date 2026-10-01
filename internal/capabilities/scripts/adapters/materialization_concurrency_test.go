package adapters

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestMaterializationWorkersClampsPlanValue(t *testing.T) {
	cases := []struct {
		requested int
		want      int
	}{
		{requested: 1, want: 1},
		{requested: 4, want: 4},
		{requested: MaxMaterializationWorkers, want: MaxMaterializationWorkers},
		{requested: MaxMaterializationWorkers + 100, want: MaxMaterializationWorkers},
	}
	for _, tc := range cases {
		plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: media.MediaPlanSpec{
			Materialization: media.MediaMaterializationPolicy{Workers: tc.requested},
		}}
		if got := materializationWorkers(plan); got != tc.want {
			t.Fatalf("materializationWorkers(workers=%d) = %d, want %d", tc.requested, got, tc.want)
		}
	}
	if got := clampMaterializationWorkers(-3); got != MinMaterializationWorkers {
		t.Fatalf("clampMaterializationWorkers(-3) = %d, want %d", got, MinMaterializationWorkers)
	}
}

func TestMaterializationWorkersDefaultIsBounded(t *testing.T) {
	for _, plan := range []*scriptpkg.ResolvedGenerationPlan{nil, {}} {
		got := materializationWorkers(plan)
		if got < 2 || got > DefaultMaterializationWorkers {
			t.Fatalf("default materializationWorkers = %d, want within [2, %d]", got, DefaultMaterializationWorkers)
		}
	}
}
