package scriptgeneration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const mapPlanV2FixtureDir = "testdata/overlay_plan/map_plan"

type mapPlanV2Fixture struct {
	name      string
	result    *GenerateResult
	language  Language
	canvas    OverlayCanvasSpec
	plates    capabilityoverlay.PlateResolver
	planID    string
	videoID   string
	projectID string
}

// mapPlanV2Fixtures pins the producer inputs once. Tests build the v2
// envelope, compare it with the checked-in request and adopt the checked-in
// plan through a v2 compiler. Both goldens are output of the real Rust
// compiler; TestRegenerateOverlayPlanFixturesFromRustCompiler rewrites them.
func mapPlanV2Fixtures(t *testing.T) []mapPlanV2Fixture {
	t.Helper()
	shortMap := mapPlanFixtureResult()
	shortMap.Scenes[0].ShortPhraseOverlay = true

	mapsOnlyCanvas := GoldenOverlayCanvas
	mapsOnlyCanvas.MapsOnly = true
	mapsOnlyCanvas.AnimationCounts = map[string]int{"chain_gap_ms": 5000}
	mapsOnly := mapPlanFixtureResult()
	mapsOnly.Scenes[0].ShortPhraseOverlay = true

	shortText := mapPlanFixtureResult()
	shortText.EntityTimeline = nil
	shortText.Scenes[0].Annotations = nil
	shortText.Scenes[0].ShortPhraseOverlay = true
	shortTextCanvas := GoldenOverlayCanvas
	shortTextCanvas.MaxPhraseOverlays = 1
	shortTextCanvas.AnimationCounts = map[string]int{"short_important_phrase": 1}
	shortTextCanvas.OverlayCategories = map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryPhrases: scriptpkg.ToggleEnabled,
		scriptpkg.OverlayCategoryMaps:    scriptpkg.ToggleDisabled,
	}

	flyoverCanvas := OverlayCanvasSpec{Width: 640, Height: 360, FPSNum: 30, FPSDen: 1}
	uncovered := newOrleansPlate(t)
	uncovered.Window = geodesy.CenteredOn(48.8566, 2.3522, uncovered.Zoom, uncovered.Width, uncovered.Height)
	uncovered.Center = capmodel.MapCenter{Latitude: 48.8566, Longitude: 2.3522}

	return []mapPlanV2Fixture{
		{
			name: "grounded_map", result: mapPlanFixtureResult(), language: "en", canvas: GoldenOverlayCanvas,
			plates: plateStub{plates: []capabilityoverlay.MapPlate{newOrleansPlate(t)}},
			planID: "map-plan", videoID: "map-video", projectID: "map-project",
		},
		{
			name: "short_phrase_map", result: shortMap, language: "en", canvas: GoldenOverlayCanvas,
			plates: plateStub{plates: []capabilityoverlay.MapPlate{newOrleansPlate(t)}},
			planID: "short-map-plan", videoID: "short-map-video", projectID: "map-project",
		},
		{
			name: "maps_only", result: mapsOnly, language: "en", canvas: mapsOnlyCanvas,
			plates: plateStub{plates: []capabilityoverlay.MapPlate{newOrleansPlate(t)}},
			planID: "maps-only-plan", videoID: "maps-only-video", projectID: "map-project",
		},
		{
			name: "short_phrase_text", result: shortText, language: "en", canvas: shortTextCanvas,
			planID: "short-phrase-plan", videoID: "short-phrase-video", projectID: "phrase-project",
		},
		{
			name: "flyover_camera", result: mapPlanFixtureResult(), language: "en", canvas: flyoverCanvas,
			plates: plateStub{plates: newOrleansFlyoverPlates(t)},
			planID: "map-flyover-plan", videoID: "map-flyover-video", projectID: "map-project",
		},
		{
			name: "no_plates", result: mapPlanFixtureResult(), language: "en", canvas: GoldenOverlayCanvas,
			planID: "map-plan", videoID: "map-video", projectID: "map-project",
		},
		{
			name: "uncovered_plate", result: mapPlanFixtureResult(), language: "en", canvas: GoldenOverlayCanvas,
			plates: plateStub{plates: []capabilityoverlay.MapPlate{uncovered}},
			planID: "map-plan", videoID: "map-video", projectID: "map-project",
		},
	}
}

