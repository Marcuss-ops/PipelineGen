package scriptgeneration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
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
