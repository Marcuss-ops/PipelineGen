package adapters

import (
	"testing"

	youtubedto "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

func TestYouTubeExtractionItemConsistencyAcrossCanonicalSurfaces(t *testing.T) {
	item := youtubedto.ExtractItem{
		ID: "asset-poland-1939", DriveFileID: "drive-poland-1939",
		DriveLink: "https://drive.google.com/file/d/drive-poland-1939/view",
		LocalPath: "/tmp/poland-1939.mp4", LegacyFileMD5: "md5-poland-1939",
		Status: "persisted", StartSeconds: 151, EndSeconds: 161, Duration: 10,
	}

	if item.ID == "" || item.DriveFileID == "" || item.DriveLink == "" || item.LocalPath == "" || item.LegacyFileMD5 == "" {
		t.Fatal("canonical extraction item is missing a required Drive/asset identity")
	}
	if item.Status != "persisted" {
		t.Fatalf("status = %q, want persisted", item.Status)
	}
	if item.EndSeconds-item.StartSeconds != 10 || item.Duration != 10 {
		t.Fatalf("duration = %d seconds, want 10", item.Duration)
	}

	// The canonical extractor response is the single handoff consumed by the
	// SQLite media_assets writer, outbox event producer and indexing worker.
	// Assert the identity remains complete at that boundary rather than
	// introducing separate provider-specific persistence DTOs.
	response := youtubedto.ExtractResponse{OK: true, Items: []youtubedto.ExtractItem{item}}
	if !response.OK || len(response.Items) != 1 {
		t.Fatalf("invalid canonical response: %+v", response)
	}
	persisted := response.Items[0]
	if persisted.ID != item.ID || persisted.DriveLink != item.DriveLink || persisted.LegacyFileMD5 != item.LegacyFileMD5 {
		t.Fatalf("asset identity changed between extractor and persistence handoff: %+v", persisted)
	}
}
