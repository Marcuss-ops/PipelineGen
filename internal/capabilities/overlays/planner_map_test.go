package overlays

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
)

const (
	mapTestZoom   = 10
	mapTestWidth  = 1280
	mapTestHeight = 720
)

// writeMapPlateRaster encodes a REAL PNG of the exact plate size and returns a
// content-addressed asset ref over those bytes. The planner's map items are
// validated against the bytes on disk (digest + PNG magic), so the fixture is a
// real raster rather than a mocked digest.
func writeMapPlateRaster(t *testing.T, width, height int) OverlayAssetRef {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 4, G: 8, B: 16, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode plate raster: %v", err)
	}
	path := filepath.Join(t.TempDir(), "plate.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write plate raster: %v", err)
	}
	return OverlayAssetRef{
		AssetID:   "operator-plate",
		SHA256:    digest.SHA256Bytes(buf.Bytes()),
		MediaType: "image/png",
		LocalPath: path,
	}
}

// testMapPlate builds a plate shaped exactly like the composition-root adapter
// builds one from a certified manifest entry: a real raster, its certified
// window, and provenance.
func testMapPlate(t *testing.T, id string, lat, lon float64, width, height int) MapPlate {
	t.Helper()
	return MapPlate{
		ID:          id,
		License:     "operator-supplied",
		Attribution: "© OpenStreetMap contributors",
		Center:      MapCenter{Latitude: lat, Longitude: lon},
		Zoom:        mapTestZoom,
		Width:       width,
		Height:      height,
		Asset:       writeMapPlateRaster(t, width, height),
		Window:      geodesy.CenteredOn(lat, lon, mapTestZoom, width, height),
	}
}

// stubPlateResolver resolves through the certified windows, in the order the
// manifest would present them.
type stubPlateResolver struct{ plates []MapPlate }

func (s stubPlateResolver) ResolvePlate(latitude, longitude float64) (MapPlate, bool) {
	for _, plate := range s.plates {
		if plate.Window.Contains(latitude, longitude) {
			return plate, true
		}
	}
	return MapPlate{}, false
}

type flyoverPlateResolver struct {
	stubPlateResolver
	flyover MapPlate
	ok      bool
}

func (r flyoverPlateResolver) ResolveFlyover(_, _ MapCenter, _, _ int) (MapPlate, bool) {
	return r.flyover, r.ok
}

var (
	romeLat, romeLon   = 41.9028, 12.4964
	parisLat, parisLon = 48.8566, 2.3522
)

func groundedCandidate(t *testing.T, entityID, label string, lat, lon float64, startUS, durationUS int64) MapCandidate {
	t.Helper()
	candidate, ok := NewMapCandidate(entityID, label, lat, lon, startUS, durationUS, 0.9)
	if !ok {
		t.Fatalf("fixture candidate %q is not grounded", entityID)
	}
	return candidate
}

