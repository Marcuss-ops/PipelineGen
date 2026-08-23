package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type finalPassItTranslator struct {
	calls int
}

func (m *finalPassItTranslator) Translate(_ context.Context, text, _ string) (string, error) {
	m.calls++
	return "[it] " + text, nil
}

func makePacquiaoBronerFinalPassSpecScene() (*scriptpkg.ModelScriptOutputV1, *scriptpkg.ClipEvidence) {
	const (
		clip1 = "yt_vdC5GXxS-qU_65_80_v1"
		clip2 = "yt_vdC5GXxS-qU_146_155_v1"
		clip3 = "yt_vdC5GXxS-qU_193_205_v1"
	)

	scenes := []scriptpkg.SpecScene{
		{
			ID:    "scene-0",
			Index: 0,
			Text:  "Pacquiao talks about Mayweather in Japan.",
			Title: "Opening",
			Kind:  scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{
					ClipID:    clip1,
					ClipTitle: "Pacquiao talks about Mayweather in Japan",
					DriveLink: "https://drive.google.com/file/d/" + clip1 + "/view",
				},
				Image: &scriptpkg.ImageBinding{
					ImageID: "img-" + clip1,
					Prompt:  "Visual for scene 0",
					URL:     "https://storage.example.com/" + clip1 + ".png",
					Status:  "generated",
				},
			},
		},
		{
			ID:    "scene-1",
			Index: 1,
			Text:  "Broner tells everyone not to worry about Floyd.",
			Title: "Middle",
			Kind:  scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{
					ClipID:    clip2,
					ClipTitle: "Broner tells everyone not to worry about Floyd",
					DriveLink: "https://drive.google.com/file/d/" + clip2 + "/view",
				},
				Image: &scriptpkg.ImageBinding{
					ImageID: "img-" + clip2,
					Prompt:  "Visual for scene 1",
					URL:     "https://storage.example.com/" + clip2 + ".png",
					Status:  "generated",
				},
			},
		},
		{
			ID:    "scene-2",
			Index: 2,
			Text:  "Broner jokes about hood support.",
			Title: "Closing",
			Kind:  scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{
					ClipID:    clip3,
					ClipTitle: "Broner jokes about hood support",
					DriveLink: "https://drive.google.com/file/d/" + clip3 + "/view",
				},
				Image: &scriptpkg.ImageBinding{
					ImageID: "img-" + clip3,
					Prompt:  "Visual for scene 2",
					URL:     "https://storage.example.com/" + clip3 + ".png",
					Status:  "generated",
				},
			},
		},
	}

	in := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Pacquiao vs Adrien Broner: three aligned clips.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  scenes,
		},
	}

	clipIDs := []string{clip1, clip2, clip3}
	evidence := &scriptpkg.ClipEvidence{AcceptedClipIDs: clipIDs}
	return in, evidence
}

func TestSpecSceneGemma_FinalPass_ThreeAlignedAssets(t *testing.T) {
	const (
		clip1 = "yt_vdC5GXxS-qU_65_80_v1"
		clip2 = "yt_vdC5GXxS-qU_146_155_v1"
		clip3 = "yt_vdC5GXxS-qU_193_205_v1"
	)

	t.Run("gemma_scene_plan_uses_canonical_clip_order", func(t *testing.T) {
		plan := &scriptpkg.ClipPrePlan{
			Version: 1,
			Title:   "Pacquiao vs Adrien Broner",
			Slots: []scriptpkg.ClipSearchSlot{
				{Ref: "scene-0", Topic: "Pacquiao talks about Mayweather in Japan", TargetDurationMs: 15000},
				{Ref: "scene-1", Topic: "Broner says not to worry about Floyd", TargetDurationMs: 9000},
				{Ref: "scene-2", Topic: "Broner jokes about hood support", TargetDurationMs: 12000},
			},
		}

		res := ports.ClipSamplerResult{
			ClipIDs: []string{clip1, clip2, clip3},
		}

		scenes := usecase.BuildGemmaScenePlan(res, plan, 900)
		require.Len(t, scenes, 3)

		assert.Equal(t, clip1, scenes[0].SelectedClipRef)
		assert.Equal(t, clip2, scenes[1].SelectedClipRef)
		assert.Equal(t, clip3, scenes[2].SelectedClipRef)
		assert.Equal(t, "scene-0", scenes[0].SlotRef)
		assert.Equal(t, "scene-1", scenes[1].SlotRef)
		assert.Equal(t, "scene-2", scenes[2].SlotRef)
		assert.Empty(t, scenes[0].TranscribedText)
		assert.Empty(t, scenes[1].TranscribedText)
		assert.Empty(t, scenes[2].TranscribedText)
	})

	t.Run("translated_specscene_preserves_three_asset_bindings", func(t *testing.T) {
		in, evidence := makePacquiaoBronerFinalPassSpecScene()
		tr := &finalPassItTranslator{}

		out, warnings, err := usecase.TranslateScriptSpec(
			context.Background(),
			in,
			evidence,
			"it",
			tr.Translate,
		)
		require.NoError(t, err)
		require.NotNil(t, out)
		require.NotNil(t, warnings)
		require.Len(t, out.SpecScene.Scenes, 3)

		wantClipIDs := []string{clip1, clip2, clip3}
		for i, sc := range out.SpecScene.Scenes {
			require.NotNil(t, sc.Bindings.Clip)
			assert.Equal(t, wantClipIDs[i], sc.Bindings.Clip.ClipID)
			assert.Contains(t, sc.Bindings.Clip.DriveLink, wantClipIDs[i])
			assert.Contains(t, sc.Text, "[it] ")
		}

		assert.Equal(t, 10, tr.calls, "TranslateScriptSpec must keep the per-text call surface stable for the 3-scene fixture")
	})
}
