package wiring

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// recordingPlateTarget captures what the wiring asked the runner to hold.
type recordingPlateTarget struct {
	resolver capabilityoverlay.PlateResolver
	calls    int
}

func (t *recordingPlateTarget) SetMapPlateResolver(resolver capabilityoverlay.PlateResolver) {
	t.resolver = resolver
	t.calls++
}

// writeWiringPlate writes a real PNG of the given size and returns its name.
func writeWiringPlate(t *testing.T, dir, name string, width, height int) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatalf("encode plate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write plate: %v", err)
	}
}

const wiringPlateManifest = `{"version":1,"plates":[{"id":"plate-gulf","file":"gulf.png",` +
	`"license":"operator-supplied","attribution":"© OpenStreetMap contributors",` +
	`"center":{"latitude":29.95,"longitude":-90.07},"zoom":8,"width":64,"height":36}]}`

const wiringFlyoverManifest = `{"version":1,"plates":[` +
	`{"id":"coarse","file":"coarse.png","license":"operator-supplied","attribution":"© OpenStreetMap contributors","center":{"latitude":0,"longitude":0},"zoom":4,"width":1024,"height":1024},` +
	`{"id":"fine","file":"fine.png","license":"operator-supplied","attribution":"© OpenStreetMap contributors","center":{"latitude":0,"longitude":0},"zoom":6,"width":2048,"height":2048}]}`

// TestWireMapPlatesResolvesCertifiedPlate certifies the composition root: a
// configured, certifiable manifest becomes a resolver whose plate carries the
// georeference, the provenance and a content-addressed local raster with no
// network reference — the only shape the planner accepts.
func TestWireMapPlatesResolvesCertifiedPlate(t *testing.T) {
	dir := t.TempDir()
	writeWiringPlate(t, dir, "gulf.png", 64, 36)
	manifestPath := filepath.Join(dir, "plates.json")
	if err := os.WriteFile(manifestPath, []byte(wiringPlateManifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	target := &recordingPlateTarget{}
	if err := wireMapPlates(target, manifestPath, zap.NewNop()); err != nil {
		t.Fatalf("wire map plates: %v", err)
	}
	if target.calls != 1 {
		t.Fatalf("resolver set %d times, want exactly once", target.calls)
	}
	resolver := target.resolver
	if resolver == nil {
		t.Fatal("a configured manifest must wire a resolver")
	}
	plate, ok := resolver.ResolvePlate(29.95, -90.07)
	if !ok {
		t.Fatal("certified plate did not resolve its own centre")
	}
	if plate.ID != "plate-gulf" || plate.Width != 64 || plate.Height != 36 || plate.Zoom != 8 {
		t.Fatalf("resolved plate = %+v, want the certified manifest entry", plate)
	}
	if plate.Attribution != "© OpenStreetMap contributors" || plate.License != "operator-supplied" {
		t.Fatalf("resolved provenance = %q/%q", plate.Attribution, plate.License)
	}
	if plate.Asset.LocalPath == "" || len(plate.Asset.SHA256) != 64 || plate.Asset.MediaType != "image/png" {
		t.Fatalf("resolved asset = %+v, want a content-addressed local PNG", plate.Asset)
	}
	if plate.Asset.URL != "" {
		t.Fatalf("resolved asset must not carry a network reference, got %q", plate.Asset.URL)
	}
	if !plate.Window.Contains(29.95, -90.07) {
		t.Fatal("resolved window does not contain the plate centre")
	}
	if _, ok := resolver.ResolvePlate(-33.8688, 151.2093); ok {
		t.Fatal("Sydney must not resolve to a Gulf plate")
	}
}

func TestWireMapPlatesResolvesOfflineFlyoverLODs(t *testing.T) {
	dir := t.TempDir()
	writeWiringPlate(t, dir, "coarse.png", 1024, 1024)
	writeWiringPlate(t, dir, "fine.png", 2048, 2048)
	manifestPath := filepath.Join(dir, "flyover.json")
	if err := os.WriteFile(manifestPath, []byte(wiringFlyoverManifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	target := &recordingPlateTarget{}
	if err := wireMapPlates(target, manifestPath, zap.NewNop()); err != nil {
		t.Fatalf("wire flyover manifest: %v", err)
	}
	resolver, ok := target.resolver.(capabilityoverlay.FlyoverPlateResolver)
	if !ok {
		t.Fatal("wired resolver does not expose the flyover port")
	}
	plate, ok := resolver.ResolveFlyover(
		capabilityoverlay.MapCenter{Latitude: 0, Longitude: 0},
		capabilityoverlay.MapCenter{Latitude: 0, Longitude: 0.2}, 640, 360,
	)
	if !ok {
		t.Fatal("certified route should resolve a camera-backed local plate")
	}
	if plate.ID != "coarse" || len(plate.LODs) != 1 || plate.LODs[0].ID != "fine" {
		t.Fatalf("resolved flyover plate = %+v, want coarse base and fine LOD", plate)
	}
	if plate.Asset.LocalPath == "" || plate.LODs[0].Asset.LocalPath == "" || plate.Asset.URL != "" || plate.LODs[0].Asset.URL != "" {
		t.Fatalf("flyover assets must be content-addressed local files: %+v", plate)
	}
	if plate.LODs[0].License != plate.License || plate.LODs[0].Attribution != plate.Attribution {
		t.Fatalf("flyover LOD provenance differs from its base: %+v", plate.LODs[0])
	}
}

// TestWireMapPlatesFailsClosed certifies the two failure boundaries: an
// unconfigured path is a valid no-map deployment, while a configured path that
// cannot be certified is a hard error — never a run that silently drops maps.
func TestWireMapPlatesFailsClosed(t *testing.T) {
	unconfigured := &recordingPlateTarget{}
	if err := wireMapPlates(unconfigured, "   ", zap.NewNop()); err != nil {
		t.Fatalf("an unconfigured manifest must be a valid no-op: %v", err)
	}
	if unconfigured.calls != 0 || unconfigured.resolver != nil {
		t.Fatal("an unconfigured manifest must not wire a resolver")
	}

	dir := t.TempDir()
	writeWiringPlate(t, dir, "gulf.png", 64, 36)
	// The declared raster size disagrees with the bytes on disk.
	mismatched := filepath.Join(dir, "mismatch.json")
	if err := os.WriteFile(mismatched, []byte(`{"version":1,"plates":[{"id":"plate-gulf","file":"gulf.png",`+
		`"license":"operator-supplied","attribution":"© OpenStreetMap contributors",`+
		`"center":{"latitude":29.95,"longitude":-90.07},"zoom":8,"width":128,"height":72}]}`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	other := &recordingPlateTarget{}
	if err := wireMapPlates(other, mismatched, zap.NewNop()); err == nil {
		t.Fatal("an uncertifiable manifest must fail wiring, not silently disable maps")
	}
	if other.calls != 0 || other.resolver != nil {
		t.Fatal("a failed wiring must not leave a resolver behind")
	}
}
