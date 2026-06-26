package scripts

import (
	"testing"

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