// TestBuildPlanEmitsMapItemFromCertifiedPlate certifies the emission contract:
// a grounded place covered by a certified plate becomes exactly one map item
// carrying the plate's georeference, provenance, content-addressed raster and
// the occurrence's audio window.
func TestBuildPlanEmitsMapItemFromCertifiedPlate(t *testing.T) {
	plate := testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)
	scene := SceneInput{
		ID:   "scene-1",
		Maps: []MapCandidate{groundedCandidate(t, "city:rome", "Rome", romeLat, romeLon, 1_000_000, 2_000_000)},
	}
	config := AllCandidatesPlannerConfig([]SceneInput{scene})

	plan, err := BuildPlan(PlanInput{
		PlanID: "map-plan", VideoID: "map-video",
		Width: mapTestWidth, Height: mapTestHeight, FPSNum: 30, FPSDen: 1,
		PlateResolver: stubPlateResolver{plates: []MapPlate{plate}},
		Scenes:        []SceneInput{scene},
	}, config)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("expected exactly one map item, got %d (%v)", len(plan.Items), plan.Items)
	}
	item := plan.Items[0]
	if item.Kind != "map" || item.TemplateID != "MAP" {
		t.Fatalf("item kind/template = %q/%q, want map/MAP", item.Kind, item.TemplateID)
	}
	if item.SceneID != "scene-1" {
		t.Fatalf("map item scene = %q, want scene-1", item.SceneID)
	}
	// The item window is the grounded occurrence window, not a guess.
	if item.StartUS != 1_000_000 || item.DurationUS != 2_000_000 {
		t.Fatalf("map item window = %d+%d, want 1000000+2000000", item.StartUS, item.DurationUS)
	}
	if item.StartMs != 1000 || item.EndMs != 3000 {
		t.Fatalf("map item ms window = %d..%d, want 1000..3000", item.StartMs, item.EndMs)
	}
	if item.Map == nil {
		t.Fatal("map item carries no map declaration")
	}
	if item.Map.Provider != "local" {
		t.Fatalf("map provider = %q, want local", item.Map.Provider)
	}
	if item.Map.SourceID != plate.ID || item.Map.SourceLicense != plate.License {
		t.Fatalf("map provenance = %q/%q, want %q/%q", item.Map.SourceID, item.Map.SourceLicense, plate.ID, plate.License)
	}
	if item.Map.Attribution != plate.Attribution {
		t.Fatalf("map attribution = %q, want %q", item.Map.Attribution, plate.Attribution)
	}
	if item.Map.Center.Latitude != plate.Center.Latitude || item.Map.Center.Longitude != plate.Center.Longitude {
		t.Fatalf("map center = %v, want %v", item.Map.Center, plate.Center)
	}
	if item.Map.Zoom != plate.Zoom || item.Map.Width != mapTestWidth || item.Map.Height != mapTestHeight {
		t.Fatalf("map georeference = zoom %d %dx%d, want zoom %d %dx%d",
			item.Map.Zoom, item.Map.Width, item.Map.Height, plate.Zoom, mapTestWidth, mapTestHeight)
	}
	if len(item.Map.Pins) != 1 {
		t.Fatalf("expected one pin, got %d", len(item.Map.Pins))
	}
	pin := item.Map.Pins[0]
	if pin.ID != "city:rome" || pin.Label != "Rome" {
		t.Fatalf("pin = %q/%q, want city:rome/Rome", pin.ID, pin.Label)
	}
	if len(item.AssetRefs) != 1 || item.AssetRefs[0].SHA256 != plate.Asset.SHA256 {
		t.Fatalf("map asset refs = %v, want the certified plate raster", item.AssetRefs)
	}
	if item.AssetRefs[0].LocalPath != plate.Asset.LocalPath {
		t.Fatalf("map basemap local path = %q, want %q", item.AssetRefs[0].LocalPath, plate.Asset.LocalPath)
	}
	if item.AssetRefs[0].URL != "" {
		t.Fatalf("map basemap must not carry a network reference, got %q", item.AssetRefs[0].URL)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("emitted map plan does not seal: %v", err)
	}
}

