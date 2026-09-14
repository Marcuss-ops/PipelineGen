package stockpipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	capfinalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// TestStockPublishStep_SkipMetadataUpload_PublishesOnlyVideos pins the
// RuntimeConfig.SkipMetadataUpload contract: an operator who does not want
// metadata.json in their Drive clips folder gets ONLY the chunk videos
// prepared, while the metadata content is still composed + hashed so the
// finalization contract keeps its content identity.
func TestStockPublishStep_SkipMetadataUpload_PublishesOnlyVideos(t *testing.T) {
	tmpDir := t.TempDir()
	clip0 := filepath.Join(tmpDir, "clip0.mp4")
	clip1 := filepath.Join(tmpDir, "clip1.mp4")
	if err := os.WriteFile(clip0, []byte("clip-0"), 0o644); err != nil {
		t.Fatalf("write clip0: %v", err)
	}
	if err := os.WriteFile(clip1, []byte("clip-1"), 0o644); err != nil {
		t.Fatalf("write clip1: %v", err)
	}

	prep := &recordingArtifactPreparation{}
	runner := &publishFakeRunner{
		runInput: &RunInput{
			FolderName:         "mike tyson",
			FolderID:           "wf-skip",
			ClipDuration:       5,
			ChunkDuration:      5,
			NoEffects:          true,
			NoTransitions:      true,
			SkipMetadataUpload: true,
		},
		cfg: OrchestratorConfig{PolicyVersion: "policy-v1"},
		state: &RunState{
			Plan: []ClipPlan{
				{SourceID: "https://youtu.be/a", StartSec: 0, EndSec: 5},
				{SourceID: "https://youtu.be/a", StartSec: 5, EndSec: 10},
			},
			ComposedPaths: []string{clip0, clip1},
		},
		artifactPrep: prep,
	}

	if err := (StockPublishStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockPublishStep.Run() unexpected error: %v", err)
	}

	// Two videos, zero metadata.json prepare calls (run-level AND per-chunk
	// metadata are both suppressed).
	if got, want := len(prep.artifacts), 2; got != want {
		t.Fatalf("expected %d prepare calls (2 videos, no metadata.json), got %d", want, got)
	}
	for i, a := range prep.artifacts {
		if a.Filename == "metadata.json" {
			t.Fatalf("artifact[%d] is a metadata.json — SkipMetadataUpload must suppress it", i)
		}
	}

	md := runner.State().MetadataPublished
	if !md.DriveUploadSkipped {
		t.Fatal("MetadataPublished.DriveUploadSkipped = false, want true")
	}
	if md.RemoteFileID != "" {
		t.Fatalf("MetadataPublished.RemoteFileID = %q, want empty", md.RemoteFileID)
	}
	if md.SHA256 == "" {
		t.Fatal("MetadataPublished.SHA256 must still be computed in skip mode")
	}
	if err := VerifyMetadata(md); err != nil {
		t.Fatalf("VerifyMetadata(skip-mode metadata) = %v, want nil", err)
	}
}

// TestVerifyMetadata_SkipModeAllowsMissingRemoteFileID guards the gate
// relaxation: RemoteFileID stays mandatory for a normal run (a missing
// location is a publish failure) but is optional when the run deliberately
// opted out of the Drive publication.
func TestVerifyMetadata_SkipModeAllowsMissingRemoteFileID(t *testing.T) {
	sha := "2036e99215c101f82679172d685b46cbe42d8f0abe153b46806bce11c12371ff"

	if err := VerifyMetadata(MetadataState{LocalPath: "/tmp/metadata.json", SHA256: sha}); err == nil {
		t.Fatal("VerifyMetadata without RemoteFileID in publish mode = nil, want ErrStockMetadataNotPublished")
	}
	skipped := MetadataState{LocalPath: "/tmp/metadata.json", SHA256: sha, DriveUploadSkipped: true}
	if err := VerifyMetadata(skipped); err != nil {
		t.Fatalf("VerifyMetadata(skip mode) = %v, want nil", err)
	}
}

// TestStockFinalizeAdapter_SkipModeSurvivesRoundTrip pins the adapter
// round-trip contract that the production FAILED run exposed: the step
// threads MetadataState into finalize.Request and the adapter rebuilds a
// MetadataState from it before re-running the VerifyMetadata gate. If
// DriveUploadSkipped is not part of the neutral finalize.Metadata contract,
// the flag is lost and the fail-closed gate rejects the run.
func TestStockFinalizeAdapter_SkipModeSurvivesRoundTrip(t *testing.T) {
	sha := "2036e99215c101f82679172d685b46cbe42d8f0abe153b46806bce11c12371ff"
	req := newStockFinalizeRequest(
		"job-1",
		capfinalization.Lease{JobID: "job-1", WorkerID: "w-1", LeaseID: "l-1"},
		[]byte("{}"),
		nil,
		MetadataState{LocalPath: "/tmp/metadata.json", SHA256: sha, DriveUploadSkipped: true},
		"fp-1",
	)

	back := toLegacyMetadata(req.Metadata)
	if !back.DriveUploadSkipped {
		t.Fatal("toLegacyMetadata lost DriveUploadSkipped across the adapter round-trip")
	}
	if err := VerifyMetadata(back); err != nil {
		t.Fatalf("VerifyMetadata(after round-trip) = %v, want nil", err)
	}
}
