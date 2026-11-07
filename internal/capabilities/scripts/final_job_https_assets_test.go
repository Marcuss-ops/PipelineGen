package scriptgeneration

import "testing"

// TestAppendFinalJobRuntimeAssetStripsStockPaths pins the remote stock contract:
// the stockpipeline Drive reference crosses FINALIZE, while every local path is
// discarded so the Producer cannot accidentally process those bytes again.
func TestAppendFinalJobRuntimeAssetStripsStockPaths(t *testing.T) {
	ref := map[string]any{
		"asset_id":          "stock-sha256-abc",
		"drive_file_id":     "drive-stock",
		"source_uri":        "https://drive.usercontent.google.com/download?id=drive-stock&export=download&confirm=t",
		"sha256":            "abc",
		"size_bytes":        int64(1024),
		"duration_ms":       int64(5000),
		"local_source_path": "/tmp/stock.mp4",
		"local_only":        true,
	}
	dst := make([]any, 0, 1)
	appendFinalJobRuntimeAsset(&dst, map[string]bool{}, "stock", ref)
	asset, ok := dst[0].(map[string]any)
	if !ok {
		t.Fatalf("runtime asset = %#v", dst[0])
	}
	for _, key := range []string{"local_source_path", "local_only", "local_path"} {
		if _, exists := asset[key]; exists {
			t.Fatalf("runtime asset leaked %s: %#v", key, asset)
		}
	}
	if asset["drive_file_id"] != "drive-stock" || asset["source_uri"] != ref["source_uri"] {
		t.Fatalf("runtime asset lost its Drive identity: %#v", asset)
	}
}

func TestValidateFinalJobHTTPSRuntimeAssetsKeepsInternalWorkerRefAndSignedSource(t *testing.T) {
	asset := map[string]any{
		"asset_id": "fragment-1", "url": "velox-asset://fragment-1",
		"source_uri":    "https://objects.example/sha256?expires=123&sig=abc",
		"drive_file_id": "drive-id",
	}
	assets := []any{asset}
	if err := validateFinalJobHTTPSRuntimeAssets(assets); err != nil {
		t.Fatalf("validateFinalJobHTTPSRuntimeAssets: %v", err)
	}
	if asset["url"] != "velox-asset://fragment-1" {
		t.Fatalf("worker runtime locator = %#v, want internal asset reference", asset["url"])
	}
	wantSource := "https://objects.example/sha256?expires=123&sig=abc"
	if asset["source_uri"] != wantSource {
		t.Fatalf("canonical source locator = %#v, want signed HTTPS URL", asset["source_uri"])
	}
	if _, exists := asset["drive_file_id"]; exists {
		t.Fatal("final-job runtime asset retained a Drive locator")
	}
}
