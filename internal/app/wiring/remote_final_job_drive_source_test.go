package wiring

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	capdelivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

type driveSourcePublisherStub struct {
	request capdelivery.PublishRequest
	result  *capdelivery.PublishResult
	err     error
}

func (s *driveSourcePublisherStub) Publish(_ context.Context, req capdelivery.PublishRequest) (*capdelivery.PublishResult, error) {
	s.request = req
	return s.result, s.err
}

func (s *driveSourcePublisherStub) ResolveFolder(context.Context, capdelivery.PublishRequest) (string, error) {
	return "", nil
}

// TestPromoteFinalJobDriveSourceUsesOneCertifiedClipFragment pins the
// no-stock fallback: a plan with only Producer-owned fragments needs one Drive
// video source, and the promoted source is the exact certified clip fragment
// already served by Objectstore.
func TestPromoteFinalJobDriveSourceUsesOneCertifiedClipFragment(t *testing.T) {
	body := []byte("certified-fragment")
	path := filepath.Join(t.TempDir(), "fragment.mp4")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sha := digest.SHA256String(string(body))
	audio := map[string]any{"asset_id": "audio", "kind": "audio", "role": "final_audio", "sha256": strings.Repeat("b", 64)}
	video := map[string]any{
		"asset_id": sha, "kind": "video", "role": "clip", "sha256": sha,
		"size_bytes": int64(len(body)), "source_uri": "https://objects.example/fragment",
	}
	publisher := &driveSourcePublisherStub{result: &capdelivery.PublishResult{FileID: "drive-fragment-1"}}
	preparer := &finalJobSegmentPreparer{paths: map[string]string{sha: path}}
	adapter := &remoteFinalJobAdapter{drivePublisher: publisher, segments: preparer}
	req := scriptgen.GenerateRequest{}
	req.Render.DriveFolderID = "drive-folder-1"
	finalize := map[string]any{"runtime_assets": []any{audio, video}}

	if err := adapter.promoteFinalJobDriveSource(context.Background(), req, finalize); err != nil {
		t.Fatalf("promoteFinalJobDriveSource: %v", err)
	}
	if publisher.request.DestinationFolderID != "drive-folder-1" ||
		publisher.request.LocalPath != path ||
		publisher.request.ContentHash != sha ||
		publisher.request.SizeBytes != int64(len(body)) ||
		publisher.request.ConflictPolicy != capdelivery.ConflictOverwrite {
		t.Fatalf("publish request = %+v, want the certified fragment into the run folder", publisher.request)
	}
	if video["drive_file_id"] != "drive-fragment-1" ||
		!strings.Contains(stringValue(video["source_uri"]), "drive-fragment-1") ||
		video["url"] != "velox-drive://drive-fragment-1" {
		t.Fatalf("promoted ref = %#v, want Drive locator on the certified fragment", video)
	}
	if _, changed := audio["drive_file_id"]; changed {
		t.Fatalf("audio ref was changed: %#v", audio)
	}
}

// TestPromoteFinalJobDriveSourceLeavesStockOnDrive pins the new boundary: stock
// already has a Drive identity from the stockpipeline, so the Producer must not
// read, copy or publish any local stock bytes to create one.
func TestPromoteFinalJobDriveSourceLeavesStockOnDrive(t *testing.T) {
	stock := map[string]any{
		"asset_id": "stock-sha", "kind": "video", "role": "stock", "drive_file_id": "drive-stock",
		"source_uri": "https://objects.example/old", "url": "https://drive.google.com/file/d/drive-stock/view",
		"sha256": strings.Repeat("a", 64), "size_bytes": int64(2048),
	}
	adapter := &remoteFinalJobAdapter{}
	finalize := map[string]any{"runtime_assets": []any{stock}}
	if err := adapter.promoteFinalJobDriveSource(context.Background(), scriptgen.GenerateRequest{}, finalize); err != nil {
		t.Fatalf("promoteFinalJobDriveSource: %v", err)
	}
	if stock["source_uri"] != driveStockDownloadURL("drive-stock") || stock["url"] != "velox-drive://drive-stock" {
		t.Fatalf("stock ref = %#v, want the stockpipeline Drive locator preserved", stock)
	}
}
