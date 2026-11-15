package scriptgeneration

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func TestApplyCallerImageSearchesReportsLateQuery(t *testing.T) {
	plan := &SemanticProductionPlan{Units: []SemanticProductionUnit{{
		SceneID: "scene-1", RequiresMedia: true, Status: ProductionUnitPlanned,
		SearchStatus: ProductionSearchSatisfied,
	}}}
	added := ApplyCallerImageSearches(plan, []mediadomain.SegmentMediaSearch{{
		SegmentID: "scene-1", Slot: mediadomain.SlotSecondaryImage, Query: "courthouse exterior",
	}}, nil)
	if added != 0 {
		t.Fatalf("late query was applied to a settled unit: added=%d", added)
	}
	if plan.Metrics.SearchesTooLate != 1 {
		t.Fatalf("late query metric = %d, want 1", plan.Metrics.SearchesTooLate)
	}
}
