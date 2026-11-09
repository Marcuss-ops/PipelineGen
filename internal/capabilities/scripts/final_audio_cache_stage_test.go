package scriptgeneration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// TestFinalAudioCacheHitStagesIndependentBytes pins the cache materialization
// contract after the I/O reduction: a hit still returns run-owned bytes that
// hash to the sidecar identity, and mutating the staged file must never touch
// the cached master. The reflink/copy choice is an implementation detail; the
// independence is the promise.
func TestFinalAudioCacheHitStagesIndependentBytes(t *testing.T) {
	cache := newFinalAudioCache(t.TempDir())
	if !cache.enabled() {
		t.Fatal("cache must be enabled for a non-empty directory")
	}
	body := []byte("certified final audio master bytes")
	source := filepath.Join(t.TempDir(), "master.m4a")
	if err := os.WriteFile(source, body, 0o600); err != nil {
		t.Fatal(err)
	}
	plan := audio.CompiledAudioPlan{
		PlanSHA256: strings.Repeat("a", 64),
		Version:    audio.AudioPlanVersion,
	}
	ref := FinalAudioReference{
		Path: source, FinalAudioSHA256: digest.SHA256String(string(body)),
		SizeBytes: int64(len(body)), DurationMS: 2500, Bitrate: 128000,
		Container: "m4a", Codec: "aac", Profile: "LC", SampleRate: 48000,
		Channels: 2, ChannelLayout: "stereo", FinalMix: true, CopyEligible: true,
	}
	cache.put(plan, ref)

	hit, ok := cache.take(plan, t.TempDir())
	if !ok {
		t.Fatal("a complete sidecar plus matching bytes must be a cache hit")
	}
	if hit.Path == "" || hit.Path == source {
		t.Fatalf("staged path = %q, want independent run-owned bytes", hit.Path)
	}
	staged, err := os.ReadFile(hit.Path)
	if err != nil {
		t.Fatalf("read staged audio: %v", err)
	}
	if string(staged) != string(body) {
		t.Fatalf("staged bytes = %q, want the certified master", staged)
	}
	if !strings.EqualFold(hit.FinalAudioSHA256, ref.FinalAudioSHA256) || hit.SizeBytes != ref.SizeBytes {
		t.Fatalf("staged identity = %s/%d, want %s/%d", hit.FinalAudioSHA256, hit.SizeBytes, ref.FinalAudioSHA256, ref.SizeBytes)
	}
	// Independence: a consumer rewriting its staged file must not corrupt the
	// shared master, which is why a plain hard link is not acceptable here.
	if err := os.WriteFile(hit.Path, []byte("mutated"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(body) {
		t.Fatalf("cached master changed through the staged file: %q", after)
	}
}
