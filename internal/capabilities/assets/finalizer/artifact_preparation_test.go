package finalizer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// stubPublisherPort is a deterministic finalization.PublisherPort stub.
type stubPublisherPort struct {
	location finalization.AssetLocation
	err      error
	got      finalization.VerifiedArtifact
}

func (s *stubPublisherPort) Publish(_ context.Context, a finalization.VerifiedArtifact) (finalization.AssetLocation, error) {
	s.got = a
	return s.location, s.err
}

var _ finalization.PublisherPort = (*stubPublisherPort)(nil)

// writeVerifiedArtifact writes a temp file and returns a VerifiedArtifact
// whose LocalPath/SHA256/SizeBytes match the on-disk bytes (passes the
// on-disk verification gate in ArtifactPreparation.validate).
func writeVerifiedArtifact(t *testing.T, content string, extra map[string]any) finalization.VerifiedArtifact {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overlay.mov")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	sha := hex.EncodeToString(sum[:])
	meta := map[string]any{"source": "chronon", "video_id": "video-1"}
	for k, v := range extra {
		meta[k] = v
	}
	return finalization.VerifiedArtifact{
		ArtifactID:       "job:overlay:001",
		Kind:             finalization.KindVideo,
		Filename:         "overlay.mov",
		LocalPath:        path,
		MIMEType:         "video/quicktime",
		SizeBytes:        int64(len(content)),
		SHA256:           sha,
		SourceVersion:    1,
		Requirement:      finalization.ArtifactRequirementRequired,
		IdempotencyKey:   "idem-overlay-001",
		Source:           "chronon",
		ArtifactMetadata: meta,
	}
}

// TestArtifactPreparation_StampsDriveIdentity pins the post-publish
// enrichment: after the publisher returns the AssetLocation, the published
// artifact's metadata must carry drive_file_id + drive_link (and
// download_link) from Location.FileID / WebViewLink, preserving the input
// metadata untouched.
func TestArtifactPreparation_StampsDriveIdentity(t *testing.T) {
	stub := &stubPublisherPort{location: finalization.AssetLocation{
		Provider:     "drive",
		FileID:       "drive-file-1",
		WebViewLink:  "https://drive.google.com/file/d/drive-file-1/view",
		DownloadLink: "https://drive.google.com/uc?id=drive-file-1",
		FolderID:     "folder-overlay",
		FolderPath:   "/video/847/overlay",
		Action:       finalization.PublishCreated,
	}}
	prep := NewArtifactPreparation(stub, nil)

	published, err := prep.Prepare(context.Background(), writeVerifiedArtifact(t, "chronon overlay bytes", nil))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if got := published.ArtifactMetadata["drive_file_id"]; got != "drive-file-1" {
		t.Errorf("drive_file_id = %v, want drive-file-1", got)
	}
	if got := published.ArtifactMetadata["drive_link"]; got != "https://drive.google.com/file/d/drive-file-1/view" {
		t.Errorf("drive_link = %v, want the Drive web-view link", got)
	}
	if got := published.ArtifactMetadata["download_link"]; got != "https://drive.google.com/uc?id=drive-file-1" {
		t.Errorf("download_link = %v, want the Drive download link", got)
	}
	// Input metadata must be preserved (not clobbered) alongside the stamps.
	if got := published.ArtifactMetadata["video_id"]; got != "video-1" {
		t.Errorf("video_id = %v, want video-1 (input metadata preserved)", got)
	}
	if got := published.ArtifactMetadata["source"]; got != "chronon" {
		t.Errorf("source = %v, want chronon (input metadata preserved)", got)
	}
	// The canonical Location still carries the Drive identity for the columns.
	if published.Location.FileID != "drive-file-1" || published.Location.WebViewLink == "" {
		t.Errorf("Location not preserved: %+v", published.Location)
	}
}

