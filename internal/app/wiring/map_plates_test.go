package wiring

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	capgeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	platformgeocoding "github.com/Marcuss-ops/PipelineGen/internal/platform/geocoding"
)

// recordingPlateTarget captures what the wiring asked the runner to hold.
type recordingPlateTarget struct {
	resolver capabilityoverlay.PlateResolver
	geocoder capgeocoding.Geocoder
	calls    int
}

func (t *recordingPlateTarget) SetMapPlateResolver(resolver capabilityoverlay.PlateResolver) {
	t.resolver = resolver
	t.calls++
}

func (t *recordingPlateTarget) SetGeocoder(geocoder capgeocoding.Geocoder) {
	t.geocoder = geocoder
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

func TestWireScriptLocationSourcesConnectsGeocoderAndCertifiedPlates(t *testing.T) {
	geocodeRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		geocodeRequests++
		if got := r.Header.Get("User-Agent"); got != "PipelineGen wiring test" {
			t.Errorf("geocoder User-Agent = %q", got)
		}
		_, _ = w.Write([]byte(`[{"lat":"29.95","lon":"-90.07","display_name":"New Orleans"}]`))
	}))
	defer server.Close()

	dataDir := t.TempDir()
	writeWiringPlate(t, dataDir, "gulf.png", 64, 36)
	if err := os.WriteFile(filepath.Join(dataDir, "plates.json"), []byte(wiringPlateManifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	target := &recordingPlateTarget{}
	cfg := &config.Config{
		Storage: config.StorageConfig{DataDir: dataDir},
		External: config.ExternalConfig{
			GeocodingBaseURL:     server.URL,
			GeocodingUserAgent:   "PipelineGen wiring test",
			GeocodingCacheDir:    "geocoding-cache",
			MapPlateManifestPath: "plates.json",
		},
	}
	if err := wireScriptLocationSources(target, cfg, zap.NewNop()); err != nil {
		t.Fatalf("wire script location sources: %v", err)
	}
	if _, ok := target.geocoder.(*platformgeocoding.Nominatim); !ok {
		t.Fatalf("wired geocoder = %T, want *geocoding.Nominatim", target.geocoder)
	}
	if geocodeRequests != 0 {
		t.Fatalf("runtime wiring must not perform a lookup before request-level opt-in, got %d requests", geocodeRequests)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "geocoding-cache")); err != nil {
		t.Fatalf("durable geocoding cache was not created under data_dir: %v", err)
	}
	result, err := target.geocoder.Geocode(context.Background(), capgeocoding.Request{Query: "New Orleans", Language: "en"})
	if err != nil {
		t.Fatalf("geocode through wired adapter: %v", err)
	}
	if result.Latitude != 29.95 || result.Longitude != -90.07 || geocodeRequests != 1 {
		t.Fatalf("geocoded result/request count = %+v/%d", result, geocodeRequests)
	}
	cached, err := target.geocoder.Geocode(context.Background(), capgeocoding.Request{Query: "New Orleans", Language: "en"})
	if err != nil {
		t.Fatalf("read wired geocoder cache: %v", err)
	}
	if cached != result || geocodeRequests != 1 {
		t.Fatalf("cached result/request count = %+v/%d, want same result without another request", cached, geocodeRequests)
	}
	if target.resolver == nil {
		t.Fatal("the configured certified plate manifest was not wired")
	}
	if plate, ok := target.resolver.ResolvePlate(result.Latitude, result.Longitude); !ok || plate.ID != "plate-gulf" {
		t.Fatalf("wired plates did not resolve geocoded place: %+v, %v", plate, ok)
	}
}

func TestWireScriptLocationSourcesLeavesMissingSourcesFailClosed(t *testing.T) {
	target := &recordingPlateTarget{}
	if err := wireScriptLocationSources(target, &config.Config{}, zap.NewNop()); err != nil {
		t.Fatalf("empty location-source config should be a valid no-map deployment: %v", err)
	}
	if target.geocoder != nil || target.resolver != nil || target.calls != 0 {
		t.Fatalf("empty config unexpectedly wired location sources: %+v", target)
	}

	invalidGeocoder := &recordingPlateTarget{}
	if err := wireScriptLocationSources(invalidGeocoder, &config.Config{
		External: config.ExternalConfig{GeocodingBaseURL: "https://geo.example/search", GeocodingUserAgent: "  "},
	}, zap.NewNop()); err == nil {
		t.Fatal("configured geocoder without an identifying User-Agent must fail wiring")
	}
	if invalidGeocoder.geocoder != nil {
		t.Fatal("invalid geocoder configuration must not partially wire an adapter")
	}

	invalidManifest := &recordingPlateTarget{}
	if err := wireScriptLocationSources(invalidManifest, &config.Config{
		Storage:  config.StorageConfig{DataDir: t.TempDir()},
		External: config.ExternalConfig{MapPlateManifestPath: "missing-plates.json"},
	}, zap.NewNop()); err == nil {
		t.Fatal("configured but missing plate manifest must fail wiring")
	}
	if invalidManifest.resolver != nil || invalidManifest.calls != 0 {
		t.Fatal("invalid map manifest must not partially wire a resolver")
	}
}

func TestWireScriptLocationSourcesResolvesAbsoluteAndRelativePaths(t *testing.T) {
	t.Run("absolute paths", func(t *testing.T) {
		root := t.TempDir()
		cacheDir := filepath.Join(root, "cache")
		target := &recordingPlateTarget{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[{"lat":"1","lon":"2"}]`))
		}))
		defer server.Close()
		cfg := &config.Config{External: config.ExternalConfig{
			GeocodingBaseURL: server.URL, GeocodingUserAgent: "PipelineGen path test",
			GeocodingCacheDir: cacheDir,
		}}
		if err := wireScriptLocationSources(target, cfg, zap.NewNop()); err != nil {
			t.Fatalf("wire absolute cache path: %v", err)
		}
		if _, err := os.Stat(cacheDir); err != nil {
			t.Fatalf("absolute cache path was not used: %v", err)
		}
	})

	t.Run("relative manifest path under data dir", func(t *testing.T) {
		dataDir := t.TempDir()
		mapsDir := filepath.Join(dataDir, "maps")
		if err := os.MkdirAll(mapsDir, 0o700); err != nil {
			t.Fatalf("create maps directory: %v", err)
		}
		writeWiringPlate(t, mapsDir, "gulf.png", 64, 36)
		if err := os.WriteFile(filepath.Join(mapsDir, "plates.json"), []byte(wiringPlateManifest), 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		target := &recordingPlateTarget{}
		cfg := &config.Config{
			Storage:  config.StorageConfig{DataDir: dataDir},
			External: config.ExternalConfig{MapPlateManifestPath: filepath.Join("maps", "plates.json")},
		}
		if err := wireScriptLocationSources(target, cfg, zap.NewNop()); err != nil {
			t.Fatalf("wire relative manifest path: %v", err)
		}
		if _, ok := target.resolver.ResolvePlate(29.95, -90.07); !ok {
			t.Fatal("manifest relative to data_dir did not resolve its plate")
		}
	})
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
