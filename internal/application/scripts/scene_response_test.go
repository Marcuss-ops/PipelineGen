package scripts

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/stretchr/testify/require"
)

func TestBuildNormalizedScenes_MergesTextImagesAndVideos(t *testing.T) {
	clipScenes := []ClipScene{
		{
			SceneIndex: 0,
			Text:       "Clip scene one",
			DriveLink:  "https://drive.google.com/file/d/clip-1/view",
			Kind:       "clip",
		},
		{
			SceneIndex: 1,
			Text:       "Text only scene",
			Kind:       "clip",
		},
		{
			SceneIndex: 2,
			Kind:       "clip",
		},
	}
	sceneImages := []SceneImage{
		{
			Index: 0,
			Text:  "Image scene one",
			URL:   "https://drive.google.com/file/d/image-1/view",
		},
	}

	got := buildNormalizedScenes(clipScenes, sceneImages)
	require.Len(t, got, 2)
	require.Equal(t, "Clip scene one", got[0]["text"])
	require.Equal(t, "https://drive.google.com/file/d/clip-1/view", got[0]["video"])
	require.Equal(t, []string{"https://drive.google.com/file/d/clip-1/view"}, got[0]["videos"])
	require.Equal(t, "https://drive.google.com/file/d/image-1/view", got[0]["image"])
	require.Equal(t, []string{"https://drive.google.com/file/d/image-1/view"}, got[0]["images"])

	require.Equal(t, "Text only scene", got[1]["text"])
	_, hasVideo := got[1]["video"]
	require.False(t, hasVideo)
	_, hasImage := got[1]["image"]
	require.False(t, hasImage)
}

func TestBuildFinalResult_PublishesScenesJSON(t *testing.T) {
	pu := &PipelineUseCase{}
	result := pu.buildFinalResult(
		&scriptpkg.GenerationSpec{Title: "Jackie Chan Interview", Language: "en", MaxChars: 200, GenerateSceneImages: true},
		&ClipSourcePathResult{
			WriteResult: &WriteScriptResult{Script: "raw-script", WordCount: 123, CacheStatus: "miss"},
			ClipScenes: []ClipScene{
				{
					SceneIndex: 0,
					Text:       "Scene text",
					DriveLink:  "https://drive.google.com/file/d/clip-2/view",
				},
			},
		},
		"",
		ScriptInsights{},
		nil,
		"",
		"",
		[]SceneImage{
			{
				Index: 0,
				Text:  "Scene text",
				URL:   "https://drive.google.com/file/d/image-2/view",
			},
		},
		nil,
		42,
	)

	require.Equal(t, true, result["ok"])

	scriptItems, ok := result["script"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, scriptItems, 1)
	require.Equal(t, "Scene text", scriptItems[0]["text"])
	require.Equal(t, "https://drive.google.com/file/d/clip-2/view", scriptItems[0]["video"])
	require.Equal(t, []string{"https://drive.google.com/file/d/clip-2/view"}, scriptItems[0]["videos"])
	require.Equal(t, "https://drive.google.com/file/d/image-2/view", scriptItems[0]["image"])
	require.Equal(t, []string{"https://drive.google.com/file/d/image-2/view"}, scriptItems[0]["images"])

	scenes, ok := result["scenes"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, scenes, 1)
	require.Equal(t, scriptItems, scenes)

	scenesJSON, ok := result["scenes_json"].(string)
	require.True(t, ok)
	require.Contains(t, scenesJSON, `"text":"Scene text"`)
	require.Contains(t, scenesJSON, `"video":"https://drive.google.com/file/d/clip-2/view"`)
	require.Contains(t, scenesJSON, `"image":"https://drive.google.com/file/d/image-2/view"`)
}
