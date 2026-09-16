package media

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// TestEditorialAssetCommitRequestDerivesEveryFieldFromTheRegistry pins the
// single mapping the plate bootstrap owns: the alias is the asset id, the
// certified registry hash is the content hash, and the Drive identity comes
// from the catalog — so a registered plate can never disagree with
// mediaregistry.ValidateEditorialBackgroundIdentity (the read-boundary gate
// clip.render fails closed on).
func TestEditorialAssetCommitRequestDerivesEveryFieldFromTheRegistry(t *testing.T) {
	plate, ok := mediaregistry.LookupEditorialBackground("drive-background-03")
	if !ok {
		t.Fatal("drive-background-03 is missing from the editorial registry")
	}

	req := editorialAssetCommitRequest(plate, "/fixtures/drive-background-03.mp4")

	if req.AssetID != plate.ID {
		t.Errorf("AssetID = %q, want the registry alias %q", req.AssetID, plate.ID)
	}
	if req.ContentHash != plate.SHA256 {
		t.Errorf("ContentHash = %q, want the certified registry hash %q", req.ContentHash, plate.SHA256)
	}
	if req.Source != EditorialAssetSource {
		t.Errorf("Source = %q, want %q", req.Source, EditorialAssetSource)
	}
	if req.MediaType != "video" {
		t.Errorf("MediaType = %q, want video", req.MediaType)
	}
	if req.LifecycleState != "ACTIVE" {
		t.Errorf("LifecycleState = %q, want ACTIVE", req.LifecycleState)
	}
	if req.Filename != plate.Filename {
		t.Errorf("Filename = %q, want %q", req.Filename, plate.Filename)
	}
	if req.LocalPath != "/fixtures/drive-background-03.mp4" {
		t.Errorf("LocalPath = %q", req.LocalPath)
	}
	if req.DurationMs != int64(mediaregistry.EditorialBackgroundDurationSecs)*1000 {
		t.Errorf("DurationMs = %d, want the registry contract duration", req.DurationMs)
	}
	// A plate is a rendering input reached by alias, never a searchable corpus
	// asset: registering one must not enqueue vector indexing work.
	if req.EmitIndexEvent {
		t.Error("EmitIndexEvent must stay false for an editorial plate")
	}
	if len(req.Locations) != 1 {
		t.Fatalf("Locations = %d, want exactly one Drive location", len(req.Locations))
	}
	if got := req.Locations[0].ExternalID; got != plate.DriveFileID {
		t.Errorf("location ExternalID = %q, want the registry Drive identity %q", got, plate.DriveFileID)
	}
	if !req.Locations[0].IsPrimary {
		t.Error("the Drive location must be primary")
	}
}

// TestEditorialAssetCommitRequestToleratesAProbeFailure documents the
// supported "no local fixture" shape: the plate is registered without a local
// path and the canonical materializer fetches the certified bytes from Drive.
func TestEditorialAssetCommitRequestHasNoLocalPathWithoutAFixture(t *testing.T) {
	plate, ok := mediaregistry.LookupEditorialBackground("drive-background-06")
	if !ok {
		t.Fatal("drive-background-06 is missing from the editorial registry")
	}
	if req := editorialAssetCommitRequest(plate, ""); req.LocalPath != "" {
		t.Errorf("LocalPath = %q, want empty", req.LocalPath)
	}
}

// TestResolvePlateFixtureIgnoresMissingAndDirectoryEntries keeps the bootstrap
// from registering a path that cannot be hashed: only a real file is adopted.
func TestResolvePlateFixtureIgnoresMissingAndDirectoryEntries(t *testing.T) {
	dir := t.TempDir()
	if got := resolvePlateFixture(dir, "absent.mp4"); got != "" {
		t.Errorf("missing fixture resolved to %q, want empty", got)
	}
	if got := resolvePlateFixture("", "any.mp4"); got != "" {
		t.Errorf("empty dir resolved to %q, want empty", got)
	}
	if got := resolvePlateFixture(dir, "."); got != "" {
		t.Errorf("a directory resolved to %q, want empty", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "plate.mp4"), []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got := resolvePlateFixture(dir, "plate.mp4")
	if !filepath.IsAbs(got) {
		t.Errorf("existing fixture resolved to %q, want an absolute path", got)
	}
}
