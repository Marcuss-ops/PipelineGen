// Package scriptgeneration — map_plan_e2e_test.go certifies the whole
// production path for one grounded place: annotations → per-scene candidate →
// certified plate → budgeted map overlay item inside a sealed OverlayPlan, and
// the fail-closed counterpart (no plates wired ⇒ no map).
package scriptgeneration

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

var (
	newOrleansLat = 29.9511
	newOrleansLon = -90.0715
)

// newOrleansPlate builds a certified plate exactly the size of the golden
// canvas and centred on the grounded place, backed by a real PNG on disk.
func newOrleansPlate(t *testing.T) capabilityoverlay.MapPlate {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, GoldenOverlayCanvas.Width, GoldenOverlayCanvas.Height))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	require.NoError(t, png.Encode(&buf, img))
	path := filepath.Join(t.TempDir(), "plate-new-orleans.png")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))

	return capabilityoverlay.MapPlate{
		ID:          "plate-new-orleans",
		License:     "operator-supplied",
		Attribution: "© OpenStreetMap contributors",
		Center:      capabilityoverlay.MapCenter{Latitude: newOrleansLat, Longitude: newOrleansLon},
		Zoom:        10,
		Width:       GoldenOverlayCanvas.Width,
		Height:      GoldenOverlayCanvas.Height,
		Window:      geodesy.CenteredOn(newOrleansLat, newOrleansLon, 10, GoldenOverlayCanvas.Width, GoldenOverlayCanvas.Height),
		Asset: capabilityoverlay.OverlayAssetRef{
			AssetID: "plate-new-orleans", SHA256: digest.SHA256Bytes(buf.Bytes()),
			MediaType: "image/png", LocalPath: path,
		},
	}
}

// plateStub resolves through the certified window, like the manifest-backed
// composition-root adapter does.
type plateStub struct{ plates []capabilityoverlay.MapPlate }

func (s plateStub) ResolveFlyover(from, to capabilityoverlay.MapCenter, _, _ int) (capabilityoverlay.MapPlate, bool) {
	if len(s.plates) == 0 || len(s.plates[0].LODs) == 0 {
		return capabilityoverlay.MapPlate{}, false
	}
	base := s.plates[0]
	if !base.Window.Contains(from.Latitude, from.Longitude) || !base.Window.Contains(to.Latitude, to.Longitude) {
		return capabilityoverlay.MapPlate{}, false
	}
	for _, lod := range base.LODs {
		if !lod.Window.Contains(from.Latitude, from.Longitude) || !lod.Window.Contains(to.Latitude, to.Longitude) {
			return capabilityoverlay.MapPlate{}, false
		}
	}
	return base, true
}

func newOrleansFlyoverPlates(t *testing.T) []capabilityoverlay.MapPlate {
	t.Helper()
	makePlate := func(id string, zoom, size int) capabilityoverlay.MapPlate {
		var buf bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, size, size))
		img.Set(0, 0, color.RGBA{R: 7, G: 11, B: 19, A: 255})
		require.NoError(t, png.Encode(&buf, img))
		path := filepath.Join(t.TempDir(), id+".png")
		require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
		center := capabilityoverlay.MapCenter{Latitude: newOrleansLat, Longitude: newOrleansLon}
		return capabilityoverlay.MapPlate{
			ID: id, License: "operator-supplied", Attribution: "© OpenStreetMap contributors",
			Center: center, Zoom: zoom, Width: size, Height: size,
			Window: geodesy.CenteredOn(center.Latitude, center.Longitude, zoom, size, size),
			Asset: capabilityoverlay.OverlayAssetRef{
				AssetID: id, SHA256: digest.SHA256Bytes(buf.Bytes()), MediaType: "image/png", LocalPath: path,
			},
		}
	}
	coarse := makePlate("new-orleans-z4", 4, 1024)
	fine := makePlate("new-orleans-z6", 6, 2048)
	coarse.LODs = []capabilityoverlay.MapPlate{fine}
	return []capabilityoverlay.MapPlate{coarse}
}

func (s plateStub) ResolvePlate(latitude, longitude float64) (capabilityoverlay.MapPlate, bool) {
	for _, plate := range s.plates {
		if plate.Window.Contains(latitude, longitude) {
			return plate, true
		}
	}
	return capabilityoverlay.MapPlate{}, false
}

