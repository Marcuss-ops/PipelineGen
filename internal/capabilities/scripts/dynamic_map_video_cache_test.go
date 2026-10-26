package scriptgeneration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// fakeDynamicMapGPUGate is the shared RenderingGen GPU admission double: it
// always grants and counts acquisitions so tests can pin the admission path.
type fakeDynamicMapGPUGate struct{ acquired int }

func (g *fakeDynamicMapGPUGate) Acquire(context.Context) (func(), error) {
	g.acquired++
	return func() {}, nil
}

type observingDynamicMapGPUGate struct {
	acquired  int
	released  int
	active    bool
	onAcquire func()
}

func (g *observingDynamicMapGPUGate) Acquire(context.Context) (func(), error) {
	g.acquired++
	g.active = true
	if g.onAcquire != nil {
		g.onAcquire()
	}
	return func() {
		g.active = false
		g.released++
	}, nil
}

func TestDynamicMapVideoCacheRoundTripAndRejectsCorruption(t *testing.T) {
	root := t.TempDir()
	t.Setenv(dynamicMapVideoCacheDirEnv, root)
	source := filepath.Join(t.TempDir(), "map.mp4")
	if err := os.WriteFile(source, []byte("certified-map-video"), 0o600); err != nil {
		t.Fatal(err)
	}
	telemetry := []byte(`{"schema":"chronon.dynamic-map-telemetry.v1","frames":42}`)
	key := dynamicMapVideoCacheKey([]byte(`{"pins":["a"]}`), "script-hash", scriptoverlay.DefaultMapEncodeProfile(), "cpu", 1920, 1080)
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
	_, sha, size, ok := loadDynamicMapVideoCacheVerified(key, filepath.Join(t.TempDir(), "verified.mp4"))
	if !ok || sha != digest.SHA256String("certified-map-video") || size != int64(len("certified-map-video")) {
		t.Fatalf("verified cache identity = %q/%d/%t", sha, size, ok)
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
	firstKey := dynamicMapVideoCacheKey([]byte("first"), "script", scriptoverlay.DefaultMapEncodeProfile(), "cpu", 1920, 1080)
	secondKey := dynamicMapVideoCacheKey([]byte("second"), "script", scriptoverlay.DefaultMapEncodeProfile(), "cpu", 1920, 1080)
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

func TestDynamicMapVideoCacheMissIsReportedAndDoesNotClaimIdentity(t *testing.T) {
	root := t.TempDir()
	t.Setenv(dynamicMapVideoCacheDirEnv, root)
	key := dynamicMapVideoCacheKey([]byte("cache-miss"), "renderer", scriptoverlay.DefaultMapEncodeProfile(), "opencv_opencl", 1920, 1080)
	telemetry, sha, size, hit := loadDynamicMapVideoCacheVerified(key, filepath.Join(t.TempDir(), "absent.mp4"))
	if hit || len(telemetry) != 0 || sha != "" || size != 0 {
		t.Fatalf("cache miss identity = telemetry:%q sha:%q size:%d hit:%t", telemetry, sha, size, hit)
	}
}

func TestDynamicMapVideoCacheKeyFencesPayloadRendererProfileAndEpoch(t *testing.T) {
	profile := scriptoverlay.DefaultMapEncodeProfile()
	t.Setenv(dynamicMapVideoCacheEpochEnv, "")
	base := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-a", profile, "cpu", 1920, 1080)
	if changed := dynamicMapVideoCacheKey([]byte(`{"camera":"b"}`), "script-a", profile, "cpu", 1920, 1080); changed == base {
		t.Fatal("different resolved map payload shared cache identity")
	}
	if changed := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-b", profile, "cpu", 1920, 1080); changed == base {
		t.Fatal("different renderer script shared cache identity")
	}
	legacyGPU, err := scriptoverlay.ResolveMapEncodeProfile(scriptoverlay.MapLegacyGPUCodec)
	if err != nil {
		t.Fatal(err)
	}
	if changed := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-a", legacyGPU, "cpu", 1920, 1080); changed == base {
		t.Fatal("different encoder profile shared cache identity")
	}
	if changed := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-a", scriptoverlay.DefaultMapEncodeProfile(), "opencv_opencl", 1920, 1080); changed == base {
		t.Fatal("different map sampler shared cache identity")
	}
	if same := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-a", scriptoverlay.DefaultMapEncodeProfile(), "cpu", 1920, 1080); same != base {
		t.Fatal("same renderer inputs must produce a deterministic cache key")
	}
	profile = scriptoverlay.DefaultMapEncodeProfile()
	profile.Preset = "p4"
	if changed := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-a", profile, "cpu", 1920, 1080); changed == base {
		t.Fatal("different encoder preset shared cache identity")
	}
	profile = scriptoverlay.DefaultMapEncodeProfile()
	profile.Quality = 18
	if changed := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-a", profile, "cpu", 1920, 1080); changed == base {
		t.Fatal("different encoder quality shared cache identity")
	}
	if changed := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-a", scriptoverlay.DefaultMapEncodeProfile(), "cpu", 2560, 1440); changed == base {
		t.Fatal("different output dimensions shared cache identity")
	}
	t.Setenv(dynamicMapVideoCacheEpochEnv, "deployment-2")
	if changed := dynamicMapVideoCacheKey([]byte(`{"camera":"a"}`), "script-a", scriptoverlay.DefaultMapEncodeProfile(), "cpu", 1920, 1080); changed == base {
		t.Fatal("different cache epoch shared cache identity")
	}
}

func TestDynamicMapCacheStoreUsesTheExistingVerifiedIdentity(t *testing.T) {
	root := t.TempDir()
	t.Setenv(dynamicMapVideoCacheDirEnv, root)
	source := filepath.Join(t.TempDir(), "map.mp4")
	body := []byte("once-identified-map-video")
	if err := os.WriteFile(source, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sha := digest.SHA256String(string(body))
	key := dynamicMapVideoCacheKey([]byte("identity"), "renderer", scriptoverlay.DefaultMapEncodeProfile(), "cpu", 1920, 1080)
	gotSHA, gotSize, err := storeDynamicMapVideoCacheWithIdentity(key, source, []byte(`{"schema":"telemetry"}`), sha, int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if gotSHA != sha || gotSize != int64(len(body)) {
		t.Fatalf("cache store identity = %q/%d, want %q/%d", gotSHA, gotSize, sha, len(body))
	}
	if _, _, err := storeDynamicMapVideoCacheWithIdentity(key, source, []byte(`{"schema":"telemetry"}`), "bad-identity", int64(len(body))); err == nil {
		t.Fatal("cache store must refuse incomplete verified identity")
	}
}