// mapPlanFixtureResult adds the certified editorial manifest the real Rust
// planner requires. It is intentionally empty: these fixtures certify map and
// phrase projection, not editorial bullet selection.
func mapPlanFixtureResult() *GenerateResult {
	result := groundedPlaceResult()
	result.Editorial = &scriptpkg.EditorialManifest{
		SchemaVersion:  overlayplan.EditorialManifestVersion,
		SourceLanguage: "en",
		ProfileVersion: overlayplan.EditorialProfileVersion,
		Certified:      true,
		Fingerprint:    strings.Repeat("d", 64),
		Scenes:         []scriptpkg.EditorialScene{},
	}
	return result
}

func mapPlanV2FixtureByName(t *testing.T, name string) mapPlanV2Fixture {
	t.Helper()
	for _, fixture := range mapPlanV2Fixtures(t) {
		if fixture.name == name {
			return fixture
		}
	}
	t.Fatalf("unknown map plan v2 fixture %q", name)
	return mapPlanV2Fixture{}
}

func buildMapPlanV2RequestForFixture(t *testing.T, fixture mapPlanV2Fixture) overlayplan.V2Request {
	t.Helper()
	input, err := prepareOverlayPlanCompile(fixture.result, fixture.language, fixture.canvas,
		fixture.planID, fixture.videoID, fixture.projectID, fixture.plates)
	require.NoError(t, err)
	require.NotNil(t, input)
	request, err := buildOverlayPlanV2Request(input.result, input.language, input.canvas,
		input.planID, input.videoID, input.projectID, input.motionSeed,
		input.timedScenes, input.scenes, input.rawScenes, input.imagePlan, input.timelineBounds)
	require.NoError(t, err)
	return request
}

func loadMapPlanV2Fixture(t *testing.T, name string) ([]byte, *capmodel.OverlayPlan) {
	t.Helper()
	requestPath := filepath.Join(mapPlanV2FixtureDir, name+".request.json")
	request, err := os.ReadFile(requestPath)
	require.NoError(t, err)
	planPath := filepath.Join(mapPlanV2FixtureDir, name+".plan.json")
	planBytes, err := os.ReadFile(planPath)
	if os.IsNotExist(err) {
		return request, nil
	}
	require.NoError(t, err)
	var plan capmodel.OverlayPlan
	require.NoError(t, json.Unmarshal(planBytes, &plan))
	return request, &plan
}

// mapPlanV2RequestForTest is the request-only path for fixtures where the
// producer removes every map candidate. The checked-in request is the golden
// the producer must still emit, even when there is nothing for Rust to plan.
func mapPlanV2RequestForTest(t *testing.T, name string) overlayplan.V2Request {
	t.Helper()
	fixture := mapPlanV2FixtureByName(t, name)
	request := buildMapPlanV2RequestForFixture(t, fixture)
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	frozen, _ := loadMapPlanV2Fixture(t, name)
	require.JSONEq(t, string(frozen), string(raw))
	return request
}

// compileMapPlanV2ForTest exercises the production v2 entry point with the
// sealed plan the real Rust compiler emitted for that request. The request is
// still built by the real producer and is compared JSON-equivalently with the
// checked-in envelope.
func compileMapPlanV2ForTest(t *testing.T, name string) (*capmodel.OverlayPlan, error) {
	t.Helper()
	fixture := mapPlanV2FixtureByName(t, name)
	request := buildMapPlanV2RequestForFixture(t, fixture)
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	frozen, plan := loadMapPlanV2Fixture(t, name)
	require.JSONEq(t, string(frozen), string(raw))
	if plan == nil {
		return nil, nil
	}
	compiler := &recordingOverlayPlanV2Compiler{plan: plan}
	adopted, err := compileOverlayPlanWithMotionOffset(context.Background(), fixture.result, fixture.language,
		fixture.canvas, fixture.planID, fixture.videoID, fixture.projectID, fixture.plates, compiler)
	if err != nil {
		return nil, err
	}
	require.JSONEq(t, string(frozen), string(compiler.payload))
	return adopted, nil
}
