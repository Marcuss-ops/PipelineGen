package mediaregistry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// ── Projection drift gates ────────────────────────────────────────────────
//
// The repository keeps several human-readable copies of the editorial
// catalogs and policy (ops/jobs/*.json, ops/jobs/editing_assets_policy.yaml,
// RenderingGen/assets/backgrounds/manifest.json). They are documented as
// PROJECTIONS, but nothing enforced that until now: each could silently drift
// into a second source of truth. These tests make the projection relationship
// mechanical.
//
// Paths are relative to this package directory, the working directory `go
// test` uses. The RenderingGen manifest lives in the sibling checkout of the
// same workspace, so the test skips when that checkout is absent.

const (
	projectionBGMRel        = "../../../ops/jobs/bgm_catalog.json"
	projectionSFXRel        = "../../../ops/jobs/sfx_catalog.json"
	projectionPolicyRel     = "../../../ops/jobs/editing_assets_policy.yaml"
	backgroundManifestRel   = "../../../../RenderingGen/assets/backgrounds/manifest.json"
	editorialCatalogPointer = "internal/capabilities/mediaregistry/editorial_catalog.go"
)

type audioCatalogProjection struct {
	Version      int                      `json:"version"`
	Canonical    *bool                    `json:"canonical"`
	ProjectionOf string                   `json:"projection_of"`
	Assets       map[string]projectionRow `json:"assets"`
}

type projectionRow struct {
	AssetID     string  `json:"asset_id"`
	DriveFileID string  `json:"drive_file_id"`
	URL         string  `json:"url"`
	DriveLink   string  `json:"drive_link"`
	Name        *string `json:"name"`
	Filename    *string `json:"filename"`
}

func TestAudioCatalogProjectionsMatchTheRegistry(t *testing.T) {
	catalog := EditorialAudioAssets()
	for _, tc := range []struct {
		name            string
		path            string
		family          string
		requireMetadata bool
	}{
		{name: "bgm_catalog.json", path: projectionBGMRel, family: "music", requireMetadata: true},
		{name: "sfx_catalog.json", path: projectionSFXRel, family: "transition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Clean(tc.path))
			if err != nil {
				t.Fatalf("read %s: %v", tc.path, err)
			}
			var projection audioCatalogProjection
			if err := json.Unmarshal(data, &projection); err != nil {
				t.Fatalf("%s is not valid json: %v", tc.path, err)
			}
			if projection.Canonical == nil || *projection.Canonical {
				t.Fatalf("%s must declare \"canonical\": false (the Go catalog is the SSOT)", tc.path)
			}
			if projection.ProjectionOf != editorialCatalogPointer {
				t.Fatalf("%s projection_of = %q, want %q", tc.path, projection.ProjectionOf, editorialCatalogPointer)
			}

			expected := map[string]EditorialAudioAsset{}
			for _, asset := range catalog {
				if asset.Family == tc.family {
					expected[asset.Alias] = asset
				}
			}
			if len(expected) == 0 {
				t.Fatalf("no catalog asset has family %q; the projection groups have drifted", tc.family)
			}
			if len(projection.Assets) != len(expected) {
				t.Fatalf("%s declares %d assets, catalog binds %d for family %q", tc.path, len(projection.Assets), len(expected), tc.family)
			}
			for alias, asset := range expected {
				row, ok := projection.Assets[alias]
				if !ok {
					t.Errorf("%s is missing catalog alias %q", tc.path, alias)
					continue
				}
				if row.AssetID != alias {
					t.Errorf("%s[%s].asset_id = %q, want %q", tc.path, alias, row.AssetID, alias)
				}
				if row.DriveFileID != asset.DriveFileID {
					t.Errorf("%s[%s].drive_file_id = %q, want %q", tc.path, alias, row.DriveFileID, asset.DriveFileID)
				}
				if row.URL != "velox-drive://"+asset.DriveFileID {
					t.Errorf("%s[%s].url = %q, want velox-drive://%s", tc.path, alias, row.URL, asset.DriveFileID)
				}
				if row.DriveLink != "https://drive.google.com/file/d/"+asset.DriveFileID+"/view?usp=drive_link" {
					t.Errorf("%s[%s].drive_link = %q", tc.path, alias, row.DriveLink)
				}
				if row.Name != nil && *row.Name != asset.Name {
					t.Errorf("%s[%s].name = %q, want %q", tc.path, alias, *row.Name, asset.Name)
				}
				if row.Filename != nil && *row.Filename != asset.Filename {
					t.Errorf("%s[%s].filename = %q, want %q", tc.path, alias, *row.Filename, asset.Filename)
				}
				if tc.requireMetadata && (row.Name == nil || row.Filename == nil) {
					t.Errorf("%s[%s] must project name and filename", tc.path, alias)
				}
			}
		})
	}
}