// groundedPlaceResult is a complete three-surface fixture: certified word
// timing, a geocoded GPE annotation, and the entity timeline that grounds the
// occurrence the map is timed by.
func groundedPlaceResult() *GenerateResult {
	occurrence := groundedPlaceOccurrence()
	return &GenerateResult{
		Scenes: []Scene{{
			ID: "scene-0", Index: 0,
			Text: map[Language]string{"en": "We drove into New Orleans today."},
			Voiceover: map[Language]AudioReference{
				"en": {ID: "voiceover-0", Duration: 0.9, Timing: func() *capabilityaudio.SpeechTimingArtifact {
					timing := groundedPlaceTiming()
					return &timing
				}()},
			},
			Annotations: &scriptpkg.SceneAnnotations{
				Version: 1, Language: "en", Status: "completed",
				PrimaryEntities: []scriptpkg.AnnotatedEntity{{
					ID: "e-new-orleans", CanonicalName: "New Orleans", Type: "GPE", Confidence: 0.93,
					Mentions: []scriptpkg.AnnotationSpan{{Text: "New Orleans", StartRune: 17, EndRune: 28}},
					Geo: &scriptpkg.GeoCoordinate{
						Latitude: newOrleansLat, Longitude: newOrleansLon,
						DisplayName: "New Orleans, Louisiana, United States",
					},
				}},
			},
		}},
		SourceLanguage: "en",
		ResolvedScenes: []ResolvedScene{{ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: 900_000}},
		EntityTimeline: &capabilityentities.EntityTimeline{
			Version: capabilityentities.EntityTimelineVersion, Language: "en", DurationUS: 900_000,
			Scenes: []capabilityentities.SceneEntityTimeline{{
				SceneID: "scene-0", SceneIndex: 0, TimelineStartUS: 0,
				Entities: []capabilityentities.EntityOccurrence{occurrence},
			}},
		},
	}
}

// TestCompileOverlayPlanWithPlatesEmitsGroundedMapItem certifies the end-to-end
// map path: one grounded place yields exactly one map item whose pin joins on
// the stable entity id, whose window is the grounded occurrence, and whose
// basemap is the certified local raster with no network reference.
func TestCompileOverlayPlanWithPlatesEmitsGroundedMapItem(t *testing.T) {
	plate := newOrleansPlate(t)
	plan, err := CompileOverlayPlanWithPlates(
		groundedPlaceResult(), "en", GoldenOverlayCanvas, "map-plan", "map-video", "map-project",
		plateStub{plates: []capabilityoverlay.MapPlate{plate}},
	)
	require.NoError(t, err)
	require.NotNil(t, plan)

	var maps []capabilityoverlay.OverlayItem
	for _, item := range plan.Items {
		if item.Kind == "map" {
			maps = append(maps, item)
		}
	}
	require.Len(t, maps, 1, "one grounded place must produce exactly one map item")
	item := maps[0]
	require.Equal(t, "MAP", item.TemplateID)
	require.Equal(t, "scene-0", item.SceneID)
	require.Equal(t, int64(500), item.StartMs, "the map starts when the place is spoken")
	require.Equal(t, int64(900), item.EndMs)
	require.NotNil(t, item.Map)
	require.Equal(t, "local", item.Map.Provider)
	require.Equal(t, plate.ID, item.Map.SourceID)
	require.Equal(t, plate.Attribution, item.Map.Attribution)
	require.Equal(t, plate.Zoom, item.Map.Zoom)
	require.Len(t, item.Map.Pins, 1)
	require.Equal(t, capabilityentities.StableEntityID("GPE", "New Orleans"), item.Map.Pins[0].ID)
	require.Equal(t, "New Orleans", item.Map.Pins[0].Label)
	require.InDelta(t, newOrleansLat, item.Map.Pins[0].Latitude, 1e-9)
	require.InDelta(t, newOrleansLon, item.Map.Pins[0].Longitude, 1e-9)

	require.Len(t, item.AssetRefs, 1)
	require.Equal(t, plate.Asset.SHA256, item.AssetRefs[0].SHA256)
	require.Equal(t, plate.Asset.LocalPath, item.AssetRefs[0].LocalPath)
	require.Empty(t, item.AssetRefs[0].URL, "a local plate must never carry a network reference")
	require.Equal(t, capabilityoverlay.MaxMapOverlaysPerRun, countKind(plan.Items, "map"),
		"the certified run-level map ceiling is one")
	require.NotEmpty(t, item.RenderKey, "the sealed plan must carry a render key for the map")
}