// TestArtifactPreparation_EmptyDriveLocationDoesNotStamp pins that a
// publisher returning an empty Drive identity does not stamp empty keys.
func TestArtifactPreparation_EmptyDriveLocationDoesNotStamp(t *testing.T) {
	stub := &stubPublisherPort{location: finalization.AssetLocation{Provider: "drive", Action: finalization.PublishCreated}}
	prep := NewArtifactPreparation(stub, nil)

	published, err := prep.Prepare(context.Background(), writeVerifiedArtifact(t, "chronon overlay bytes", nil))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	for _, key := range []string{"drive_file_id", "drive_link", "download_link"} {
		if _, ok := published.ArtifactMetadata[key]; ok {
			t.Errorf("metadata should not contain %q when the publisher returned an empty identity", key)
		}
	}
}

// TestArtifactPreparation_RecordsFinalizeOperations pins the metric split of
// the publish spine (post_writer_finalize is no longer a black box): with a
// run + stage bound to ctx, Prepare records finalize.artifact_prepare,
// finalize.artifact_hash and finalize.drive_publish under the CALLER's stage,
// each carrying the artifact's byte count. artifact_prepare deliberately
// includes the artifact_hash subtiming (the audio_render/mix nesting
// precedent), so the operations must never be summed as wall time.
func TestArtifactPreparation_RecordsFinalizeOperations(t *testing.T) {
	stub := &stubPublisherPort{location: finalization.AssetLocation{
		Provider:     "drive",
		FileID:       "drive-file-1",
		WebViewLink:  "https://drive.google.com/file/d/drive-file-1/view",
		DownloadLink: "https://drive.google.com/uc?id=drive-file-1",
		FolderID:     "folder-overlay",
		FolderPath:   "/video/847/overlay",
		Action:       finalization.PublishCreated,
	}}
	prep := NewArtifactPreparation(stub, nil)

	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{AttemptID: "attempt-finalize-1"})
	defer run.Finish()
	ctx := kernobs.WithStage(kernobs.WithRun(context.Background(), run), kernobs.StageName("post_writer_finalize"))

	artifact := writeVerifiedArtifact(t, "chronon overlay bytes", nil)
	if _, err := prep.Prepare(ctx, artifact); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	got := make(map[string]kernobs.OperationReport)
	for _, op := range run.Report().Operations {
		if op.Component == "finalize" {
			got[op.Operation] = op
		}
	}
	for _, want := range []string{"artifact_prepare", "artifact_hash", "drive_publish"} {
		op, ok := got[want]
		if !ok {
			t.Errorf("missing finalize.%s operation in RunReport", want)
			continue
		}
		if op.Stage != "post_writer_finalize" {
			t.Errorf("finalize.%s stage = %q, want post_writer_finalize", want, op.Stage)
		}
		if op.Status != kernobs.StageStatusCompleted {
			t.Errorf("finalize.%s status = %q, want completed", want, op.Status)
		}
		if op.Bytes != artifact.SizeBytes {
			t.Errorf("finalize.%s bytes = %d, want %d", want, op.Bytes, artifact.SizeBytes)
		}
	}
}

// TestArtifactPreparation_RecordsOperationsDefaultPublishStage pins the
// neutral default: direct publishers that do not tag a stage (stock, vidrush)
// record the publish-spine operations under the canonical publish stage,
// never a fabricated stage.
func TestArtifactPreparation_RecordsOperationsDefaultPublishStage(t *testing.T) {
	stub := &stubPublisherPort{location: finalization.AssetLocation{Provider: "drive", Action: finalization.PublishCreated}}
	prep := NewArtifactPreparation(stub, nil)

	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{AttemptID: "attempt-publish-1"})
	defer run.Finish()
	ctx := kernobs.WithRun(context.Background(), run)

	if _, err := prep.Prepare(ctx, writeVerifiedArtifact(t, "chronon overlay bytes", nil)); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for _, op := range run.Report().Operations {
		if op.Component == "finalize" && op.Stage != string(kernobs.StagePublish) {
			t.Errorf("finalize.%s stage = %q, want publish (default)", op.Operation, op.Stage)
		}
	}
}
