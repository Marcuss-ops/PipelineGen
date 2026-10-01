package overlays

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// writeMapRaster encodes a real, minimal PNG to disk and returns a matching
// content-addressed asset ref plus its path. The map contract is exercised
// against actual bytes, never a mocked digest.
func writeMapRaster(t *testing.T) (OverlayAssetRef, string) {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 8, G: 16, B: 32, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	path := filepath.Join(t.TempDir(), "basemap.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write raster: %v", err)
	}
	return OverlayAssetRef{
		AssetID:   "map-basemap",
		SHA256:    digest.SHA256Bytes(buf.Bytes()),
		MediaType: "image/png",
		LocalPath: path,
	}, path
}

// validMapOverlay is a complete, contract-valid map declaration centred on
// Rome with the single pin at the window centre.
func validMapOverlay(canvasWidth, canvasHeight int) MapOverlay {
	return MapOverlay{
		Provider:      "local",
		SourceID:      "operator-plate-2026",
		SourceLicense: "operator-supplied",
		Center:        MapCenter{Latitude: 41.9028, Longitude: 12.4964},
		Zoom:          6,
		Width:         canvasWidth,
		Height:        canvasHeight,
		Attribution:   "© OpenStreetMap contributors",
		MotionID:      "image_fade_reveal",
		Pins: []MapOverlayPin{{
			ID: "rome", Label: "Rome", Latitude: 41.9028, Longitude: 12.4964,
			Color: "#FF0000", RadiusPX: 8,
		}},
	}
}

func TestMapOverlayValidateAcceptsOperatorSuppliedRaster(t *testing.T) {
	asset, _ := writeMapRaster(t)
	mapOverlay := validMapOverlay(1280, 720)

	if err := mapOverlay.Validate(1280, 720, []OverlayAssetRef{asset}); err != nil {
		t.Fatalf("valid operator-supplied map rejected: %v", err)
	}
}

// TestMapOverlayValidateAcceptsAttributionWithOrdinaryLatinLetters is the
// regression for the historical double-escaped control-character class, which
// banned the literal characters \ x 0 r n and therefore rejected every real
// attribution ("OpenStreetMap contributors" alone carries r and n).
func TestMapOverlayValidateAcceptsAttributionWithOrdinaryLatinLetters(t *testing.T) {
	asset, _ := writeMapRaster(t)
	for _, attribution := range []string{
		"© OpenStreetMap contributors",
		"© OpenStreetMap contributors — ODbL",
		"Raster: operator plate 2026-01 (CC-BY-4.0)",
	} {
		mapOverlay := validMapOverlay(1280, 720)
		mapOverlay.Attribution = attribution
		if err := mapOverlay.Validate(1280, 720, []OverlayAssetRef{asset}); err != nil {
			t.Fatalf("ordinary attribution %q rejected: %v", attribution, err)
		}
	}
}

func TestMapOverlayValidateRejectsControlCharactersInVisibleText(t *testing.T) {
	cases := map[string]func(*MapOverlay){
		"attribution newline": func(m *MapOverlay) { m.Attribution = "OpenStreetMap\ncontributors" },
		"attribution carriage": func(m *MapOverlay) {
			m.Attribution = "OpenStreetMap\rcontributors"
		},
		"attribution tab":    func(m *MapOverlay) { m.Attribution = "OSM\tcontributors" },
		"attribution nul":    func(m *MapOverlay) { m.Attribution = "OSM\x00contributors" },
		"attribution delete": func(m *MapOverlay) { m.Attribution = "OSM\x7fcontributors" },
		"attribution invalid utf8": func(m *MapOverlay) {
			m.Attribution = "OSM\xffcontributors"
		},
		"attribution blank": func(m *MapOverlay) { m.Attribution = "   " },
		"pin label newline": func(m *MapOverlay) { m.Pins[0].Label = "Ro\nme" },
		"pin id blank padded": func(m *MapOverlay) {
			m.Pins[0].ID = " rome "
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			asset, _ := writeMapRaster(t)
			mapOverlay := validMapOverlay(1280, 720)
			mutate(&mapOverlay)
			if err := mapOverlay.Validate(1280, 720, []OverlayAssetRef{asset}); err == nil {
				t.Fatal("expected invalid visible text to be rejected")
			}
		})
	}
}

