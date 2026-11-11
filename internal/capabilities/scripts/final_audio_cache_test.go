package scriptgeneration

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// Test facade keeps cache coverage at the scripts integration boundary while
// the cache implementation itself lives in scripts/audio.
type finalAudioCache struct {
	cache *scriptaudio.FinalAudioCache
	dir   string
}

func newFinalAudioCache(dir string) *finalAudioCache {
	return &finalAudioCache{cache: scriptaudio.NewFinalAudioCache(dir), dir: dir}
}

func (c *finalAudioCache) enabled() bool { return c != nil && c.cache.Enabled() }

func (c *finalAudioCache) take(plan audio.CompiledAudioPlan, destDir string) (FinalAudioReference, bool) {
	if c == nil || c.cache == nil {
		return FinalAudioReference{}, false
	}
	ref, ok := c.cache.Take(plan, destDir)
	if !ok {
		return FinalAudioReference{}, false
	}
	return finalAudioReferenceFromCache(ref), true
}

func (c *finalAudioCache) put(plan audio.CompiledAudioPlan, ref FinalAudioReference) {
	if c != nil && c.cache != nil {
		c.cache.Put(plan, finalAudioCacheReference(ref))
	}
}

func (c *finalAudioCache) entryPath(planSHA string) string {
	return filepath.Join(c.dir, planSHA[:2], planSHA)
}

// The final-audio checkpoint is keyed by exec.JobID, so it can only ever skip a
// unit inside ONE job. These tests pin the cross-run cache that replaces that
// blind spot: an identical compiled plan reuses the certified master, a plan
// that changed never does, and every hit is re-hashed and re-certified before
// the run trusts it.

func finalAudioCacheTestPlan(sha string) audio.CompiledAudioPlan {
	return audio.CompiledAudioPlan{
		Version:         audio.LegacyAudioPlanVersion,
		TimelineVersion: audio.TimelineVersion,
		DurationUS:      4_000_000,
		// One silence tiling the whole master: the smallest plan the legacy
		// contract accepts, so the test measures the cache, not the compiler.
		Events: []audio.AudioEvent{{EventID: "silence", Type: audio.EventSilence, TimelineStartUS: 0, DurationUS: 4_000_000}},
		Output: audio.AudioOutputContract{
			Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo",
		},
		PlanSHA256: sha,
	}
}

func finalAudioCacheTestReference(t *testing.T, dir string, body []byte) FinalAudioReference {
	t.Helper()
	path := filepath.Join(dir, "master.m4a")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sha, size, err := digest.SHA256File(path)
	if err != nil {
		t.Fatal(err)
	}
	return FinalAudioReference{
		AssetID: "rendered-master", Path: path, Container: "m4a",
		AudioContractVersion: audio.AudioContractVersion, AudioPlanVersion: audio.LegacyAudioPlanVersion,
		PlanSHA256: finalAudioCacheTestPlan("").PlanSHA256, FinalAudioSHA256: sha,
		Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo",
		Bitrate: 128000, DurationUS: 4_000_000, DurationMS: 4000, StartPTS: 0,
		SizeBytes: size, FinalMix: true, CopyEligible: true,
	}
}

