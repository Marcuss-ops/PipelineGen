package scriptgeneration

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
)

func TestDynamicMapVideoCacheRoundTripAndRejectsCorruption(t *testing.T) {
	root := t.TempDir()
	t.Setenv(dynamicMapVideoCacheDirEnv, root)
	source := filepath.Join(t.TempDir(), "map.mp4")
	if err := os.WriteFile(source, []byte("certified-map-video"), 0o600); err != nil {
		t.Fatal(err)
	}
	telemetry := []byte(`{"schema":"chronon.dynamic-map-telemetry.v1","frames":42}`)
	key := dynamicMapVideoCacheKey([]byte(`{"pins":["a"]}`), "script-hash", scriptoverlay.DefaultMapEncodeProfile(), 1920, 1080)
	if err := storeDynamicMapVideoCache(key, source, telemetry); err != nil {
		t.Fatalf("store cache entry: %v", err)
	}
	destination := filepath.Join(t.TempDir(), "restored.mp4")
	gotTelemetry, ok := loadDynamicMapVideoCache(key, destination)
	if !ok {
		t.Fatal("expected verified cache hit")
	}
	if string(gotTelemetry) != string(telemetry) {
		t.Fatalf("telemetry = %s, want %s", gotTelemetry, telemetry)
	}
	gotVideo, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotVideo) != "certified-map-video" {
		t.Fatalf("restored video = %q", gotVideo)
	}

	cacheVideo, _, _ := dynamicMapVideoCachePaths(key)
	if err := os.WriteFile(cacheVideo, []byte("corrupted-map-video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadDynamicMapVideoCache(key, filepath.Join(t.TempDir(), "must-not-exist.mp4")); ok {
		t.Fatal("tampered content-addressed cache entry was served")
	}
}

func TestDynamicMapVideoCachePrunesOldestVideoAtConfiguredBound(t *testing.T) {
	root := t.TempDir()
	t.Setenv(dynamicMapVideoCacheDirEnv, root)
	t.Setenv(dynamicMapVideoCacheMaxEnv, "25")
	source := filepath.Join(t.TempDir(), "map.mp4")
	if err := os.WriteFile(source, []byte("certified-map-video"), 0o600); err != nil {
		t.Fatal(err)
	}
	telemetry := []byte(`{"schema":"chronon.dynamic-map-telemetry.v1"}`)
	firstKey := dynamicMapVideoCacheKey([]byte("first"), "script", scriptoverlay.DefaultMapEncodeProfile(), 1920, 1080)
	secondKey := dynamicMapVideoCacheKey([]byte("second"), "script", scriptoverlay.DefaultMapEncodeProfile(), 1920, 1080)
	if err := storeDynamicMapVideoCache(firstKey, source, telemetry); err != nil {
		t.Fatal(err)
	}
	firstVideo, _, _ := dynamicMapVideoCachePaths(firstKey)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(firstVideo, old, old); err != nil {
		t.Fatal(err)
	}
	if err := storeDynamicMapVideoCache(secondKey, source, telemetry); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadDynamicMapVideoCache(firstKey, filepath.Join(t.TempDir(), "first.mp4")); ok {
		t.Fatal("oldest cache entry survived the configured size bound")
	}
	if _, ok := loadDynamicMapVideoCache(secondKey, filepath.Join(t.TempDir(), "second.mp4")); !ok {
		t.Fatal("newest cache entry was not retained")
	}
}

func TestDynamicMapVideoCacheKeyFencesPayloadRendererProfileAndEpoch(t *testing.T) {
	profile := scriptoverlay.DefaultMapEncodeProfile()
	base := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-a", profile, 1920, 1080)
	if changed := dynamicMapVideoCacheKey([]byte(`{"camera":"b"}`), "script-a", profile, 1920, 1080); changed == base {
		t.Fatal("different resolved map payload shared cache identity")
	}
	if changed := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-b", profile, 1920, 1080); changed == base {
		t.Fatal("different renderer script shared cache identity")
	}
	profile.Codec = scriptoverlay.MapCPUCodec
	profile.ExpectGPU = false
	if changed := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-a", profile, 1920, 1080); changed == base {
		t.Fatal("different encoder profile shared cache identity")
	}
	if changed := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-a", scriptoverlay.DefaultMapEncodeProfile(), 2560, 1440); changed == base {
		t.Fatal("different output dimensions shared cache identity")
	}
	t.Setenv(dynamicMapVideoCacheEpochEnv, "deployment-2")
	if changed := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-a", scriptoverlay.DefaultMapEncodeProfile(), 1920, 1080); changed == base {
		t.Fatal("different cache epoch shared cache identity")
	}
}