func TestMapOverlayValidateRejectsNonPNGRaster(t *testing.T) {
	asset, path := writeMapRaster(t)
	payload := []byte("definitely not a png raster")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("overwrite raster: %v", err)
	}
	asset.SHA256 = digest.SHA256Bytes(payload)
	mapOverlay := validMapOverlay(1280, 720)

	err := mapOverlay.Validate(1280, 720, []OverlayAssetRef{asset})
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("expected non-PNG raster integrity failure, got %v", err)
	}
}

func TestMapOverlayValidateRejectsRasterHashMismatch(t *testing.T) {
	asset, _ := writeMapRaster(t)
	asset.SHA256 = strings.Repeat("ab", 32)
	mapOverlay := validMapOverlay(1280, 720)

	if err := mapOverlay.Validate(1280, 720, []OverlayAssetRef{asset}); err == nil {
		t.Fatal("expected raster SHA-256 mismatch to be rejected")
	}
}

func TestMapOverlayValidateRejectsNetworkRaster(t *testing.T) {
	asset, _ := writeMapRaster(t)
	asset.URL = "https://tile.example.com/6/21/13.png"
	mapOverlay := validMapOverlay(1280, 720)

	if err := mapOverlay.Validate(1280, 720, []OverlayAssetRef{asset}); err == nil {
		t.Fatal("expected network basemap reference to be rejected")
	}
}

func TestMapOverlayValidateRejectsNonLocalProvider(t *testing.T) {
	asset, _ := writeMapRaster(t)
	mapOverlay := validMapOverlay(1280, 720)
	mapOverlay.Provider = "openstreetmap"

	if err := mapOverlay.Validate(1280, 720, []OverlayAssetRef{asset}); err == nil {
		t.Fatal("expected non-local provider to be rejected")
	}
}

func TestMapOverlayValidateRejectsPinOutsideWindow(t *testing.T) {
	asset, _ := writeMapRaster(t)
	mapOverlay := validMapOverlay(1280, 720)
	// Sydney is far outside the Rome window at zoom 6.
	mapOverlay.Pins[0].Latitude = -33.8688
	mapOverlay.Pins[0].Longitude = 151.2093

	if err := mapOverlay.Validate(1280, 720, []OverlayAssetRef{asset}); err == nil {
		t.Fatal("expected off-plate pin to be rejected")
	}
}

func TestMapOverlayValidateRejectsDuplicatePinIDs(t *testing.T) {
	asset, _ := writeMapRaster(t)
	mapOverlay := validMapOverlay(1280, 720)
	mapOverlay.Pins = append(mapOverlay.Pins, mapOverlay.Pins[0])

	if err := mapOverlay.Validate(1280, 720, []OverlayAssetRef{asset}); err == nil {
		t.Fatal("expected duplicate pin id to be rejected")
	}
}

func TestMapOverlayValidateRejectsRasterCanvasMismatch(t *testing.T) {
	asset, _ := writeMapRaster(t)
	mapOverlay := validMapOverlay(1280, 720)

	if err := mapOverlay.Validate(1920, 1080, []OverlayAssetRef{asset}); err == nil {
		t.Fatal("expected raster/canvas dimension mismatch to be rejected")
	}
}

func TestMapOverlayValidateRejectsOversizedRaster(t *testing.T) {
	asset, _ := writeMapRaster(t)
	mapOverlay := validMapOverlay(8000, 4320)

	if err := mapOverlay.Validate(8000, 4320, []OverlayAssetRef{asset}); err == nil {
		t.Fatal("expected raster beyond the published contract maximum to be rejected")
	}
}

func TestMapOverlayValidateRejectsTooManyPins(t *testing.T) {
	asset, _ := writeMapRaster(t)
	mapOverlay := validMapOverlay(1280, 720)
	pins := make([]MapOverlayPin, 0, maxMapPins+1)
	for i := 0; i <= maxMapPins; i++ {
		pins = append(pins, MapOverlayPin{
			ID: fmt.Sprintf("pin-%d", i), Label: fmt.Sprintf("Pin %d", i),
			Latitude: 41.9028, Longitude: 12.4964, Color: "#00FF00", RadiusPX: 4,
		})
	}
	mapOverlay.Pins = pins

	if err := mapOverlay.Validate(1280, 720, []OverlayAssetRef{asset}); err == nil {
		t.Fatal("expected more pins than the published contract allows to be rejected")
	}
}

