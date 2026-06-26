package scripts

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildScenesWithMarkersMapsEveryClip(t *testing.T) {
	pack := map[string]any{
		"clip_ids":   []string{"clip-a", "clip-b"},
		"clip_names": []string{"First source", "Second source"},
	}
	scenes := BuildScenesWithMarkers("The first event changed everything. The second event explains what happened next.", pack)
	require.Len(t, scenes, 2)
	require.Equal(t, "clip-a", scenes[0].ClipID)
	require.Equal(t, "clip-b", scenes[1].ClipID)
	require.Equal(t, "clip", scenes[0].Kind)
	require.NotEmpty(t, scenes[0].Text)
	require.NotEmpty(t, scenes[1].Text)
}
