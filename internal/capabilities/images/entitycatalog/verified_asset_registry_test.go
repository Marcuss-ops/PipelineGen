package entitycatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadVerifiedAssetRegistryVerifiesBytesAndLocator(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "francis.png")
	imageBytes := []byte("verified-francis")
	if err := os.WriteFile(imagePath, imageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(imageBytes)
	sha := hex.EncodeToString(sum[:])
	manifest := map[string]any{
		"version": 1,
		"entities": map[string]any{
			"Francis Kaufmann": []any{map[string]any{
				"asset_id":      sha,
				"sha256":        sha,
				"path":          "francis.png",
				"media_type":    "image/png",
				"drive_file_id": "drive-francis-1",
				"drive_link":    "https://drive.google.com/file/d/drive-francis-1/view",
			}},
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(dir, "entity_asset_registry.json")
	if err := os.WriteFile(registryPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(VerifiedAssetRegistryEnv, registryPath)

	registry, err := LoadVerifiedAssetRegistry()
	if err != nil {
		t.Fatal(err)
	}
	refs := registry[NormalizeVerifiedAssetKey("Francis Kaufmann")]
	if len(refs) != 1 {
		t.Fatalf("refs = %d, want 1", len(refs))
	}
	ref := refs[0]
	if ref.AssetID != sha || ref.SHA256 != sha || ref.LocalPath != imagePath {
		t.Fatalf("unexpected verified asset: %+v", ref)
	}
	if ref.MediaType != "image/png" {
		t.Fatalf("media type = %q, want image/png", ref.MediaType)
	}
	if ref.URL != "https://drive.google.com/uc?export=download&id=drive-francis-1" {
		t.Fatalf("URL = %q, want canonical Drive download locator", ref.URL)
	}
}

func TestLoadVerifiedAssetRegistrySkipsModifiedFile(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "francis.png")
	if err := os.WriteFile(imagePath, []byte("verified-francis"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"version": 1,
		"entities": map[string]any{
			"Francis Kaufmann": []any{map[string]any{
				"asset_id": strings.Repeat("a", 64),
				"sha256":   strings.Repeat("a", 64),
				"path":     "francis.png",
			}},
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(dir, "entity_asset_registry.json")
	if err := os.WriteFile(registryPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(VerifiedAssetRegistryEnv, registryPath)

	registry, err := LoadVerifiedAssetRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry) != 0 {
		t.Fatalf("modified file entered the registry: %+v", registry)
	}
}

func TestLoadVerifiedAssetRegistryFailsClosedOnMalformedManifest(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "entity_asset_registry.json")
	if err := os.WriteFile(registryPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(VerifiedAssetRegistryEnv, registryPath)

	if _, err := LoadVerifiedAssetRegistry(); err == nil {
		t.Fatal("malformed registry was accepted")
	}
}

func TestLoadVerifiedAssetRegistryFailsClosedOnUnsupportedVersion(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "entity_asset_registry.json")
	if err := os.WriteFile(registryPath, []byte(`{"version":2,"entities":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(VerifiedAssetRegistryEnv, registryPath)

	if _, err := LoadVerifiedAssetRegistry(); err == nil {
		t.Fatal("unsupported registry version was accepted")
	}
}