// TestMapOverlayWireMatchesPublishedContract pins the producer's map wire
// field set to the published overlay-plan.v1 contract. The worker decodes a
// plan with DisallowUnknownFields and its schema forbids additional
// properties, so an extra field here — historically center.display_name,
// which the geodesy enrichment point carried — would reject the whole plan on
// the worker even though this producer considered it valid.
func TestMapOverlayWireMatchesPublishedContract(t *testing.T) {
	raw, err := json.Marshal(validMapOverlay(1280, 720))
	if err != nil {
		t.Fatalf("marshal map overlay: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal map overlay: %v", err)
	}
	assertJSONKeys(t, decoded, []string{
		"provider", "source_id", "source_license", "center", "zoom",
		"width", "height", "attribution", "motion_id", "pins",
	})
	center, ok := decoded["center"].(map[string]any)
	if !ok {
		t.Fatalf("center is not an object: %T", decoded["center"])
	}
	assertJSONKeys(t, center, []string{"latitude", "longitude"})
	pins, ok := decoded["pins"].([]any)
	if !ok || len(pins) != 1 {
		t.Fatalf("pins = %T(%v)", decoded["pins"], decoded["pins"])
	}
	pin, ok := pins[0].(map[string]any)
	if !ok {
		t.Fatalf("pin is not an object: %T", pins[0])
	}
	assertJSONKeys(t, pin, []string{"id", "label", "latitude", "longitude", "color", "radius_px"})
}

// assertJSONKeys fails unless the object carries exactly the expected keys —
// the same bidirectional comparison the worker's schema parity suite makes.
func assertJSONKeys(t *testing.T, object map[string]any, want []string) {
	t.Helper()
	expected := make(map[string]struct{}, len(want))
	for _, key := range want {
		expected[key] = struct{}{}
		if _, ok := object[key]; !ok {
			t.Errorf("missing contract field %q", key)
		}
	}
	for key := range object {
		if _, ok := expected[key]; !ok {
			t.Errorf("field %q is not part of the published contract", key)
		}
	}
}

// TestComputeRenderKeyCoversMapDeclaration certifies the map's render-key
// coverage: the key is deterministic, and every render-visible map field moves
// it so two different plates can never share a cached render.
func TestComputeRenderKeyCoversMapDeclaration(t *testing.T) {
	asset, _ := writeMapRaster(t)
	baseMap := validMapOverlay(1280, 720)
	item := OverlayItem{
		ID: "scene-1-map", Kind: "map", TemplateID: "MAP",
		StartMs: 0, EndMs: 3000, AssetRefs: []OverlayAssetRef{asset}, Map: &baseMap,
	}
	plan := OverlayPlan{Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, RendererVersion: "chronon"}

	key := ComputeRenderKey(plan, item)
	if key == "" {
		t.Fatal("map render key must be non-empty")
	}
	if again := ComputeRenderKey(plan, item); again != key {
		t.Fatalf("map render key is not deterministic: %q != %q", again, key)
	}

	mutations := map[string]func(*MapOverlay){
		"attribution": func(m *MapOverlay) { m.Attribution = "© Another contributor" },
		"zoom":        func(m *MapOverlay) { m.Zoom = 7 },
		"center":      func(m *MapOverlay) { m.Center.Latitude = 48.8566; m.Center.Longitude = 2.3522 },
		"pin label":   func(m *MapOverlay) { m.Pins[0].Label = "Roma" },
		"motion":      func(m *MapOverlay) { m.MotionID = "image_focus_reveal" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := baseMap
			mutate(&changed)
			mutated := item
			mutated.Map = &changed
			if other := ComputeRenderKey(plan, mutated); other == key {
				t.Fatalf("render key ignores the map %s", name)
			}
		})
	}
}

func TestOverlayPlanValidateAcceptsMapItemAndPopulatesRenderKey(t *testing.T) {
	asset, _ := writeMapRaster(t)
	mapOverlay := validMapOverlay(1280, 720)
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "map-plan",
		VideoID:       "video-map",
		Width:         1280,
		Height:        720,
		FPSNum:        30,
		FPSDen:        1,
		Items: []OverlayItem{{
			ID: "scene-1-map", SceneID: "scene-1", Kind: "map", TemplateID: "MAP",
			StartMs: 0, EndMs: 4000, AssetRefs: []OverlayAssetRef{asset}, Map: &mapOverlay,
		}},
	}

	if err := plan.Validate(); err != nil {
		t.Fatalf("valid map plan rejected: %v", err)
	}
	if plan.Items[0].RenderKey == "" {
		t.Fatal("map item render key was not populated")
	}
	if plan.Fingerprint == "" {
		t.Fatal("map plan fingerprint was not populated")
	}
}

