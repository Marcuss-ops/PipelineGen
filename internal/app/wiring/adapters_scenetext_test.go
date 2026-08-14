package wiring

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type scenetextClipResolver struct {
	clip *asset.Asset
}

func (r scenetextClipResolver) ResolveByMediaAssetID(context.Context, string) (*asset.Asset, error) {
	return r.clip, nil
}

func TestSceneTextGeneratorConvertScenesEnrichesCanonicalClipFields(t *testing.T) {
	clip := &asset.Asset{ID: "clip-1", Duration: 6500 * time.Millisecond}
	clip.SetLocalPath("/var/lib/pipelinegen/clips/clip-1.mp4")
	clip.SetFileHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	generator := &SceneTextGenerator{ClipAssets: scenetextClipResolver{clip: clip}}
	result := &usecase.EngineResult{Output: scriptpkg.ModelScriptOutputV1{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
			ID: "scene-1", Index: 0, Text: "clip narration",
			Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{
				ClipID: "clip-1", StartMs: 1200, EndMs: 4200,
			}},
		}}},
	}}

	scenes, err := generator.convertScenes(context.Background(), result, scriptgen.Language("en"), capabilityaudio.AudioModeNone, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 1 || scenes[0].Clip == nil {
		t.Fatalf("expected one enriched clip scene: %#v", scenes)
	}
	got := scenes[0].Clip
	if got.Path != "/var/lib/pipelinegen/clips/clip-1.mp4" || got.SHA256 == "" || got.AudioPath != got.Path || got.Duration != 6.5 || got.SourceInMS != 1200 || got.SourceOutMS != 4200 {
		t.Fatalf("canonical clip enrichment incomplete: %#v", got)
	}
}

func TestSceneTextGeneratorConvertScenesPreservesAllClipBindings(t *testing.T) {
	clip := &asset.Asset{ID: "registry-asset", Duration: 2 * time.Second}
	clip.SetLocalPath("/var/lib/pipelinegen/clips/clip.mp4")
	clip.SetFileHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	generator := &SceneTextGenerator{ClipAssets: scenetextClipResolver{clip: clip}}
	result := &usecase.EngineResult{Output: scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
		ID: "scene-1", Index: 0, Text: "multi clip",
		Bindings: scriptpkg.SceneBindings{Clips: []scriptpkg.ClipBinding{{ClipID: "clip-a"}, {ClipID: "clip-b"}}},
	}}}}}
	scenes, err := generator.convertScenes(context.Background(), result, scriptgen.Language("en"), capabilityaudio.AudioModeCombinedTimeline, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 1 || len(scenes[0].Clips) != 2 || scenes[0].Clips[0].ID != "clip-a" || scenes[0].Clips[1].ID != "clip-b" {
		t.Fatalf("multi-clip bindings were not preserved: %#v", scenes)
	}
	if scenes[0].Clip != scenes[0].Clips[0] || len(scenes[0].AudioIntents) != 2 {
		t.Fatalf("primary alias or audio intents lost: %#v", scenes[0])
	}
}
