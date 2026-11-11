package script

import (
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
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
	if err := (&RemoteFinalJobAdapter{}).attachFinalJobDriveSources(finalize); err != nil {
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
	err := (&RemoteFinalJobAdapter{}).attachFinalJobDriveSources(finalize)
	if err == nil || !strings.Contains(err.Error(), "no Drive identity") {
		t.Fatalf("attachFinalJobDriveSources error = %v, want missing Drive identity", err)
	}
}

func TestAttachFinalJobDriveSourcesV1PreservesIdentityAndSetsLocators(t *testing.T) {
	finalize := scriptgen.FinalJobFinalizeV1{RuntimeAssets: []scriptgen.FinalJobRuntimeAssetV1{
		{AssetID: "stock-asset", SHA256: strings.Repeat("a", 64), SizeBytes: 100, Kind: "video", Role: "stock", DriveFileID: "drive-stock"},
		{AssetID: "overlay-asset", SHA256: strings.Repeat("b", 64), SizeBytes: 200, Kind: "video", Role: "overlay", URL: "https://objects.example/overlay", DriveFileID: "not-an-identity"},
	}}
	if err := attachFinalJobDriveSourcesV1(&finalize); err != nil {
		t.Fatalf("attachFinalJobDriveSourcesV1: %v", err)
	}
	stock, overlay := finalize.RuntimeAssets[0], finalize.RuntimeAssets[1]
	if stock.AssetID != "stock-asset" || stock.URL != "velox-drive://drive-stock" || !strings.HasPrefix(stock.SourceURI, "https://") {
		t.Fatalf("stock identity/locators = %+v", stock)
	}
	if overlay.AssetID != "overlay-asset" || overlay.DriveFileID != "" || overlay.URL != "velox-asset://overlay-asset" || overlay.SourceURI != "https://objects.example/overlay" {
		t.Fatalf("overlay identity/locators = %+v", overlay)
	}
}
