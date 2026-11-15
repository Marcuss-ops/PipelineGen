package overlays

import (
	"context"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

type recordingMapWindowTrimmer struct {
	calls  int
	fpsNum int
	fpsDen int
	items  []capmodel.OverlayItem
}

// passThroughMapWindowTrimmer keeps planner unit tests focused on map
// composition and budgeting. The Rust operation's timing behavior is covered
// by the real-worker boundary tests.
type passThroughMapWindowTrimmer struct{}

func (passThroughMapWindowTrimmer) TrimMapWindowsBeforeBudget(_ context.Context, items []capmodel.OverlayItem, _, _ int) ([]capmodel.OverlayItem, error) {
	return append([]capmodel.OverlayItem(nil), items...), nil
}

func buildPlanWithMapWindowTrimmerStub(input PlanInput, config PlannerConfig) (capmodel.OverlayPlan, error) {
	return BuildPlanWithMapWindowTrimmer(context.Background(), input, config, passThroughMapWindowTrimmer{})
}

func (r *recordingMapWindowTrimmer) TrimMapWindowsBeforeBudget(_ context.Context, items []capmodel.OverlayItem, fpsNum, fpsDen int) ([]capmodel.OverlayItem, error) {
	r.calls++
	r.fpsNum, r.fpsDen = fpsNum, fpsDen
	r.items = append([]capmodel.OverlayItem(nil), items...)
	return append([]capmodel.OverlayItem(nil), items...), nil
}

func TestBuildPlanWithMapWindowTrimmerDelegatesMapPass(t *testing.T) {
	plate := testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)
	scene := SceneInput{
		ID:   "scene-1",
		Maps: []MapCandidate{groundedCandidate(t, "city:rome", "Rome", romeLat, romeLon, 1_000_000, 2_000_000)},
	}
	trimmer := &recordingMapWindowTrimmer{}

	plan, err := BuildPlanWithMapWindowTrimmer(context.Background(), PlanInput{
		PlanID: "map-trimmer-plan", VideoID: "map-trimmer-video",
		Width: mapTestWidth, Height: mapTestHeight, FPSNum: 30, FPSDen: 1,
		PlateResolver: stubPlateResolver{plates: []MapPlate{plate}},
		Scenes:        []SceneInput{scene},
	}, AllCandidatesPlannerConfig([]SceneInput{scene}), trimmer)
	if err != nil {
		t.Fatalf("build plan with trimmer: %v", err)
	}
	if trimmer.calls != 1 {
		t.Fatalf("trimmer calls = %d, want exactly one map-window pass", trimmer.calls)
	}
	if trimmer.fpsNum != 30 || trimmer.fpsDen != 1 {
		t.Fatalf("trimmer fps = %d/%d, want 30/1", trimmer.fpsNum, trimmer.fpsDen)
	}
	if len(trimmer.items) != 1 || trimmer.items[0].Kind != "map" {
		t.Fatalf("trimmer items = %+v, want the planned map item", trimmer.items)
	}
	if len(plan.Items) != 1 || plan.Items[0].Kind != "map" {
		t.Fatalf("plan items = %+v, want the map returned by the trimmer", plan.Items)
	}
}

func TestBuildPlanFailsClosedWithoutBrainRustMapTrimmer(t *testing.T) {
	plate := testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)
	scene := SceneInput{
		ID:   "scene-1",
		Maps: []MapCandidate{groundedCandidate(t, "city:rome", "Rome", romeLat, romeLon, 1_000_000, 2_000_000)},
	}
	_, err := BuildPlan(PlanInput{
		PlanID: "map-trimmer-required-plan", VideoID: "map-trimmer-required-video",
		Width: mapTestWidth, Height: mapTestHeight, FPSNum: 30, FPSDen: 1,
		PlateResolver: stubPlateResolver{plates: []MapPlate{plate}},
		Scenes:        []SceneInput{scene},
	}, AllCandidatesPlannerConfig([]SceneInput{scene}))
	if err == nil || !strings.Contains(err.Error(), "BrainRust map-window trimmer is required") {
		t.Fatalf("map plan without BrainRust trimmer error = %v, want fail-closed trimmer requirement", err)
	}
}