func TestOverlayPlanValidateRejectsMapDeclarationOnNonMapItem(t *testing.T) {
	mapOverlay := validMapOverlay(1280, 720)
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "map-plan",
		VideoID:       "video-map",
		Width:         1280,
		Height:        720,
		FPSNum:        30,
		FPSDen:        1,
		Items: []OverlayItem{{
			ID: "phrase", TemplateID: "IMPORTANT_PHRASE", Text: "HELLO",
			StartMs: 0, EndMs: 2000, Map: &mapOverlay,
		}},
	}

	if err := plan.Validate(); err == nil {
		t.Fatal("expected a map declaration on a non-map item to be rejected")
	}
}

func TestOverlayPlanValidateRejectsCameraMapWithoutCompleteLODProvenance(t *testing.T) {
	asset, _ := writeMapRaster(t)
	fine := asset
	fine.AssetID = "map-fine"
	mapOverlay := validMapOverlay(1280, 720)
	mapOverlay.Width, mapOverlay.Height = 2048, 2048
	mapOverlay.Zoom = 4
	mapOverlay.CameraMove = &MapCameraMove{
		From: MapCenter{Latitude: 0, Longitude: 0}, To: MapCenter{Latitude: 0, Longitude: 0.2},
		StartZoom: 4, EndZoom: 6,
	}
	mapOverlay.Center = MapCenter{Latitude: 0, Longitude: 0}
	mapOverlay.Pins[0].Latitude, mapOverlay.Pins[0].Longitude = 0, 0
	mapOverlay.LODs = []MapOverlayLOD{
		{AssetID: asset.AssetID, SourceID: "coarse", SourceLicense: "operator-supplied", Attribution: mapOverlay.Attribution, Center: mapOverlay.Center, Zoom: 4, Width: 2048, Height: 2048},
		{AssetID: fine.AssetID, SourceID: "fine", SourceLicense: "operator-supplied", Attribution: mapOverlay.Attribution, Center: mapOverlay.Center, Zoom: 6, Width: 4096, Height: 4096},
	}
	// The manifest/worker API validates local PNG bytes and dimensions, so use
	// a pair of real fixture rasters matching the declared LODs.
	plateDir := t.TempDir()
	coarseAsset := writeMapRasterSized(t, plateDir, "coarse.png", 2048, 2048, "map-coarse")
	fineAsset := writeMapRasterSized(t, plateDir, "fine.png", 4096, 4096, "map-fine")
	mapOverlay.LODs[0].AssetID, mapOverlay.LODs[1].AssetID = coarseAsset.AssetID, fineAsset.AssetID
	assets := []OverlayAssetRef{coarseAsset, fineAsset}
	if err := mapOverlay.Validate(1280, 720, assets); err != nil {
		t.Fatalf("valid local camera map rejected: %v", err)
	}
	mapOverlay.LODs[1].SourceLicense = "different-license"
	if err := mapOverlay.Validate(1280, 720, assets); err == nil || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("mismatched LOD license must be rejected, got %v", err)
	}
}

func writeMapRasterSized(t *testing.T, dir, name string, width, height int, assetID string) OverlayAssetRef {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatalf("encode %s: %v", name, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return OverlayAssetRef{AssetID: assetID, SHA256: digest.SHA256Bytes(buf.Bytes()), MediaType: "image/png", LocalPath: path}
}

func TestOverlayPlanValidateRejectsMapItemWithoutDeclaration(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "map-plan",
		VideoID:       "video-map",
		Width:         1280,
		Height:        720,
		FPSNum:        30,
		FPSDen:        1,
		Items: []OverlayItem{{
			ID: "scene-1-map", Kind: "map", TemplateID: "MAP",
			StartMs: 0, EndMs: 4000,
		}},
	}

	if err := plan.Validate(); err == nil {
		t.Fatal("expected a map item without a declaration to be rejected")
	}
}