// TestBuildPlanMapFailsClosed certifies that every unmet prerequisite yields NO
// map at all: no resolver, no covering plate, a plate raster that is not this
// canvas, and a candidate that is not grounded WGS84.
func TestBuildPlanMapFailsClosed(t *testing.T) {
	rome := groundedCandidate(t, "city:rome", "Rome", romeLat, romeLon, 1_000_000, 2_000_000)
	parisPlate := testMapPlate(t, "plate-paris", parisLat, parisLon, mapTestWidth, mapTestHeight)
	smallPlate := testMapPlate(t, "plate-small", romeLat, romeLon, 640, 360)

	cases := map[string]struct {
		resolver PlateResolver
		maps     []MapCandidate
	}{
		"no resolver":       {resolver: nil, maps: []MapCandidate{rome}},
		"no covering plate": {resolver: stubPlateResolver{plates: []MapPlate{parisPlate}}, maps: []MapCandidate{rome}},
		"plate raster is not this canvas": {
			resolver: stubPlateResolver{plates: []MapPlate{smallPlate}},
			maps:     []MapCandidate{rome},
		},
		"no candidates": {
			resolver: stubPlateResolver{plates: []MapPlate{testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)}},
			maps:     nil,
		},
		"ungrounded candidate": {
			resolver: stubPlateResolver{plates: []MapPlate{testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)}},
			maps:     []MapCandidate{{EntityID: "city:nowhere", Label: "Nowhere", Latitude: 91, Longitude: 0, StartUS: 0, DurationUS: 1_000_000}},
		},
		"empty audio span": {
			resolver: stubPlateResolver{plates: []MapPlate{testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)}},
			maps:     []MapCandidate{{EntityID: "city:rome", Label: "Rome", Latitude: romeLat, Longitude: romeLon}},
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			scene := SceneInput{ID: "scene-1", Maps: testCase.maps}
			plan, err := BuildPlan(PlanInput{
				PlanID: "map-plan", VideoID: "map-video",
				Width: mapTestWidth, Height: mapTestHeight, FPSNum: 30, FPSDen: 1,
				PlateResolver: testCase.resolver,
				Scenes:        []SceneInput{scene},
			}, AllCandidatesPlannerConfig([]SceneInput{scene}))
			if err != nil {
				t.Fatalf("build plan: %v", err)
			}
			for _, item := range plan.Items {
				if item.Kind == "map" {
					t.Fatalf("expected no map item, got %+v", item)
				}
			}
		})
	}
}

// TestBuildPlanGroupsOnePlateIntoOneMapWithDeterministicPins certifies the
// per-scene dedupe: every place on the same plate becomes ONE map item whose
// pins are ordered by stable entity id, and a repeated place is a single pin.
func TestBuildPlanGroupsOnePlateIntoOneMapWithDeterministicPins(t *testing.T) {
	plate := testMapPlate(t, "plate-lazio", romeLat, romeLon, mapTestWidth, mapTestHeight)
	// Two distinct grounded places near Rome, supplied out of id order, plus a
	// duplicate of one of them.
	scene := SceneInput{ID: "scene-1", Maps: []MapCandidate{
		groundedCandidate(t, "city:zagarolo", "Zagarolo", romeLat+0.05, romeLon+0.05, 2_000_000, 1_000_000),
		groundedCandidate(t, "city:rome", "Rome", romeLat, romeLon, 1_000_000, 1_000_000),
		groundedCandidate(t, "city:rome", "Rome", romeLat, romeLon, 1_000_000, 1_000_000),
	}}
	plan, err := BuildPlan(PlanInput{
		PlanID: "map-plan", VideoID: "map-video",
		Width: mapTestWidth, Height: mapTestHeight, FPSNum: 30, FPSDen: 1,
		PlateResolver: stubPlateResolver{plates: []MapPlate{plate}},
		Scenes:        []SceneInput{scene},
	}, AllCandidatesPlannerConfig([]SceneInput{scene}))
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("expected one map item for one plate, got %d", len(plan.Items))
	}
	item := plan.Items[0]
	if len(item.Map.Pins) != 2 {
		t.Fatalf("expected two deduplicated pins, got %d (%v)", len(item.Map.Pins), item.Map.Pins)
	}
	if item.Map.Pins[0].ID != "city:rome" || item.Map.Pins[1].ID != "city:zagarolo" {
		t.Fatalf("pins are not in stable-id order: %q, %q", item.Map.Pins[0].ID, item.Map.Pins[1].ID)
	}
	// The item window covers the union of the grounded occurrences.
	if item.StartUS != 1_000_000 || item.DurationUS != 2_000_000 {
		t.Fatalf("map window = %d+%d, want the union 1000000+2000000", item.StartUS, item.DurationUS)
	}
}

