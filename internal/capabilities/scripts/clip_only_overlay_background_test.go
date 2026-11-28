package scriptgeneration

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestSingleClipOverlayBackgroundUsesResolvedClip(t *testing.T) {
	req := GenerateRequest{FinalJob: true, MediaMode: scriptpkg.MediaModeClipOnly}
	result := &GenerateResult{Scenes: []Scene{{
		ID:   "clip-0",
		Clip: &ClipReference{ID: "yt_clip_1", Path: "data/media/clips/a.mp4", SHA256: "abc"},
	}}}

	got := singleClipOverlayBackground(req, result)
	if got == nil {
		t.Fatal("single-clip clip_only final job did not derive an overlay background")
	}
	if got.Kind != "video" || got.AssetID != "yt_clip_1" || got.LocalPath != "data/media/clips/a.mp4" ||
		got.SHA256 != "abc" || got.MediaType != "video/mp4" || got.Fit != "cover" || !got.Loop {
		t.Fatalf("overlay background = %+v, want the resolved clip", got)
	}
}

func TestSingleClipOverlayBackgroundRejectsMultipleClips(t *testing.T) {
	req := GenerateRequest{FinalJob: true, MediaMode: scriptpkg.MediaModeClipOnly}
	result := &GenerateResult{Scenes: []Scene{
		{ID: "clip-0", Clip: &ClipReference{ID: "yt_clip_1", Path: "a.mp4", SHA256: "a"}},
		{ID: "clip-1", Clip: &ClipReference{ID: "yt_clip_2", Path: "b.mp4", SHA256: "b"}},
	}}

	if got := singleClipOverlayBackground(req, result); got != nil {
		t.Fatalf("multi-clip overlay background = %+v, want nil", got)
	}
}

func TestSingleClipOverlayBackgroundRequiresFinalClipOnly(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{{
		ID:   "clip-0",
		Clip: &ClipReference{ID: "yt_clip_1", Path: "a.mp4", SHA256: "a"},
	}}}

	for name, req := range map[string]GenerateRequest{
		"non-final": {MediaMode: scriptpkg.MediaModeClipOnly},
		"stock":     {FinalJob: true, MediaMode: scriptpkg.MediaModeStockOnly},
	} {
		if got := singleClipOverlayBackground(req, result); got != nil {
			t.Fatalf("%s overlay background = %+v, want nil", name, got)
		}
	}
}

func TestSceneOverlayBackgroundsMapsVerifiedMultiClipScenesIndependently(t *testing.T) {
	req := GenerateRequest{FinalJob: true, MediaMode: scriptpkg.MediaModeClipOnly}
	result := &GenerateResult{Scenes: []Scene{
		{ID: "scene-01", Clip: &ClipReference{ID: "yt-1", Path: "/clips/one.mp4", SHA256: "sha-one"}},
		{ID: "scene-02", Clips: []*ClipReference{{ID: "yt-2", Path: "/clips/two.mp4", SHA256: "sha-two"}}},
		{ID: "scene-03", Clip: &ClipReference{ID: "unverified", Path: "/clips/three.mp4"}},
	}}
	got := SceneOverlayBackgrounds(req, result)
	if len(got) != 2 {
		t.Fatalf("background scene count = %d, want 2 verified scenes", len(got))
	}
	for sceneID, wantAsset := range map[string]string{"scene-01": "yt-1", "scene-02": "yt-2"} {
		background := got[sceneID]
		if background == nil || background.Kind != "video" || len(background.AssetRefs) != 1 || background.AssetRefs[0].AssetID != wantAsset {
			t.Fatalf("background[%q] = %+v, want video asset %q", sceneID, background, wantAsset)
		}
	}
	if _, exists := got["scene-03"]; exists {
		t.Fatal("unverified clip must not become a scene background")
	}
}
