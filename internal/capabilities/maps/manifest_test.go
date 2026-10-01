package maps

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// writePlate writes a real PNG of the given size and returns its absolute path.
func writePlate(t *testing.T, dir, name string, width, height int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatalf("encode plate: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write plate: %v", err)
	}
	return path
}

// writeManifestFile writes body to dir/name. Manifests must sit BESIDE the
// rasters they reference: a plate's file is resolved relative to the manifest's
// own directory.
func writeManifestFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func plateEntry(id, file, license, attribution string, lat, lon float64, zoom, width, height int) string {
	return fmt.Sprintf(
		`{"id":%q,"file":%q,"license":%q,"attribution":%q,"center":{"latitude":%v,"longitude":%v},"zoom":%d,"width":%d,"height":%d}`,
		id, file, license, attribution, lat, lon, zoom, width, height,
	)
}

func TestLoadManifestCertifiesOperatorPlate(t *testing.T) {
	dir := t.TempDir()
	raster := writePlate(t, dir, "gulf.png", 64, 36)
	path := writeManifestFile(t, dir, "plates.json", `{"version":1,"plates":[`+
		plateEntry("gulf_corridor", "gulf.png", "operator-supplied", "© OpenStreetMap contributors", 29.86, -92.70, 8, 64, 36)+`]}`)

	manifest, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	plates := manifest.Plates()
	if len(plates) != 1 {
		t.Fatalf("certified plates = %d, want 1", len(plates))
	}
	plate := plates[0]
	if plate.Path() != raster {
		t.Errorf("plate path = %q, want %q", plate.Path(), raster)
	}
	wantHash, _, err := digest.SHA256File(raster)
	if err != nil {
		t.Fatalf("digest raster: %v", err)
	}
	if plate.SHA256() != wantHash || len(plate.SHA256()) != 64 {
		t.Errorf("plate sha256 = %q, want the raster's content address %q", plate.SHA256(), wantHash)
	}
	lat, lon := plate.Window().CenterLatLon()
	if diff := lat - 29.86; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("window center latitude = %v, want 29.86", lat)
	}
	if diff := lon - (-92.70); diff > 1e-6 || diff < -1e-6 {
		t.Errorf("window center longitude = %v, want -92.70", lon)
	}
	if !plate.Window().Contains(29.86, -92.70) {
		t.Error("the certified window must contain the plate's own center")
	}
}

func TestResolvePicksMostDetailedCoveringPlate(t *testing.T) {
	dir := t.TempDir()
	writePlate(t, dir, "coarse.png", 64, 36)
	writePlate(t, dir, "fine.png", 64, 36)
	path := writeManifestFile(t, dir, "plates.json", `{"version":1,"plates":[`+
		plateEntry("coarse", "coarse.png", "op", "© A", 29.86, -92.70, 6, 64, 36)+`,`+
		plateEntry("fine", "fine.png", "op", "© B", 29.86, -92.70, 10, 64, 36)+`]}`)

	manifest, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	plate, ok := manifest.Resolve(29.86, -92.70)
	if !ok {
		t.Fatal("the Gulf-coast point must resolve to a certified plate")
	}
	if plate.ID != "fine" {
		t.Errorf("resolved plate = %q, want the highest-zoom raster %q", plate.ID, "fine")
	}
	if _, ok := manifest.Resolve(41.9028, 12.4964); ok {
		t.Error("a point outside every plate window must not resolve (no fabricated map)")
	}
}

func TestResolveFlyoverRequiresEveryZoomToCoverRouteAndViewport(t *testing.T) {
	dir := t.TempDir()
	writePlate(t, dir, "coarse.png", 1024, 1024)
	writePlate(t, dir, "fine.png", 2048, 2048)
	path := writeManifestFile(t, dir, "flyover.json", `{"version":1,"plates":[`+
		plateEntry("coarse", "coarse.png", "operator-supplied", "© OpenStreetMap contributors", 0, 0, 4, 1024, 1024)+`,`+
		plateEntry("fine", "fine.png", "operator-supplied", "© OpenStreetMap contributors", 0, 0, 6, 2048, 2048)+`]}`)
	manifest, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("valid flyover manifest rejected: %v", err)
	}

	plates, ok := manifest.ResolveFlyover(0, 0, 0, 0.2, 640, 360)
	if !ok {
		t.Fatal("certified zoom 4→6 plates should cover the grounded route and viewport")
	}
	if len(plates) != 2 || plates[0].ID != "coarse" || plates[1].ID != "fine" {
		t.Fatalf("flyover LODs = %+v, want deterministic coarse→fine order", plates)
	}
	if _, ok := manifest.ResolveFlyover(0, 0, 80, 80, 640, 360); ok {
		t.Fatal("a route outside certified windows must not resolve to a flyover")
	}
	if _, ok := manifest.ResolveFlyover(0, 0, 0, 0.2, 0, 360); ok {
		t.Fatal("invalid canvas dimensions must not resolve to a flyover")
	}
}

func TestLoadManifestFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		raster bool // write a 64x36 png named plate.png
		entry  string
	}{
		{"missing raster", false, plateEntry("p", "plate.png", "op", "© A", 29.86, -92.70, 8, 64, 36)},
		{"declared size disagrees with the raster", true, plateEntry("p", "plate.png", "op", "© A", 29.86, -92.70, 8, 32, 32)},
		{"absolute raster path", false, plateEntry("p", "/etc/hosts", "op", "© A", 29.86, -92.70, 8, 64, 36)},
		{"escaping raster path", false, plateEntry("p", "../outside.png", "op", "© A", 29.86, -92.70, 8, 64, 36)},
		{"zoom out of range", true, plateEntry("p", "plate.png", "op", "© A", 29.86, -92.70, 30, 64, 36)},
		{"invalid center", false, plateEntry("p", "plate.png", "op", "© A", 999, -92.70, 8, 64, 36)},
		{"blank license", true, plateEntry("p", "plate.png", "   ", "© A", 29.86, -92.70, 8, 64, 36)},
		{"blank attribution", true, plateEntry("p", "plate.png", "op", "  ", 29.86, -92.70, 8, 64, 36)},
		{"blank id", true, plateEntry("   ", "plate.png", "op", "© A", 29.86, -92.70, 8, 64, 36)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.raster {
				writePlate(t, dir, "plate.png", 64, 36)
			}
			path := writeManifestFile(t, dir, "plates.json", `{"version":1,"plates":[`+tc.entry+`]}`)
			if _, err := LoadManifest(path); err == nil {
				t.Fatal("expected the manifest to fail closed")
			}
		})
	}
}

func TestLoadManifestFailsClosedForNonPNGRaster(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plate.png"), []byte("not a png"), 0o600); err != nil {
		t.Fatalf("write raster: %v", err)
	}
	path := writeManifestFile(t, dir, "plates.json", `{"version":1,"plates":[`+
		plateEntry("p", "plate.png", "op", "© A", 29.86, -92.70, 8, 64, 36)+`]}`)
	if _, err := LoadManifest(path); err == nil {
		t.Fatal("a non-PNG raster must fail closed")
	}
}

func TestLoadManifestFailsClosedForDuplicateIDsAndUnknownFields(t *testing.T) {
	dir := t.TempDir()
	writePlate(t, dir, "plate.png", 64, 36)
	entry := plateEntry("dup", "plate.png", "op", "© A", 29.86, -92.70, 8, 64, 36)

	duplicate := writeManifestFile(t, dir, "duplicate.json", `{"version":1,"plates":[`+entry+`,`+entry+`]}`)
	if _, err := LoadManifest(duplicate); err == nil {
		t.Fatal("duplicate plate ids must fail closed")
	}

	unknown := writeManifestFile(t, dir, "unknown.json", `{"version":1,"plates":[`+
		strings.TrimSuffix(entry, "}")+`,"provider":"osm"}]}`)
	if _, err := LoadManifest(unknown); err == nil {
		t.Fatal("an unknown plate field must fail closed (strict decode)")
	}

	badVersion := writeManifestFile(t, dir, "version.json", `{"version":2,"plates":[]}`)
	if _, err := LoadManifest(badVersion); err == nil {
		t.Fatal("an unsupported manifest version must fail closed")
	}
}

func TestLoadManifestRejectsMissingFile(t *testing.T) {
	if _, err := LoadManifest(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("a missing manifest must fail closed")
	}
}
