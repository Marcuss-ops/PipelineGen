package wiring

import (
	"strings"
	"testing"
)

func TestAttachFinalJobDriveSourcesKeepsProducerVideoOnHTTPS(t *testing.T) {
	clip := map[string]any{
		"asset_id": "clip-asset", "kind": "video", "role": "clip",
		"source_uri": "https://objects.example/clip", "url": "velox-asset://clip-asset",
	}
	stock := map[string]any{
		"asset_id": "stock-asset", "kind": "video", "role": "stock",
		"drive_file_id": "drive-stock-1", "source_uri": "https://old.example/stock",
	}
	finalize := map[string]any{"runtime_assets": []any{clip, stock}}
	if err := (&remoteFinalJobAdapter{}).attachFinalJobDriveSources(finalize); err != nil {
		t.Fatalf("attachFinalJobDriveSources: %v", err)
	}
	if clip["source_uri"] != "https://objects.example/clip" || clip["url"] != "velox-asset://clip-asset" {
		t.Fatalf("Producer clip source was changed: %#v", clip)
	}
	if stock["source_uri"] != driveStockDownloadURL("drive-stock-1") || stock["url"] != "velox-drive://drive-stock-1" {
		t.Fatalf("stock ref = %#v, want Master Drive locator", stock)
	}
}

func TestAttachFinalJobDriveSourcesRejectsStockWithoutDriveID(t *testing.T) {
	stock := map[string]any{"asset_id": "stock-asset", "kind": "video", "role": "stock"}
	finalize := map[string]any{"runtime_assets": []any{stock}}
	err := (&remoteFinalJobAdapter{}).attachFinalJobDriveSources(finalize)
	if err == nil || !strings.Contains(err.Error(), "no Drive identity") {
		t.Fatalf("attachFinalJobDriveSources error = %v, want missing Drive identity", err)
	}
}
