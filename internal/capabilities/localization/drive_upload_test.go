package localization

import (
	"context"
	"errors"
	"testing"
)

// fakeDriveUploader records the upload input and returns a fixed result (or
// error).
type fakeDriveUploader struct {
	result *DriveUploadResult
	err    error
	got    DriveUploadInput
}

func (f *fakeDriveUploader) Upload(_ context.Context, in DriveUploadInput) (*DriveUploadResult, error) {
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

// renderedArtifact returns a RENDERED artifact with verified local bytes.
func renderedArtifact() LocalizedClipArtifact {
	return LocalizedClipArtifact{
		Version:         LocalizedClipArtifactVersion,
		JobID:           "job-1",
		SceneID:         "scene-1",
		ClipID:          "clip-1",
		Language:        "es",
		PlanFingerprint: "plan-fp",
		AssetID:         "asset-es",
		LocalPath:       "/tmp/renders/clip-1.es.mp4",
		SHA256:          "render-sha",
		SizeBytes:       1234,
		DurationMS:      10000,
		VideoCodec:      "h264",
		AudioCodec:      "aac",
		Status:          LocalizedClipRendered,
	}
}

func newTestPublisher(t *testing.T, uploader DriveUploader) *DrivePublisher {
	t.Helper()
	p, err := NewDrivePublisher(uploader)
	if err != nil {
		t.Fatalf("NewDrivePublisher: %v", err)
	}
	return p
}

// TestDrivePublisher_PublishesRenderedArtifact verifies the upload returns a
// certified artifact (UPLOADED + DriveFileID + DriveLink) without mutating
// the input.
func TestDrivePublisher_PublishesRenderedArtifact(t *testing.T) {
	uploader := &fakeDriveUploader{result: &DriveUploadResult{FileID: "drive-es", Link: "https://drive/es"}}
	p := newTestPublisher(t, uploader)

	in := renderedArtifact()
	out, err := p.Publish(context.Background(), in, "folder-1")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if out.Status != LocalizedClipUploaded {
		t.Errorf("status: got %q, want UPLOADED", out.Status)
	}
	if out.DriveFileID != "drive-es" || out.DriveLink != "https://drive/es" {
		t.Errorf("drive facts: got file=%q link=%q", out.DriveFileID, out.DriveLink)
	}
	// Input not mutated.
	if in.Status != LocalizedClipRendered || in.DriveFileID != "" {
		t.Errorf("input was mutated: %+v", in)
	}

	// Upload input carries the certified facts.
	got := uploader.got
	if got.LocalPath != "/tmp/renders/clip-1.es.mp4" || got.Filename != "clip-1.es.render-sha.mp4" || got.FolderID != "folder-1" || got.ContentHash != "render-sha" || got.Language != "es" || got.SizeBytes != 1234 {
		t.Errorf("upload input: got %+v", got)
	}
}

// TestDrivePublisher_RejectsNotRendered verifies a non-RENDERED artifact
// (e.g. FAILED) is never uploaded.
func TestDrivePublisher_RejectsNotRendered(t *testing.T) {
	p := newTestPublisher(t, &fakeDriveUploader{result: &DriveUploadResult{FileID: "x"}})

	art := renderedArtifact()
	art.Status = LocalizedClipFailed
	if _, err := p.Publish(context.Background(), art, "folder"); err == nil {
		t.Fatal("Publish must reject a non-RENDERED artifact")
	}
}

// TestDrivePublisher_RejectsMissingFacts verifies a missing local path, hash,
// or folder fails closed before any upload.
func TestDrivePublisher_RejectsMissingFacts(t *testing.T) {
	p := newTestPublisher(t, &fakeDriveUploader{result: &DriveUploadResult{FileID: "x"}})

	cases := []struct {
		name   string
		mutate func(*LocalizedClipArtifact)
		folder string
	}{
		{"missing local_path", func(a *LocalizedClipArtifact) { a.LocalPath = "" }, "folder"},
		{"missing sha256", func(a *LocalizedClipArtifact) { a.SHA256 = " " }, "folder"},
		{"missing folder", func(a *LocalizedClipArtifact) {}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			art := renderedArtifact()
			tc.mutate(&art)
			if _, err := p.Publish(context.Background(), art, tc.folder); err == nil {
				t.Fatalf("Publish must reject %s", tc.name)
			}
		})
	}
}

// TestDrivePublisher_PropagatesUploaderError verifies uploader failures are
// surfaced (never a fabricated UPLOADED artifact).
func TestDrivePublisher_PropagatesUploaderError(t *testing.T) {
	p := newTestPublisher(t, &fakeDriveUploader{err: errors.New("drive down")})

	out, err := p.Publish(context.Background(), renderedArtifact(), "folder")
	if err == nil {
		t.Fatal("Publish must propagate the uploader error")
	}
	if out.Status != LocalizedClipRendered || out.DriveFileID != "" {
		t.Errorf("failed upload must not fabricate UPLOADED facts: %+v", out)
	}
}

// TestDrivePublisher_RejectsIncompleteResult verifies an uploader returning an
// empty Drive result is a typed failure.
func TestDrivePublisher_RejectsIncompleteResult(t *testing.T) {
	p := newTestPublisher(t, &fakeDriveUploader{result: &DriveUploadResult{}})

	if _, err := p.Publish(context.Background(), renderedArtifact(), "folder"); err == nil {
		t.Fatal("Publish must reject an empty Drive result")
	}
}

// TestDrivePublisher_NilUploaderFailsConstruction verifies the publisher
// cannot be built without an uploader.
func TestDrivePublisher_NilUploaderFailsConstruction(t *testing.T) {
	if _, err := NewDrivePublisher(nil); err == nil {
		t.Fatal("NewDrivePublisher must reject a nil uploader")
	}
}
