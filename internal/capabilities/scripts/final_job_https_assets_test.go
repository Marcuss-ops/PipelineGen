package scriptgeneration

import "testing"

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
