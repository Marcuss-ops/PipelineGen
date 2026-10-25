package scriptgeneration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// dynamicMapVideoFlightKey is a CONSERVATIVE outer key for render sharing: it
// must be equal whenever the durable cache key is equal, so it covers every
// cache-key input it can observe (the cache epoch, the encoder profile, the
// sampler and the whole plan). False splits are harmless (the durable cache
// still de-duplicates the store); false joins are impossible because the plan
// itself is in the key.
func dynamicMapVideoFlightKey(plan capoverlay.OverlayPlan) string {
	profile, _ := scriptoverlay.ResolveMapEncodeProfile(os.Getenv(scriptoverlay.MapEncodeProfileEnv))
	sampler := ""
	if len(plan.Items) == 1 && plan.Items[0].Map != nil {
		sampler = dynamicMapSamplerForProfile(profile)
	}
	encoded, _ := json.Marshal(plan)
	return digest.SHA256String(strings.Join([]string{
		"pipelinegen.dynamic-map-video-flight/v1",
		strings.TrimSpace(os.Getenv(dynamicMapVideoCacheEpochEnv)),
		sampler, profile.Codec, profile.Preset, fmt.Sprint(profile.Quality),
		string(encoded),
	}, "\x00"))
}

// joinDynamicMapVideoRender runs render unless an identical map render is
// already in flight. A joiner WAITS for the leader and then re-enters render()
// itself: on leader success that re-entry is the durable-cache hit path (the
// leader's workspace is private and removed by its own caller), on leader
// failure it is an independent retry — one transient failure never fails two
// callers.
func joinDynamicMapVideoRender(ctx context.Context, plan capoverlay.OverlayPlan, render func() (string, error)) (string, error) {
	key := dynamicMapVideoFlightKey(plan)
	promise := &dynamicMapVideoPromise{done: make(chan struct{})}
	existing, loaded := dynamicMapVideoRenders.LoadOrStore(key, promise)
	if loaded {
		leader := existing.(*dynamicMapVideoPromise)
		select {
		case <-leader.done:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return render()
	}
	path, err := render()
	promise.err = err
	close(promise.done)
	dynamicMapVideoRenders.Delete(key)
	return path, err
}

func dynamicMapVideoCacheKey(payload []byte, scriptDigest string, profile scriptoverlay.MapEncodeProfile, sampler string, outputWidth, outputHeight int) string {
	epoch := strings.TrimSpace(os.Getenv(dynamicMapVideoCacheEpochEnv))
	if epoch == "" {
		epoch = "dynamic-map-video-cache/v1"
	}
	return digest.SHA256String(strings.Join([]string{
		"pipelinegen.dynamic-map-video-cache/v1",
		epoch,
		scriptDigest,
		profile.Codec,
		fmt.Sprint(profile.ExpectGPU),
		profile.Preset,
		fmt.Sprint(profile.Quality),
		sampler,
		fmt.Sprintf("%dx%d", outputWidth, outputHeight),
		digest.SHA256String(string(payload)),
	}, "\x00"))
}

func dynamicMapVideoCachePaths(key string, cacheDirs ...string) (videoPath, manifestPath string, enabled bool) {
	root := strings.TrimSpace(os.Getenv(dynamicMapVideoCacheDirEnv))
	if root == "" && len(cacheDirs) > 0 {
		root = strings.TrimSpace(cacheDirs[0])
	}
	if root == "" || len(key) != 64 {
		return "", "", false
	}
	return filepath.Join(root, key+".mp4"), filepath.Join(root, key+".json"), true
}

// loadDynamicMapVideoCache treats every malformed or stale entry as a miss.
// The caller still runs the normal render and certification path.
func loadDynamicMapVideoCache(key, destination string, cacheDirs ...string) ([]byte, bool) {
	telemetry, _, _, hit := loadDynamicMapVideoCacheVerified(key, destination, cacheDirs...)
	return telemetry, hit
}

func loadDynamicMapVideoCacheVerified(key, destination string, cacheDirs ...string) ([]byte, string, int64, bool) {
	videoPath, manifestPath, enabled := dynamicMapVideoCachePaths(key, cacheDirs...)
	if !enabled {
		return nil, "", 0, false
	}
	videoInfo, err := os.Lstat(videoPath)
	if err != nil || !videoInfo.Mode().IsRegular() {
		return nil, "", 0, false
	}
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() {
		return nil, "", 0, false
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, "", 0, false
	}
	var manifest dynamicMapVideoCacheManifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Schema != "pipelinegen.dynamic-map-video-cache.v1" ||
		manifest.Key != key || manifest.SizeBytes <= 0 || len(manifest.SHA256) != 64 || len(manifest.Telemetry) == 0 ||
		videoInfo.Size() != manifest.SizeBytes {
		return nil, "", 0, false
	}
	actual, size, err := digest.SHA256File(videoPath)
	if err != nil || size != manifest.SizeBytes || !strings.EqualFold(actual, manifest.SHA256) {
		return nil, "", 0, false
	}
	if err := copyDynamicMapFile(videoPath, destination); err != nil {
		return nil, "", 0, false
	}
	return append(json.RawMessage(nil), manifest.Telemetry...), actual, size, true
}

func storeDynamicMapVideoCache(key, source string, telemetry []byte, cacheDirs ...string) error {
	_, _, err := storeDynamicMapVideoCacheVerified(key, source, telemetry, cacheDirs...)
	return err
}

func storeDynamicMapVideoCacheVerified(key, source string, telemetry []byte, cacheDirs ...string) (string, int64, error) {
	sha, size, err := hashDynamicMapVideo(source)
	if err != nil {
		return "", 0, err
	}
	return storeDynamicMapVideoCacheWithIdentity(key, source, telemetry, sha, size, cacheDirs...)
}

func storeDynamicMapVideoCacheWithIdentity(key, source string, telemetry []byte, expectedSHA string, expectedSize int64, cacheDirs ...string) (string, int64, error) {
	videoPath, manifestPath, enabled := dynamicMapVideoCachePaths(key, cacheDirs...)
	if !enabled {
		return "", 0, nil
	}
	if len(telemetry) == 0 {
		return "", 0, fmt.Errorf("dynamic map cache: telemetry is required")
	}
	if len(expectedSHA) != 64 || expectedSize <= 0 {
		return "", 0, fmt.Errorf("dynamic map cache: verified source identity is required")
	}
	if err := os.MkdirAll(filepath.Dir(videoPath), 0o750); err != nil {
		return "", 0, fmt.Errorf("create cache directory: %w", err)
	}
	videoTemp, err := os.CreateTemp(filepath.Dir(videoPath), ".map-video-*.tmp")
	if err != nil {
		return "", 0, fmt.Errorf("create cached video temp: %w", err)
	}
	videoTempPath := videoTemp.Name()
	defer func() { _ = os.Remove(videoTempPath) }()
	src, err := os.Open(source)
	if err != nil {
		_ = videoTemp.Close()
		return "", 0, fmt.Errorf("open rendered map video: %w", err)
	}
	sum, copyErr := digest.SHA256Reader(io.TeeReader(src, videoTemp))
	closeSrcErr := src.Close()
	if copyErr != nil {
		_ = videoTemp.Close()
		return "", 0, fmt.Errorf("copy rendered map video: %w", copyErr)
	}
	if closeSrcErr != nil {
		_ = videoTemp.Close()
		return "", 0, fmt.Errorf("close rendered map video: %w", closeSrcErr)
	}
	if !strings.EqualFold(sum, expectedSHA) {
		_ = videoTemp.Close()
		return "", 0, fmt.Errorf("rendered map video changed after identity verification")
	}
	videoInfo, statErr := videoTemp.Stat()
	if statErr != nil {
		_ = videoTemp.Close()
		return "", 0, fmt.Errorf("stat cached map video: %w", statErr)
	}
	size := videoInfo.Size()
	if size <= 0 {
		_ = videoTemp.Close()
		return "", 0, fmt.Errorf("rendered map video is empty")
	}
	if size != expectedSize {
		_ = videoTemp.Close()
		return "", 0, fmt.Errorf("rendered map video size changed after identity verification: got=%d want=%d", size, expectedSize)
	}
	if err := videoTemp.Sync(); err != nil {
		_ = videoTemp.Close()
		return sum, size, fmt.Errorf("sync cached map video: %w", err)
	}
	if err := videoTemp.Close(); err != nil {
		return sum, size, fmt.Errorf("close cached map video: %w", err)
	}
	if err := os.Rename(videoTempPath, videoPath); err != nil {
		return sum, size, fmt.Errorf("install cached map video: %w", err)
	}
	manifest := dynamicMapVideoCacheManifest{
		Schema: "pipelinegen.dynamic-map-video-cache.v1", Key: key,
		SHA256: sum, SizeBytes: size,
		Telemetry: append(json.RawMessage(nil), telemetry...),
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return sum, size, fmt.Errorf("encode cache manifest: %w", err)
	}
	manifestTemp, err := os.CreateTemp(filepath.Dir(manifestPath), ".map-manifest-*.tmp")
	if err != nil {
		return sum, size, fmt.Errorf("create cache manifest temp: %w", err)
	}
	manifestTempPath := manifestTemp.Name()
	defer func() { _ = os.Remove(manifestTempPath) }()
	if _, err := manifestTemp.Write(encoded); err != nil {
		_ = manifestTemp.Close()
		return sum, size, fmt.Errorf("write cache manifest: %w", err)
	}
	if err := manifestTemp.Sync(); err != nil {
		_ = manifestTemp.Close()
		return sum, size, fmt.Errorf("sync cache manifest: %w", err)
	}
	if err := manifestTemp.Close(); err != nil {
		return sum, size, fmt.Errorf("close cache manifest: %w", err)
	}
	if err := os.Rename(manifestTempPath, manifestPath); err != nil {
		return sum, size, fmt.Errorf("install cache manifest: %w", err)
	}
	if err := pruneDynamicMapVideoCache(filepath.Dir(videoPath), dynamicMapVideoCacheMaxSize()); err != nil {
		return sum, size, fmt.Errorf("prune map video cache: %w", err)
	}
	return sum, size, nil
}

type dynamicMapCacheEntry struct {
	videoPath    string
	manifestPath string
	modTime      time.Time
	size         int64
}

func dynamicMapVideoCacheMaxSize() int64 {
	value := strings.TrimSpace(os.Getenv(dynamicMapVideoCacheMaxEnv))
	if value == "" {
		return dynamicMapVideoCacheMaxBytes
	}
	maxBytes, err := strconv.ParseInt(value, 10, 64)
	if err != nil || maxBytes <= 0 {
		return dynamicMapVideoCacheMaxBytes
	}
	return maxBytes
}

// pruneDynamicMapVideoCache bounds local cache growth. Entries with the oldest
// video mtime are removed first; a concurrent reader treats a disappearing
// entry as a miss and falls back to the renderer.
func pruneDynamicMapVideoCache(root string, maxBytes int64) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var cached []dynamicMapCacheEntry
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mp4") {
			continue
		}
		videoPath := filepath.Join(root, entry.Name())
		info, err := os.Lstat(videoPath)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		manifestPath := strings.TrimSuffix(videoPath, ".mp4") + ".json"
		cached = append(cached, dynamicMapCacheEntry{
			videoPath: videoPath, manifestPath: manifestPath,
			modTime: info.ModTime(), size: info.Size(),
		})
		total += info.Size()
	}
	if total <= maxBytes {
		return nil
	}
	sort.Slice(cached, func(i, j int) bool { return cached[i].modTime.Before(cached[j].modTime) })
	for _, entry := range cached {
		if total <= maxBytes {
			break
		}
		if err := os.Remove(entry.videoPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Remove(entry.manifestPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= entry.size
	}
	return nil
}

func copyDynamicMapFile(source, destination string) error {
	// Cache and per-run workspaces usually share a filesystem. Avoid copying a
	// large certified MP4 on cache hits; fall back to streaming across devices.
	if err := os.Link(source, destination); err == nil {
		return nil
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
