package renderqueue

import (
	"encoding/json"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func TestMarshalRenderingGenOverlayPlanCarriesEditorialCompositionSidecar(t *testing.T) {
	composition := json.RawMessage(`{"schema_version":"editorial_composition.v1","composition_id":"c1","scene_id":"s1","window":{"start_us":0,"duration_us":5000000},"members":[{"member_id":"summary-1","kind":"summary","start_us":1000000,"duration_us":3000000,"reveal":"progressive","points":[{"text":"Grounded phrase one","start_us":1000000,"duration_us":1000000},{"text":"Grounded phrase two","start_us":2500000,"duration_us":1500000}]}]}`)
	plan := capmodel.OverlayPlan{
		SchemaVersion:         capmodel.SchemaVersionPlan,
		PlanID:                "plan-1",
		VideoID:               "video-1",
		Width:                 1280,
		Height:                720,
		FPSNum:                24,
		FPSDen:                1,
		EditorialCompositions: []json.RawMessage{composition},
	}

	legacy, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), "editorial_compositions") {
		t.Fatalf("legacy OverlayPlan wire leaked the composition sidecar: %s", legacy)
	}
	rendererWire, err := MarshalRenderingGenOverlayPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	var projected struct {
		EditorialCompositions []json.RawMessage `json:"editorial_compositions"`
	}
	if err := json.Unmarshal(rendererWire, &projected); err != nil {
		t.Fatal(err)
	}
	if len(projected.EditorialCompositions) != 0 {
		t.Fatalf("default renderer wire compositions = %d, want opt-in disabled", len(projected.EditorialCompositions))
	}
	plan.EditorialCompositionsEnabled = true
	rendererWire, err = MarshalRenderingGenOverlayPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rendererWire, &projected); err != nil {
		t.Fatal(err)
	}
	if len(projected.EditorialCompositions) != 1 {
		t.Fatalf("opt-in renderer wire compositions = %d, want 1", len(projected.EditorialCompositions))
	}
	var got, want any
	if err := json.Unmarshal(projected.EditorialCompositions[0], &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(composition, &want); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("renderer sidecar changed during projection:\n got %s\nwant %s", gotJSON, wantJSON)
	}
}
