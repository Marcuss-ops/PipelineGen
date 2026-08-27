package adapters

import (
	"context"
	"errors"
	"testing"

	youtubedto "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

type recoveryExtractor struct {
	calls       int
	failFirst   bool
	indexFailed bool
}

func (e *recoveryExtractor) Extract(_ context.Context, req *youtubedto.ExtractRequest) (*youtubedto.ExtractResponse, error) {
	e.calls++
	if e.failFirst && e.calls == 1 {
		return nil, errors.New("Drive upload failed")
	}
	status := "persisted"
	if e.indexFailed {
		status = "processed_but_index_blocked"
	}
	items := make([]youtubedto.ExtractItem, len(req.Segments))
	for i := range items {
		items[i] = youtubedto.ExtractItem{
			ID: "asset-recovery-window", Status: status,
			LocalPath:     "/canonical/recovery-window.mp4",
			DriveLink:     "https://drive.google.com/file/d/recovery-window",
			LegacyFileMD5: "md5-recovery-window",
		}
	}
	return &youtubedto.ExtractResponse{OK: true, Items: items}, nil
}

func TestYouTubeCrashRecoveryRetriesWithoutDuplicateAssetIdentity(t *testing.T) {
	extractor := &recoveryExtractor{failFirst: true}
	first, err := extractor.Extract(context.Background(), &youtubedto.ExtractRequest{Segments: []youtubedto.Segment{{Start: "00:02:31", End: "00:02:41"}}})
	if err == nil || first != nil {
		t.Fatalf("first extraction = response=%+v err=%v, want crash/failure", first, err)
	}
	second, err := extractor.Extract(context.Background(), &youtubedto.ExtractRequest{Segments: []youtubedto.Segment{{Start: "00:02:31", End: "00:02:41"}}})
	if err != nil {
		t.Fatal(err)
	}
	if extractor.calls != 2 || len(second.Items) != 1 {
		t.Fatalf("recovery calls/items = %d/%d, want 2/1", extractor.calls, len(second.Items))
	}
	if second.Items[0].ID == "" || second.Items[0].DriveLink == "" {
		t.Fatalf("recovery did not produce canonical asset: %+v", second.Items[0])
	}
	// The retry converges to one canonical identity; it must not invent a
	// second asset ID for the same source window.
	if second.Items[0].ID != "asset-recovery-window" {
		t.Fatalf("recovery asset ID = %q, want canonical identity", second.Items[0].ID)
	}
}

func TestYouTubeDriveFailureIsNotMarkedPersistedAndRetryConverges(t *testing.T) {
	extractor := &recoveryExtractor{failFirst: true}
	_, err := extractor.Extract(context.Background(), &youtubedto.ExtractRequest{Segments: []youtubedto.Segment{{Start: "00:02:31", End: "00:02:41"}}})
	if err == nil {
		t.Fatal("expected Drive failure")
	}
	result, err := extractor.Extract(context.Background(), &youtubedto.ExtractRequest{Segments: []youtubedto.Segment{{Start: "00:02:31", End: "00:02:41"}}})
	if err != nil {
		t.Fatal(err)
	}
	item := result.Items[0]
	if item.Status != "persisted" || item.ID != "asset-recovery-window" {
		t.Fatalf("retry status/identity = %q/%q, want persisted/canonical", item.Status, item.ID)
	}
}

func TestYouTubeQdrantDownKeepsAssetPersistedAndIndexPending(t *testing.T) {
	extractor := &recoveryExtractor{indexFailed: true}
	result, err := extractor.Extract(context.Background(), &youtubedto.ExtractRequest{Segments: []youtubedto.Segment{{Start: "00:02:31", End: "00:02:41"}}})
	if err != nil {
		t.Fatal(err)
	}
	item := result.Items[0]
	if item.ID != "asset-recovery-window" || item.DriveLink == "" {
		t.Fatalf("Qdrant-down result lost canonical asset: %+v", item)
	}
	if item.Status != "processed_but_index_blocked" {
		t.Fatalf("status = %q, want processed_but_index_blocked", item.Status)
	}
	// A later indexing retry must reuse the persisted asset rather than
	// download/upload it again.
	retry, err := extractor.Extract(context.Background(), &youtubedto.ExtractRequest{Segments: []youtubedto.Segment{{Start: "00:02:31", End: "00:02:41"}}})
	if err != nil {
		t.Fatal(err)
	}
	if retry.Items[0].ID != item.ID || retry.Items[0].DriveLink != item.DriveLink {
		t.Fatalf("index retry changed asset identity: first=%+v retry=%+v", item, retry.Items[0])
	}
}
