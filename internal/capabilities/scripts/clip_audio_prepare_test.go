package scriptgeneration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/clips"
)

type clipAudioPrepareStub struct{ path string }

func (s clipAudioPrepareStub) ResolveClipAudioAsset(context.Context, string) (capabilityaudio.ResolvedAudioAsset, error) {
	return capabilityaudio.ResolvedAudioAsset{Path: s.path}, nil
}

func TestPrepareClipAudioAssetsMaterializesMissingPath(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "clip-*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	result := &GenerateResult{Scenes: []Scene{{ID: "scene-0", Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioClip, ClipAssetID: "clip-1"}, Clip: &ClipReference{ID: "clip-1"}}}}
	ms, err := prepareClipAudioAssets(context.Background(), result, clipAudioPrepareStub{path: f.Name()}, capabilityaudio.MixVoiceoverWithDuckedClip)
	if err != nil || ms < 0 || result.Scenes[0].Clip.AudioPath != f.Name() {
		t.Fatalf("prepare clip audio: err=%v ms=%d path=%q", err, ms, result.Scenes[0].Clip.AudioPath)
	}
}

func TestPrepareClipAudioAssetsSkipsVoiceoverOnly(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{{ID: "scene-0", Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioClip, ClipAssetID: "clip-1"}, Clip: &ClipReference{ID: "clip-1"}}}}
	if _, err := prepareClipAudioAssets(context.Background(), result, nil, capabilityaudio.MixVoiceoverOnly); err != nil {
		t.Fatal(err)
	}
}

type clipAudioPrepareFunc func(context.Context, string) (capabilityaudio.ResolvedAudioAsset, error)

func (f clipAudioPrepareFunc) ResolveClipAudioAsset(ctx context.Context, clipID string) (capabilityaudio.ResolvedAudioAsset, error) {
	return f(ctx, clipID)
}

// TestPrepareClipAudioAssetsResolvesIndependentClipsConcurrently pins the
// fan-out: every clip resolution must start before ANY of them is released.
// A serial implementation starts only the first resolver, so this test fails
// on the start barrier instead of relying on a wall-clock threshold.
func TestPrepareClipAudioAssetsResolvesIndependentClipsConcurrently(t *testing.T) {
	const clipCount = 6
	dir := t.TempDir()
	release := make(chan struct{})
	started := make(chan struct{}, clipCount)
	source := clipAudioPrepareFunc(func(ctx context.Context, clipID string) (capabilityaudio.ResolvedAudioAsset, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return capabilityaudio.ResolvedAudioAsset{}, ctx.Err()
		}
		path := filepath.Join(dir, clipID+".m4a")
		if err := os.WriteFile(path, []byte("clip-audio"), 0o600); err != nil {
			return capabilityaudio.ResolvedAudioAsset{}, err
		}
		return capabilityaudio.ResolvedAudioAsset{Path: path, DurationUS: 1_000_000}, nil
	})

	result := &GenerateResult{}
	for i := 0; i < clipCount; i++ {
		clipID := fmt.Sprintf("clip-%d", i)
		result.Scenes = append(result.Scenes, Scene{
			ID:    fmt.Sprintf("scene-%d", i),
			Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioClip, ClipAssetID: clipID},
			Clip:  &ClipReference{ID: clipID},
		})
	}

	done := make(chan error, 1)
	go func() {
		_, err := prepareClipAudioAssets(context.Background(), result, source, capabilityaudio.MixVoiceoverWithDuckedClip)
		done <- err
	}()
	for i := 0; i < clipCount; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			close(release)
			<-done
			t.Fatalf("only %d of %d clip resolutions started; clip preparation is still serial", i, clipCount)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("prepare clip audio: %v", err)
	}
	for i := range result.Scenes {
		if result.Scenes[i].Clip.AudioPath == "" {
			t.Fatalf("scene %s clip %s was left without a materialized path", result.Scenes[i].ID, result.Scenes[i].Clip.ID)
		}
	}
}

// One producer, two consumers: the master audio phase materializes the clip
// once and the overlay branch reuses the same facts. The resolver call counter
// is the proof — a second materialization would increment it.
func TestClipAudioLatch_ResolvesClipsOnceForBothBranches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip-1.m4a")
	if err := os.WriteFile(path, []byte("clip-audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	source := clipAudioPrepareFunc(func(context.Context, string) (capabilityaudio.ResolvedAudioAsset, error) {
		calls++
		return capabilityaudio.ResolvedAudioAsset{Path: path, DurationUS: 1_000_000}, nil
	})
	newDocument := func() *GenerateResult {
		return &GenerateResult{Scenes: []Scene{{
			ID:    "scene-0",
			Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioClip, ClipAssetID: "clip-1"},
			Clip:  &ClipReference{ID: "clip-1"},
		}}}
	}
	master, branch := newDocument(), newDocument()

	latch := clips.NewClipAudioLatch()
	if _, err := latch.Produce(context.Background(), master, source, capabilityaudio.MixVoiceoverWithDuckedClip); err != nil {
		t.Fatalf("produce: %v", err)
	}
	if err := latch.ApplyTo(branch); err != nil {
		t.Fatalf("branch apply: %v", err)
	}
	if calls != 1 {
		t.Fatalf("resolver calls = %d, want 1 (the branch must reuse the snapshot)", calls)
	}
	if master.Scenes[0].Clip.AudioPath != path || branch.Scenes[0].Clip.AudioPath != path {
		t.Fatalf("audio paths = %q / %q, want the same materialized path %q",
			master.Scenes[0].Clip.AudioPath, branch.Scenes[0].Clip.AudioPath, path)
	}
}
