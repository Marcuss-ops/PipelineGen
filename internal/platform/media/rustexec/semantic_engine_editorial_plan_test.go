package rustexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func TestCompileEditorialPlanAppliesBatchEdits(t *testing.T) {
	response := `{"ok":true,"plan":{
		"pre_budget_edits":[
			{"index":0,"id":"map","keep":true,"start_us":0,"duration_us":1500000,"start_ms":0,"end_ms":1500},
			{"index":1,"id":"image","keep":true,"start_us":1600000,"duration_us":500000,"start_ms":1600,"end_ms":2100}],
		"timing_edits":[
			{"index":0,"id":"map","keep":true,"start_us":0,"duration_us":2000000,"start_ms":0,"end_ms":2000},
			{"index":1,"id":"image","keep":true,"start_us":2100000,"duration_us":500000,"start_ms":2100,"end_ms":2600}],
		"ranked_entities":[[0]],
		"entrance_edits":[{"index":0,"id":"image","enter_frames":12}]
	}}`
	runner := &semanticRunner{output: []byte(response)}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	items := []capmodel.OverlayItem{
		{ID: "map", SceneID: "scene-1", Kind: "map", StartMs: 0, EndMs: 2000, StartUS: 0, DurationUS: 2_000_000, Map: &capmodel.MapOverlay{}},
		{ID: "image", SceneID: "scene-1", Kind: "entity_image", StartMs: 1600, EndMs: 2100, StartUS: 1_600_000, DurationUS: 500_000},
	}
	entranceItems := []capmodel.OverlayItem{
		{ID: "image", SceneID: "scene-1", Kind: "entity_image", StartMs: 100, EndMs: 3000,
			MotionParams: map[string]any{"enter_frames": 8, "opaque": "keep"}},
		{ID: "phrase", SceneID: "scene-1", Kind: "text_phrase", StartMs: 0, EndMs: 3000,
			MotionParams: map[string]any{"enter_frames": 8}},
	}
	scenes := []capabilityentities.EntityOverlayRankingScene{{Entities: []capabilityentities.EntityOverlayRankingOccurrence{
		{EntityID: "person-tim", OverlayID: "overlay-s1-tim", EntityType: "PERSON", Confidence: 0.98},
	}}}

	got, err := adapter.CompileEditorialPlan(context.Background(), items, entranceItems, scenes,
		map[string][2]int64{"scene-1": {0, 5_000_000}}, 24, 1)
	if err != nil {
		t.Fatalf("compile editorial plan: %v", err)
	}
	if len(got.PreBudgetItems) != 2 || got.PreBudgetItems[0].DurationUS != 1_500_000 || got.PreBudgetItems[0].EndMs != 1500 {
		t.Fatalf("pre-budget edits not applied: %+v", got.PreBudgetItems)
	}
	if len(got.ResolvedItems) != 2 || got.ResolvedItems[1].StartUS != 2_100_000 || got.ResolvedItems[1].StartMs != 2100 {
		t.Fatalf("timing edits not applied: %+v", got.ResolvedItems)
	}
	if len(got.RankedEntities) != 1 || len(got.RankedEntities[0]) != 1 || got.RankedEntities[0][0] != 0 {
		t.Fatalf("ranking not preserved: %+v", got.RankedEntities)
	}
	if len(got.EntranceItems) != 2 || got.EntranceItems[0].MotionParams["enter_frames"] != 12 ||
		got.EntranceItems[0].MotionParams["opaque"] != "keep" {
		t.Fatalf("entrance edits not applied: %+v", got.EntranceItems[0].MotionParams)
	}
	if got.EntranceItems[1].MotionParams["enter_frames"] != 8 {
		t.Fatalf("unmodified entrance changed: %+v", got.EntranceItems[1].MotionParams)
	}

	var request compileEditorialPlanRequest
	if err := json.Unmarshal(runner.input, &request); err != nil {
		t.Fatal(err)
	}
	if request.Operation != "compile_editorial_plan" || request.FPSNum != 24 || request.FPSDen != 1 {
		t.Fatalf("request header: %+v", request)
	}
	if len(request.Items) != 2 || !request.Items[0].HasMap || request.Items[1].ID != "image" {
		t.Fatalf("request items: %+v", request.Items)
	}
	if len(request.EntityScenes) != 1 || len(request.EntranceItems) != 2 {
		t.Fatalf("request entity/entrance inputs: %+v / %+v", request.EntityScenes, request.EntranceItems)
	}
	if runner.calls != 1 {
		t.Fatalf("batch used %d worker calls, want 1", runner.calls)
	}
}

func TestCompileEditorialPlanFailsClosed(t *testing.T) {
	items := []capmodel.OverlayItem{{ID: "map", Kind: "map", DurationUS: 2_000_000, Map: &capmodel.MapOverlay{}}}
	scenes := []capabilityentities.EntityOverlayRankingScene{{Entities: []capabilityentities.EntityOverlayRankingOccurrence{
		{EntityID: "person-tim", OverlayID: "overlay-s1-tim", EntityType: "PERSON", Confidence: 0.98},
	}}}

	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "missing plan",
			output: `{"ok":true}`,
			want:   "returned no plan",
		},
		{
			name: "ranking length",
			output: `{"ok":true,"plan":{
				"pre_budget_edits":[{"index":0,"id":"map","keep":true,"start_us":0,"duration_us":2000000,"start_ms":0,"end_ms":2000}],
				"timing_edits":[{"index":0,"id":"map","keep":true,"start_us":0,"duration_us":2000000,"start_ms":0,"end_ms":2000}],
				"ranked_entities":[],"entrance_edits":[]}}`,
			want: "0 scene selections for 1 scenes",
		},
		{
			name: "invalid entrance",
			output: `{"ok":true,"plan":{
				"pre_budget_edits":[{"index":0,"id":"map","keep":true,"start_us":0,"duration_us":2000000,"start_ms":0,"end_ms":2000}],
				"timing_edits":[{"index":0,"id":"map","keep":true,"start_us":0,"duration_us":2000000,"start_ms":0,"end_ms":2000}],
				"ranked_entities":[[0]],
				"entrance_edits":[{"index":0,"id":"map","enter_frames":0}]}}`,
			want: "invalid enter_frames",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &semanticRunner{output: []byte(test.output)}
			adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.CompileEditorialPlan(context.Background(), items, items, scenes, nil, 24, 1)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}