func TestFinalAudioCacheReusesCertifiedMasterForIdenticalPlan(t *testing.T) {
	cacheDir, runDir := t.TempDir(), t.TempDir()
	plan := finalAudioCacheTestPlan(strings.Repeat("6", 64))
	ref := finalAudioCacheTestReference(t, t.TempDir(), []byte("certified aac bytes"))
	ref.PlanSHA256 = plan.PlanSHA256
	cache := newFinalAudioCache(cacheDir)
	cache.put(plan, ref)

	hit, ok := cache.take(plan, runDir)
	if !ok {
		t.Fatal("an identical plan must reuse the certified master")
	}
	if err := ValidateFinalAudioReference(hit, plan); err != nil {
		t.Fatalf("a cache hit must satisfy the same canonical contract as a render: %v", err)
	}
	if !strings.EqualFold(hit.FinalAudioSHA256, ref.FinalAudioSHA256) {
		t.Fatalf("reused sha = %s, want the certified %s", hit.FinalAudioSHA256, ref.FinalAudioSHA256)
	}
	if hit.Path == ref.Path {
		t.Fatal("a hit must materialize its own run-owned file, not alias the cache entry")
	}
	body, err := os.ReadFile(hit.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "certified aac bytes" {
		t.Fatalf("staged bytes = %q, want the certified master", body)
	}
}

func TestFinalAudioCacheConcurrentHitsHaveIndependentFiles(t *testing.T) {
	cache := newFinalAudioCache(t.TempDir())
	dest := t.TempDir()
	plan := finalAudioCacheTestPlan(strings.Repeat("6", 64))
	ref := finalAudioCacheTestReference(t, t.TempDir(), []byte("certified aac bytes"))
	ref.PlanSHA256 = plan.PlanSHA256
	cache.put(plan, ref)
	const count = 8
	type result struct {
		ref FinalAudioReference
		ok  bool
	}
	results := make(chan result, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, ok := cache.take(plan, dest)
			results <- result{got, ok}
		}()
	}
	wg.Wait()
	close(results)
	paths := make([]string, 0, count)
	seen := make(map[string]bool)
	for result := range results {
		if !result.ok || seen[result.ref.Path] {
			t.Fatalf("cache hit = %v, duplicate path = %q", result.ok, result.ref.Path)
		}
		if err := ValidateFinalAudioReference(result.ref, plan); err != nil {
			t.Fatal(err)
		}
		seen[result.ref.Path] = true
		paths = append(paths, result.ref.Path)
	}
	if len(paths) != count {
		t.Fatalf("received %d hits, want %d", len(paths), count)
	}
	if err := os.WriteFile(paths[0], []byte("changed by one job"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths[1:] {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != "certified aac bytes" {
			t.Fatalf("another job's audio changed: %q, %v", body, err)
		}
	}
	if _, ok := cache.take(plan, dest); !ok {
		t.Fatal("changing a job's file corrupted the shared cache")
	}
}

func TestFinalAudioCacheConcurrentWritersKeepCertifiedEntry(t *testing.T) {
	cache := newFinalAudioCache(t.TempDir())
	plan := finalAudioCacheTestPlan(strings.Repeat("6", 64))
	ref := finalAudioCacheTestReference(t, t.TempDir(), []byte("certified aac bytes"))
	ref.PlanSHA256 = plan.PlanSHA256
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				cache.put(plan, ref)
			}
		}()
	}
	wg.Wait()
	if got, ok := cache.take(plan, t.TempDir()); !ok || got.FinalAudioSHA256 != ref.FinalAudioSHA256 {
		t.Fatalf("cache entry after concurrent writes = %+v, hit=%v", got, ok)
	}
}

func TestFinalAudioCacheNeverAnswersWithForeignOrStaleBytes(t *testing.T) {
	cacheDir := t.TempDir()
	plan := finalAudioCacheTestPlan(strings.Repeat("6", 64))
	ref := finalAudioCacheTestReference(t, t.TempDir(), []byte("certified aac bytes"))
	ref.PlanSHA256 = plan.PlanSHA256
	cache := newFinalAudioCache(cacheDir)
	cache.put(plan, ref)

	other := finalAudioCacheTestPlan(strings.Repeat("7", 64))
	if _, ok := cache.take(other, t.TempDir()); ok {
		t.Fatal("a different plan hash must never answer with another plan's audio")
	}

	// Corrupt the stored bytes: the sidecar still names the old digest, so the
	// hit must be refused instead of publishing unverified audio.
	entry := cache.entryPath(plan.PlanSHA256) + ".m4a"
	tampered := []byte("tampered aac bytes")
	if err := os.WriteFile(entry, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.take(plan, t.TempDir()); ok {
		t.Fatal("bytes that no longer match the certified digest must be a miss")
	}

	// A truncated artifact is a miss too, even if its digest is not consulted.
	if err := os.WriteFile(entry, []byte("cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.take(plan, t.TempDir()); ok {
		t.Fatal("a size that disagrees with the sidecar must be a miss")
	}
}

func TestFinalAudioCacheDisabledIsANoOp(t *testing.T) {
	for _, dir := range []string{"", "  ", "off"} {
		if cache := newFinalAudioCache(dir); cache.enabled() {
			t.Fatalf("cache dir %q must disable reuse", dir)
		}
	}
	plan := finalAudioCacheTestPlan(strings.Repeat("6", 64))
	ref := finalAudioCacheTestReference(t, t.TempDir(), []byte("certified aac bytes"))
	ref.PlanSHA256 = plan.PlanSHA256
	runner := &Runner{}
	runner.SetFinalAudioCacheDir("off")
	if runner.finalAudioCache != nil {
		t.Fatal("SetFinalAudioCacheDir(\"off\") must leave reuse disabled")
	}
	runner.SetFinalAudioCacheDir(t.TempDir())
	runner.finalAudioCache.Put(plan, finalAudioCacheReference(ref))
	if _, ok := runner.finalAudioCache.Take(plan, t.TempDir()); !ok {
		t.Fatal("a configured cache dir must reuse the certified master")
	}

	// A reference the canonical gate would reject is never stored: a cache of
	// unverifiable entries can only ever produce failed runs.
	broken := ref
	broken.Bitrate = 0
	dir := t.TempDir()
	brokenCache := newFinalAudioCache(dir)
	brokenCache.put(plan, broken)
	if _, ok := brokenCache.take(plan, t.TempDir()); ok {
		t.Fatal("an entry that cannot satisfy the canonical contract must not be stored")
	}
}