// TestBuildPlanMapMotionsAreCertifiedAndDeterministic certifies that consecutive
// maps in a run rotate inside the certified centered pool and that the same
// input always produces the same motion.
func TestBuildPlanMapMotionsAreCertifiedAndDeterministic(t *testing.T) {
	plates := []MapPlate{
		testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight),
		testMapPlate(t, "plate-paris", parisLat, parisLon, mapTestWidth, mapTestHeight),
	}
	scenes := []SceneInput{
		{ID: "scene-1", Maps: []MapCandidate{groundedCandidate(t, "city:rome", "Rome", romeLat, romeLon, 0, 1_000_000)}},
		{ID: "scene-2", Maps: []MapCandidate{groundedCandidate(t, "city:paris", "Paris", parisLat, parisLon, 0, 1_000_000)}},
	}
	build := func() OverlayPlan {
		// Exercise a caller-selected ceiling below the default three-map budget.
		config := AllCandidatesPlannerConfig(scenes)
		config.RunLevelMapOverlayLimit = 2
		plan, err := BuildPlan(PlanInput{
			PlanID: "map-plan", VideoID: "map-video",
			Width: mapTestWidth, Height: mapTestHeight, FPSNum: 30, FPSDen: 1,
			PlateResolver: stubPlateResolver{plates: plates},
			Scenes:        scenes,
		}, config)
		if err != nil {
			t.Fatalf("build plan: %v", err)
		}
		return plan
	}
	first := build()
	second := build()
	var maps int
	for i := range first.Items {
		if first.Items[i].Kind != "map" {
			continue
		}
		maps++
		motion := first.Items[i].Map.MotionID
		if motion != second.Items[i].Map.MotionID {
			t.Fatalf("map motion is not deterministic: %q != %q", motion, second.Items[i].Map.MotionID)
		}
		if !containsString(mapMotionIDs(), motion) {
			t.Fatalf("map motion %q is not in the certified centered pool %v", motion, mapMotionIDs())
		}
	}
	if maps != 2 {
		t.Fatalf("expected two map items (one per scene/plate), got %d", maps)
	}
	if first.Items[0].Map.MotionID == first.Items[1].Map.MotionID {
		t.Fatalf("consecutive maps reused motion %q", first.Items[0].Map.MotionID)
	}
}

