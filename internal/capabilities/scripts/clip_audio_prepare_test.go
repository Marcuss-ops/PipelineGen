package scriptgeneration

import (
	"context"
	"os"
	"testing"

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