func TestEditingAssetsPolicyProjectionMatchesTheCanonicalPolicy(t *testing.T) {
	data, err := os.ReadFile(filepath.Clean(projectionPolicyRel))
	if err != nil {
		t.Fatalf("read %s: %v", projectionPolicyRel, err)
	}
	got, err := ParseEditingAssetsPolicy(data)
	if err != nil {
		t.Fatalf("parse %s: %v", projectionPolicyRel, err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("%s does not validate: %v", projectionPolicyRel, err)
	}
	want := DefaultEditingAssetsPolicy()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s has drifted from DefaultEditingAssetsPolicy:\n got: %+v\nwant: %+v", projectionPolicyRel, got, want)
	}
}

type backgroundManifestProjection struct {
	Kind     string `json:"kind"`
	Contract string `json:"contract"`
	Canvas   struct {
		Width  int `json:"width"`
		Height int `json:"height"`
		FPS    int `json:"fps"`
	} `json:"canvas"`
	DurationSeconds int `json:"duration_seconds"`
	AudioStreams    int `json:"audio_streams"`
	Assets          []struct {
		ID                string `json:"id"`
		File              string `json:"file"`
		SHA256            string `json:"sha256"`
		SourceDriveFileID string `json:"source_drive_file_id"`
		MediaType         string `json:"media_type"`
		Role              string `json:"role"`
	} `json:"assets"`
}

// TestBackgroundManifestIsAProjectionOfTheRegistry closes the cross-repo
// duplication: RenderingGen's background manifest and PipelineGen's registry
// describe the same six plates, and nothing compared them before.
func TestBackgroundManifestIsAProjectionOfTheRegistry(t *testing.T) {
	data, err := os.ReadFile(filepath.Clean(backgroundManifestRel))
	if err != nil {
		t.Skipf("sibling RenderingGen background manifest unavailable: %v", err)
	}
	var manifest backgroundManifestProjection
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("background manifest is not valid json: %v", err)
	}
	if manifest.Kind != "renderinggen.background-assets" {
		t.Errorf("manifest kind = %q", manifest.Kind)
	}
	if manifest.Contract != EditorialBackgroundContract {
		t.Errorf("manifest contract = %q, want %q", manifest.Contract, EditorialBackgroundContract)
	}
	if manifest.Canvas.Width != EditorialBackgroundWidth || manifest.Canvas.Height != EditorialBackgroundHeight || manifest.Canvas.FPS != EditorialBackgroundFPS {
		t.Errorf("manifest canvas = %dx%d@%d, registry = %dx%d@%d",
			manifest.Canvas.Width, manifest.Canvas.Height, manifest.Canvas.FPS,
			EditorialBackgroundWidth, EditorialBackgroundHeight, EditorialBackgroundFPS)
	}
	if manifest.DurationSeconds != EditorialBackgroundDurationSecs {
		t.Errorf("manifest duration = %d, registry = %d", manifest.DurationSeconds, EditorialBackgroundDurationSecs)
	}
	if manifest.AudioStreams != EditorialBackgroundAudioStreams {
		t.Errorf("manifest audio_streams = %d, registry = %d", manifest.AudioStreams, EditorialBackgroundAudioStreams)
	}

	registry := map[string]EditorialBackgroundAsset{}
	for _, asset := range EditorialBackgroundAssets() {
		registry[asset.ID] = asset
	}
	if len(manifest.Assets) != len(registry) {
		t.Fatalf("manifest declares %d plates, registry binds %d", len(manifest.Assets), len(registry))
	}
	seen := map[string]bool{}
	for _, plate := range manifest.Assets {
		asset, ok := registry[plate.ID]
		if !ok {
			t.Errorf("manifest declares %q, which the registry does not bind", plate.ID)
			continue
		}
		seen[plate.ID] = true
		if plate.File != asset.Filename {
			t.Errorf("%s file = %q, registry filename = %q", plate.ID, plate.File, asset.Filename)
		}
		if plate.SHA256 != asset.SHA256 {
			t.Errorf("%s sha256 = %q, registry = %q", plate.ID, plate.SHA256, asset.SHA256)
		}
		if plate.SourceDriveFileID != asset.DriveFileID {
			t.Errorf("%s source_drive_file_id = %q, registry = %q", plate.ID, plate.SourceDriveFileID, asset.DriveFileID)
		}
		if plate.MediaType != asset.MediaType {
			t.Errorf("%s media_type = %q, registry = %q", plate.ID, plate.MediaType, asset.MediaType)
		}
		if plate.Role != asset.Role {
			t.Errorf("%s role = %q, registry = %q", plate.ID, plate.Role, asset.Role)
		}
	}
	for id := range registry {
		if !seen[id] {
			t.Errorf("registry binds %q but the manifest omits it", id)
		}
	}
}