// TestMapItemsDoNotDisplaceImagesOrPhrases certifies the editorial budget
// contract: maps are a SEPARATE arm, so admitting one can never evict an image
// or a phrase, and a run with no eligible map is byte-for-byte the old result.
func TestMapItemsDoNotDisplaceImagesOrPhrases(t *testing.T) {
	plate := testMapPlate(t, "plate-rome", romeLat, romeLon, mapTestWidth, mapTestHeight)
	imageRaster := writeMapPlateRaster(t, 8, 8)
	scene := SceneInput{
		ID: "scene-1",
		Maps: []MapCandidate{
			groundedCandidate(t, "city:rome", "Rome", romeLat, romeLon, 0, 1_000_000),
		},
		Images: []ImageCandidate{{
			AssetID: "photo-1", SHA256: imageRaster.SHA256, MediaType: "image/png",
			LocalPath: imageRaster.LocalPath, StartMs: 0, EndMs: 2000, Score: 0.8,
		}},
		Phrases: []TimedAnnotation{{Text: "A grounded headline", StartMs: 0, EndMs: 2000, Score: 0.7}},
	}
	config := AllCandidatesPlannerConfig([]SceneInput{scene})
	plan, err := BuildPlan(PlanInput{
		PlanID: "map-plan", VideoID: "map-video",
		Width: mapTestWidth, Height: mapTestHeight, FPSNum: 30, FPSDen: 1,
		PlateResolver: stubPlateResolver{plates: []MapPlate{plate}},
		Scenes:        []SceneInput{scene},
	}, config)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	counts := map[string]int{}
	for _, item := range plan.Items {
		counts[item.Kind]++
	}
	if counts["map"] != 1 || counts["image"] != 1 || counts["text_phrase"] != 1 {
		t.Fatalf("plan content = %v, want one map, one image and one phrase", counts)
	}

	// The map arm is independent: a zero map ceiling still keeps the certified
	// default (one map), and the image/phrase keep-sets are unchanged either way.
	budgeted, phraseBudget := ApplyEditorialOverlayBudgetWithLimits(plan.Items, 0, 0)
	var budgetedCounts = map[string]int{}
	for _, item := range budgeted {
		budgetedCounts[item.Kind]++
	}
	if budgetedCounts["map"] != 1 || budgetedCounts["image"] != 1 || budgetedCounts["text_phrase"] != 1 {
		t.Fatalf("budgeted content = %v, want the same one map, one image and one phrase", budgetedCounts)
	}
	if phraseBudget.Materialized != 1 {
		t.Fatalf("phrase budget = %+v, want one materialized phrase", phraseBudget)
	}

	// With no map candidates at all the budget result is exactly the
	// pre-feature behaviour: images and phrases survive, no map is invented.
	withoutMaps := scene
	withoutMaps.Maps = nil
	plainPlan, err := BuildPlan(PlanInput{
		PlanID: "map-plan", VideoID: "map-video",
		Width: mapTestWidth, Height: mapTestHeight, FPSNum: 30, FPSDen: 1,
		PlateResolver: stubPlateResolver{plates: []MapPlate{plate}},
		Scenes:        []SceneInput{withoutMaps},
	}, AllCandidatesPlannerConfig([]SceneInput{withoutMaps}))
	if err != nil {
		t.Fatalf("build plan without maps: %v", err)
	}
	plainBudgeted, _ := ApplyEditorialOverlayBudgetWithLimits(plainPlan.Items, 0, 0)
	for _, item := range plainBudgeted {
		if item.Kind == "map" {
			t.Fatalf("a map was invented without candidates: %+v", item)
		}
	}
	if len(plainBudgeted) != 2 {
		t.Fatalf("expected the image and the phrase to survive, got %d items", len(plainBudgeted))
	}
}

