package register

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExpandClipsBySegments_ExactMultiple(t *testing.T) {
	got, err := expandClipsBySegments([]RegisterFromYouTubeRequest{{
		URL:               "https://www.youtube.com/watch?v=9u4T_o3FxOU",
		Name:              "Clip",
		Start:             0,
		End:               10,
		SecondsPerSegment: 5,
	}})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, 0.0, got[0].Start)
	require.Equal(t, 5.0, got[0].End)
	require.Equal(t, 5.0, got[1].Start)
	require.Equal(t, 10.0, got[1].End)
	require.Zero(t, got[1].SecondsPerSegment, "children must strip the field to prevent recursive expansion")
	require.Equal(t, "Clip (part 1)", got[0].Name)
	require.Equal(t, "Clip (part 2)", got[1].Name)
}

func TestExpandClipsBySegments_RejectsMissingEnd(t *testing.T) {
	_, err := expandClipsBySegments([]RegisterFromYouTubeRequest{{
		URL:               "https://www.youtube.com/watch?v=9u4T_o3FxOU",
		Name:              "Clip",
		Start:             10,
		End:               0,
		SecondsPerSegment: 5,
	}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "explicit end")
}

func TestExpandClipsBySegments_RejectsRemainder(t *testing.T) {
	_, err := expandClipsBySegments([]RegisterFromYouTubeRequest{{
		URL:               "https://www.youtube.com/watch?v=9u4T_o3FxOU",
		Name:              "Clip",
		Start:             0,
		End:               10,
		SecondsPerSegment: 3,
	}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not an exact multiple")
	require.Contains(t, err.Error(), "remainder")
}

func TestExpandClipsBySegments_NoSegmentationPassesThrough(t *testing.T) {
	got, err := expandClipsBySegments([]RegisterFromYouTubeRequest{{
		URL:   "https://www.youtube.com/watch?v=9u4T_o3FxOU",
		Name:  "Whole window",
		Start: 12,
		End:   84,
	}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, 12.0, got[0].Start)
	require.Equal(t, 84.0, got[0].End)
	require.True(t, strings.HasPrefix(got[0].Name, "Whole window"))
}
