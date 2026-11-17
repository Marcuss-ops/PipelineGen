package localization

import (
	"context"
	"testing"
)

// recordingReuseCache is a RenderReuseCache that also records the Drive identity
// of published artifacts, i.e. the optional half the durable adapter implements.
type recordingReuseCache struct {
	artifact *ReusedRenderArtifact
	recorded []RenderDriveIdentity
	calls    int
}

func (c *recordingReuseCache) Lookup(_ context.Context, fingerprint string) (*ReusedRenderArtifact, bool, error) {
	if c.artifact == nil || c.artifact.Fingerprint != fingerprint {
		return nil, false, nil
	}
	copy := *c.artifact
	return &copy, true, nil
}

func (c *recordingReuseCache) Store(_ context.Context, artifact ReusedRenderArtifact) error {
	copy := artifact
	c.artifact = &copy
	return nil
}

func (c *recordingReuseCache) RecordDriveIdentity(_ context.Context, fingerprint string, identity RenderDriveIdentity) error {
	c.calls++
	c.recorded = append(c.recorded, identity)
	return nil
}

// TestDrivePublisher_ReusesPublishedIdentityWithoutUpload pins the saving: an
// artifact that already carries a Drive identity must not be uploaded again when
// reuse is enabled, and the destination folder stays the one that actually holds
// the file (never the folder this run would have used).
func TestDrivePublisher_ReusesPublishedIdentityWithoutUpload(t *testing.T) {
	uploader := &fakeDriveUploader{result: &DriveUploadResult{FileID: "unused", Link: "https://drive/unused"}}
	p := newTestPublisher(t, uploader).WithPublishedReuse(true)

	artifact := renderedArtifact()
	artifact.DriveFileID = "drive-existing"
	artifact.DriveLink = "https://drive/existing"
	artifact.DriveFolderID = "folder-previous"

	out, err := p.Publish(context.Background(), artifact, "folder-this-run")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if out.Status != LocalizedClipUploaded {
		t.Fatalf("status = %q, want UPLOADED", out.Status)
	}
	if out.DriveFileID != "drive-existing" || out.DriveLink != "https://drive/existing" {
		t.Fatalf("drive identity = %q/%q, want the existing file", out.DriveFileID, out.DriveLink)
	}
	if out.DriveFolderID != "folder-previous" {
		t.Fatalf("drive folder = %q, want the folder that holds the file", out.DriveFolderID)
	}
	if uploader.got.FolderID != "" {
		t.Fatalf("publisher uploaded again into %q, want no upload", uploader.got.FolderID)
	}
}

// TestDrivePublisher_DefaultContractStillUploads keeps the default honest: reuse
// is opt-in, so a deployment that did not enable it publishes into this run's
// folder exactly as before.
func TestDrivePublisher_DefaultContractStillUploads(t *testing.T) {
	uploader := &fakeDriveUploader{result: &DriveUploadResult{FileID: "drive-new", Link: "https://drive/new"}}
	p := newTestPublisher(t, uploader)

	artifact := renderedArtifact()
	artifact.DriveFileID = "drive-existing"
	artifact.DriveLink = "https://drive/existing"

	out, err := p.Publish(context.Background(), artifact, "folder-this-run")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if out.DriveFileID != "drive-new" {
		t.Fatalf("drive file = %q, want a fresh upload", out.DriveFileID)
	}
	if uploader.got.FolderID != "folder-this-run" {
		t.Fatalf("upload folder = %q, want this run's folder", uploader.got.FolderID)
	}
}

// TestService_RecordsPublishedDriveIdentity pins the other half of the seam: a
// successful publication is recorded against the plan fingerprint, so the next
// identical run has an identity to reuse. With reuse disabled nothing is
// recorded and the pipeline behaves exactly as before.
func TestService_RecordsPublishedDriveIdentity(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		cache := &recordingReuseCache{}
		renderer := newTestRenderer(t,
			&fakeRendererCompiler{},
			newTestWire(t, &fakeSubtitleResolver{track: matchingTrack()}, &fakeSubtitleCompiler{asset: validSubtitleAsset()}),
			&fakeRenderPlanExecutor{facts: validRenderFacts()},
		).WithRenderReuseCache(cache)
		uploader := &fakeDriveUploader{result: &DriveUploadResult{FileID: "drive-1", Link: "https://drive/1"}}
		service, err := NewServiceWithConcurrency(renderer, newTestPublisher(t, uploader), 1, 1)
		if err != nil {
			t.Fatalf("NewServiceWithConcurrency: %v", err)
		}
		service.WithPublishedDriveReuse(enabled)

		plan := validPlan()
		plan.Fingerprint = Fingerprint(plan)
		result, err := service.Localize(context.Background(), LocalizeInput{
			Concurrency: 1, FolderID: "folder-run", Plans: []LocalizedClipPlan{plan},
		})
		if err != nil || result == nil || len(result.Artifacts) != 1 || len(result.Failures) != 0 {
			t.Fatalf("Localize(enabled=%v): err=%v result=%+v", enabled, err, result)
		}

		if !enabled {
			if cache.calls != 0 {
				t.Fatalf("reuse disabled: recorded %d drive identities, want 0", cache.calls)
			}
			if result.Artifacts[0].DriveFolderID != "folder-run" {
				t.Fatalf("reuse disabled: folder = %q, want this run's folder", result.Artifacts[0].DriveFolderID)
			}
			continue
		}
		if cache.calls != 1 {
			t.Fatalf("reuse enabled: recorded %d drive identities, want 1", cache.calls)
		}
		got := cache.recorded[0]
		if got.FileID != "drive-1" || got.Link != "https://drive/1" || got.FolderID != "folder-run" {
			t.Fatalf("recorded identity = %+v, want the published file in this run's folder", got)
		}
	}
}