// TestRankedUniqueMapIndicesDeduplicatesPerScene certifies the budget's map arm
// in isolation: the same plate in the same scene is one map (highest priority
// wins), the same plate in another scene is a distinct map, and the cap holds.
func TestBuildPlanEmitsNativeFlyToFromCertifiedOfflineLODs(t *testing.T) {
	coarse := testMapPlate(t, "plate-coarse", 0, 0, 2048, 2048)
	coarse.Zoom = 4
	coarse.Window = geodesy.CenteredOn(0, 0, coarse.Zoom, coarse.Width, coarse.Height)
	coarse.Asset.AssetID = "map-coarse"
	fine := testMapPlate(t, "plate-fine", 0, 0, 4096, 4096)
	fine.Zoom = 6
	fine.Window = geodesy.CenteredOn(0, 0, fine.Zoom, fine.Width, fine.Height)
	fine.Asset.AssetID = "map-fine"
	coarse.LODs = []MapPlate{fine}

	scene := SceneInput{ID: "scene-flyover", Maps: []MapCandidate{
		groundedCandidate(t, "city:start", "Start", 0, 0, 1_000_000, 1_000_000),
		groundedCandidate(t, "city:end", "End", 0, 0.2, 2_000_000, 1_000_000),
	}}
	resolver := flyoverPlateResolver{
		stubPlateResolver: stubPlateResolver{plates: []MapPlate{coarse}},
		flyover:           coarse, ok: true,
	}
	plan, err := BuildPlan(PlanInput{
		PlanID: "flyover-plan", VideoID: "flyover-video",
		Width: mapTestWidth, Height: mapTestHeight, FPSNum: 30, FPSDen: 1,
		PlateResolver: resolver, Scenes: []SceneInput{scene},
	}, AllCandidatesPlannerConfig([]SceneInput{scene}))
	if err != nil {
		t.Fatalf("build camera map plan: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("camera route emitted %d items, want one grouped map", len(plan.Items))
	}
	item := plan.Items[0]
	if item.Map == nil || item.Map.CameraMove == nil {
		t.Fatalf("grounded route did not produce a camera_move: %+v", item.Map)
	}
	move := item.Map.CameraMove
	if move.From != (MapCenter{Latitude: 0, Longitude: 0}) || move.To != (MapCenter{Latitude: 0, Longitude: 0.2}) {
		t.Fatalf("camera endpoints = %+v → %+v, want chronological grounded endpoints", move.From, move.To)
	}
	if move.StartZoom != 4 || move.EndZoom != 6 || len(item.Map.LODs) != 2 || len(item.AssetRefs) != 2 {
		t.Fatalf("fly-to LOD package incomplete: camera=%+v LODs=%+v assets=%+v", move, item.Map.LODs, item.AssetRefs)
	}
	if item.AssetRefs[0].URL != "" || item.AssetRefs[1].URL != "" {
		t.Fatalf("offline LOD package unexpectedly carries a network URL: %+v", item.AssetRefs)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("camera map plan failed contract validation: %v", err)
	}
}

func TestBuildPlanFallsBackToStaticMapWhenFlyoverCoverageIsUnavailable(t *testing.T) {
	plate := testMapPlate(t, "plate-route", 0, 0, mapTestWidth, mapTestHeight)
	scene := SceneInput{ID: "scene-route", Maps: []MapCandidate{
		groundedCandidate(t, "city:start", "Start", 0, 0, 1_000_000, 1_000_000),
		groundedCandidate(t, "city:end", "End", 0, 0.2, 2_000_000, 1_000_000),
	}}
	resolver := flyoverPlateResolver{
		stubPlateResolver: stubPlateResolver{plates: []MapPlate{plate}},
		ok:                false,
	}
	plan, err := BuildPlan(PlanInput{
		PlanID: "static-fallback-plan", VideoID: "static-fallback-video",
		Width: mapTestWidth, Height: mapTestHeight, FPSNum: 30, FPSDen: 1,
		PlateResolver: resolver, Scenes: []SceneInput{scene},
	}, AllCandidatesPlannerConfig([]SceneInput{scene}))
	if err != nil {
		t.Fatalf("build static fallback: %v", err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Map == nil {
		t.Fatalf("covered candidates should retain the static map fallback: %+v", plan.Items)
	}
	if plan.Items[0].Map.CameraMove != nil || len(plan.Items[0].Map.LODs) != 0 || len(plan.Items[0].AssetRefs) != 1 {
		t.Fatalf("uncovered flyover must not invent camera/LOD metadata: %+v", plan.Items[0].Map)
	}
}

func TestRankedUniqueMapIndicesDeduplicatesPerScene(t *testing.T) {
	makeItem := func(id, sceneID, plateID string, priority float64) OverlayItem {
		return OverlayItem{
			ID: id, SceneID: sceneID, Kind: "map", TemplateID: "MAP",
			Map:    &MapOverlay{Provider: "local", SourceID: plateID},
			Params: map[string]any{"priority": priority},
		}
	}
	items := []OverlayItem{
		makeItem("low", "scene-1", "plate-a", 0.1),
		makeItem("high", "scene-1", "plate-a", 0.9),
		makeItem("other-scene", "scene-2", "plate-a", 0.5),
		makeItem("second-plate", "scene-1", "plate-b", 0.4),
	}
	indices := rankedUniqueMapIndices(items, 2)
	if len(indices) != 2 {
		t.Fatalf("map cap ignored: %v", indices)
	}
	if indices[0] != 1 {
		t.Fatalf("highest-priority duplicate must win, got index %d", indices[0])
	}
	// A limit wider than the candidate set keeps the second scene's map and
	// drops only the lowest-priority duplicate.
	uncapped := rankedUniqueMapIndices(items, len(items))
	if len(uncapped) != 3 {
		t.Fatalf("expected three unique plate/scene maps, got %v", uncapped)
	}
	for _, index := range uncapped {
		if index == 0 {
			t.Fatal("the duplicate with the lower priority was admitted")
		}
	}
}

// TestBuildPlanMergesCrossSceneRouteIntoOneMapItem certifies the run-level
// route contract: grounded places mentioned across DIFFERENT scenes resolve
// against ONE certified flyover plate and lower to exactly ONE map item —
// a three-city script renders one map animation, never one render job per
// scene. The item anchors to the first mentioning scene and carries every
// pin with the run-wide chronological camera route.
func TestBuildPlanMergesCrossSceneRouteIntoOneMapItem(t *testing.T) {
	// One route plate big enough to hold the whole Paris→London→Rome
	// viewport across both LODs (zoom 5 base, zoom 7 fine).
	routeLat, routeLon := 45.4, 6.2
	base := testMapPlate(t, "route-base", routeLat, routeLon, 7680, 4320)
	base.Zoom = 5
	base.Window = geodesy.CenteredOn(routeLat, routeLon, 5, 7680, 4320)
	base.Asset.AssetID = "route-base-asset"
	fine := testMapPlate(t, "route-fine", routeLat, routeLon, 7680, 4320)
	fine.Zoom = 7
	fine.Window = geodesy.CenteredOn(routeLat, routeLon, 7, 7680, 4320)
	fine.Asset.AssetID = "route-fine-asset"
	base.LODs = []MapPlate{fine}

	scenes := []SceneInput{
		{ID: "scene-1", Maps: []MapCandidate{groundedCandidate(t, "city:paris", "Paris", parisLat, parisLon, 0, 1_000_000)}},
		{ID: "scene-2", Maps: []MapCandidate{groundedCandidate(t, "city:london", "London", 51.5074, -0.1278, 10_000_000, 1_000_000)}},
		{ID: "scene-3", Maps: []MapCandidate{groundedCandidate(t, "city:rome", "Rome", romeLat, romeLon, 20_000_000, 1_000_000)}},
	}
	plan, err := BuildPlan(PlanInput{
		PlanID: "map-plan", VideoID: "map-video",
		Width: mapTestWidth, Height: mapTestHeight, FPSNum: 30, FPSDen: 1,
		PlateResolver: flyoverPlateResolver{stubPlateResolver: stubPlateResolver{plates: []MapPlate{base}}, flyover: base, ok: true},
		Scenes:        scenes,
	}, AllCandidatesPlannerConfig(scenes))
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	mapCount := 0
	var route *OverlayItem
	for i := range plan.Items {
		if plan.Items[i].Kind != "map" {
			continue
		}
		mapCount++
		route = &plan.Items[i]
	}
	if mapCount != 1 || route == nil {
		t.Fatalf("expected ONE run-level map item, got %d", mapCount)
	}
	if route.SceneID != "scene-1" {
		t.Fatalf("route map anchored to %q, want the first mentioning scene", route.SceneID)
	}
	if len(route.Map.Pins) != 3 {
		t.Fatalf("route map pins = %d, want all three cities", len(route.Map.Pins))
	}
	if route.Map.CameraMove == nil {
		t.Fatal("run-level route map carries no camera move")
	}
	if route.Map.CameraMove.From.Latitude != parisLat || route.Map.CameraMove.To.Latitude != romeLat {
		t.Fatalf("camera route = %v→%v, want the run-wide chronological endpoints", route.Map.CameraMove.From, route.Map.CameraMove.To)
	}
	if len(route.AssetRefs) != 2 {
		t.Fatalf("route assets = %d, want base + fine LOD", len(route.AssetRefs))
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("merged route plan does not seal: %v", err)
	}
}
