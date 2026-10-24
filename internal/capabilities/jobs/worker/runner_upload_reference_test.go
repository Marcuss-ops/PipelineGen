// Package worker — runner_upload_reference_test.go (master-audio single-owner,
// waste-cut 2026-10-24 extension of Taglio 2b to the worker-runtime path).
//
// uploadManifest used to re-ship EVERY manifest artifact over the asset
// transport — including the certified final_audio master that publishFinalAudio
// had already uploaded to Drive and registered. A final_audio entry that carries
// the published reference (canonical audio_asset_id + drive_file_id) must not be
// stat'ed, hashed or uploaded again: its certified identity rides the
// sender-safe manifest as a ready reference.
//
// The fixtures point the referenced master at a NON-EXISTENT path: any byte
// access (stat, sha256, upload) fails the test, pinning the structural skip.
package worker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// countingAssetClient records every asset-transport upload.
type countingAssetClient struct {
	uploads []string
}

func (c *countingAssetClient) UploadFile(_ context.Context, assetID, _ string) error {
	c.uploads = append(c.uploads, assetID)
	return nil
}

func (c *countingAssetClient) Download(context.Context, string) (io.ReadCloser, string, error) {
	return nil, "", nil
}

// TestUploadManifest_ReferencedFinalAudioIsNotReshipped pins that a referenced
// master is a pure REFERENCE on the worker-runtime path: zero stat, zero hash,
// zero asset-transport upload — while the sender-safe manifest still carries it
// as a ready reference so the ToRemote required-precheck passes. The sidecar is
// uploaded normally.
func TestUploadManifest_ReferencedFinalAudioIsNotReshipped(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.json")
	if err := os.WriteFile(scriptPath, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := &scriptpkg.ArtifactManifest{
		SchemaVersion: scriptpkg.SchemaVersionArtifactManifestV1,
		JobID:         "ref-job",
		Artifacts: []scriptpkg.Artifact{
			{
				ID: "ref-job:script_json", Kind: scriptpkg.ArtifactKindScriptJSON,
				Path: scriptPath, Filename: "script.json",
				MIMEType: "application/json", SizeBytes: 11, Required: true,
			},
			{
				// The certified master reference: NON-EXISTENT local path — any
				// byte access would fail the required-artifact file check.
				ID: "asset-published", Kind: scriptpkg.ArtifactKindFinalAudio,
				Path: filepath.Join(dir, "gone", "final_audio.m4a"), Filename: "final_audio.m4a",
				MIMEType: "audio/mp4", SHA256: "sha-of-master", Required: true,
				ArtifactMetadata: map[string]any{
					"audio_asset_id": "asset-published",
					"drive_file_id":  "drive-master",
				},
			},
		},
	}

	client := &countingAssetClient{}
	r := &Runner{log: zap.NewNop(), assetClient: client}

	remote, err := r.uploadManifest(context.Background(), "ref-job", map[string]any{
		scriptpkg.ManifestKey: manifest,
	})
	if err != nil {
		t.Fatalf("uploadManifest: %v", err)
	}

	if len(client.uploads) != 1 || client.uploads[0] != "ref-job:script_json" {
		t.Fatalf("asset-transport uploads = %v, want only the sidecar (referenced master must not be re-shipped)", client.uploads)
	}
	if remote == nil {
		t.Fatal("sender-safe manifest is nil")
	}
	found := false
	for _, a := range remote.Artifacts {
		if a.ID != "asset-published" {
			continue
		}
		found = true
		if a.Status != scriptpkg.StatusReady {
			t.Errorf("master reference Status = %q, want %q", a.Status, scriptpkg.StatusReady)
		}
		if a.RemoteAssetID != "asset-published" {
			t.Errorf("master reference RemoteAssetID = %q, want the canonical asset id", a.RemoteAssetID)
		}
		if a.ArtifactMetadata["drive_file_id"] != "drive-master" {
			t.Errorf("master reference lost the drive_file_id provenance: %#v", a.ArtifactMetadata)
		}
	}
	if !found {
		t.Fatal("sender-safe manifest dropped the master reference — the ToRemote required-precheck must see it as delivered")
	}
}

// TestUploadManifest_UnreferencedFinalAudioStillFailsClosedOnMissingFile pins
// the safety net: WITHOUT the full published reference (missing drive_file_id)
// the master keeps the generic path — and a required artifact whose file is
// missing still fails closed with the file-not-found error.
func TestUploadManifest_UnreferencedFinalAudioStillFailsClosedOnMissingFile(t *testing.T) {
	dir := t.TempDir()
	for name, meta := range map[string]map[string]any{
		"no reference at all":        nil,
		"asset id only (no drive id)": {"audio_asset_id": "asset-x"},
		"reference but no digest":     {"audio_asset_id": "asset-x", "drive_file_id": "drive-x"},
	} {
		manifest := &scriptpkg.ArtifactManifest{
			SchemaVersion: scriptpkg.SchemaVersionArtifactManifestV1,
			JobID:         "safety-job",
			Artifacts: []scriptpkg.Artifact{
				{
					ID: "asset-x", Kind: scriptpkg.ArtifactKindFinalAudio,
					Path: filepath.Join(dir, "gone", "final_audio.m4a"), Filename: "final_audio.m4a",
					MIMEType: "audio/mp4", Required: true,
					ArtifactMetadata: meta,
				},
			},
		}
		if name == "reference but no digest" {
			manifest.Artifacts[0].SHA256 = ""
		}
		r := &Runner{log: zap.NewNop(), assetClient: &countingAssetClient{}}
		if _, err := r.uploadManifest(context.Background(), "safety-job", map[string]any{
			scriptpkg.ManifestKey: manifest,
		}); err == nil {
			t.Fatalf("%s: uploadManifest succeeded with a missing on-disk master — the generic path must still fail closed", name)
		} else if !containsSubstr(err.Error(), "file not found on disk") {
			t.Fatalf("%s: err = %v, want the file-not-found failure of the generic path", name, err)
		}
	}
}