// TestCompileOverlayPlanWithFlyoverPlatesEmitsNativeCamera certifies the
// grounded producer path from annotations and certified timing through offline
// LOD selection into camera_move. A single place is a stationary fly-to (zoom
// into the geocoded target), not a fabricated route or screen-space coordinate.
func TestCompileOverlayPlanWithFlyoverPlatesEmitsNativeCamera(t *testing.T) {
	canvas := OverlayCanvasSpec{Width: 640, Height: 360, FPSNum: 30, FPSDen: 1}
	plan, err := CompileOverlayPlanWithPlates(
		groundedPlaceResult(), "en", canvas, "map-flyover-plan", "map-flyover-video", "map-project",
		plateStub{plates: newOrleansFlyoverPlates(t)},
	)
	require.NoError(t, err)
	require.NotNil(t, plan)
	maps := make([]capabilityoverlay.OverlayItem, 0, 1)
	for _, item := range plan.Items {
		if item.Kind == "map" {
			maps = append(maps, item)
		}
	}
	require.Len(t, maps, 1)
	item := maps[0]
	require.NotNil(t, item.Map)
	require.NotNil(t, item.Map.CameraMove)
	require.Equal(t, capabilityoverlay.MapCenter{Latitude: newOrleansLat, Longitude: newOrleansLon}, item.Map.CameraMove.From)
	require.Equal(t, item.Map.CameraMove.From, item.Map.CameraMove.To)
	require.Equal(t, 4.0, item.Map.CameraMove.StartZoom)
	require.Equal(t, 6.0, item.Map.CameraMove.EndZoom)
	require.Len(t, item.Map.LODs, 2)
	require.Len(t, item.AssetRefs, 2)
	require.Equal(t, item.Map.LODs[0].AssetID, item.AssetRefs[0].AssetID)
	require.Equal(t, item.Map.LODs[1].AssetID, item.AssetRefs[1].AssetID)
	require.Empty(t, item.AssetRefs[0].URL)
	require.Empty(t, item.AssetRefs[1].URL)
	require.NoError(t, plan.Validate(), "producer must seal the camera-backed map before enqueue")
}

// TestCompileOverlayPlanWithoutPlatesEmitsNoMap certifies the fail-closed
// default: a deployment with no operator manifest emits no map at all, rather
// than reaching for a tile service or inventing a plate.
func TestCompileOverlayPlanWithoutPlatesEmitsNoMap(t *testing.T) {
	plan, err := CompileOverlayPlan(
		groundedPlaceResult(), "en", GoldenOverlayCanvas, "map-plan", "map-video", "map-project",
	)
	require.NoError(t, err)
	// The place contributes nothing else (a GPE card needs a resolved image
	// binding), so the run legitimately produces no plan at all.
	if plan != nil {
		require.Zero(t, countKind(plan.Items, "map"), "no map may be emitted without a certified plate")
	}
}

// TestCompileOverlayPlanWithPlatesIgnoresUncoveredPlace certifies that a
// resolver which covers a different geography yields no map for this place.
func TestCompileOverlayPlanWithPlatesIgnoresUncoveredPlace(t *testing.T) {
	plate := newOrleansPlate(t)
	plate.Window = geodesy.CenteredOn(48.8566, 2.3522, plate.Zoom, plate.Width, plate.Height)
	plate.Center = capabilityoverlay.MapCenter{Latitude: 48.8566, Longitude: 2.3522}

	plan, err := CompileOverlayPlanWithPlates(
		groundedPlaceResult(), "en", GoldenOverlayCanvas, "map-plan", "map-video", "map-project",
		plateStub{plates: []capabilityoverlay.MapPlate{plate}},
	)
	require.NoError(t, err)
	if plan != nil {
		require.Zero(t, countKind(plan.Items, "map"), "a place outside every certified plate must not become a map")
	}
}

func countKind(items []capabilityoverlay.OverlayItem, kind string) int {
	count := 0
	for _, item := range items {
		if item.Kind == kind {
			count++
		}
	}
	return count
}
