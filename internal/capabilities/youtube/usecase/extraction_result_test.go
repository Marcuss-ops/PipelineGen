package usecase

import (
	"testing"

	"github.com/stretchr/testify/require"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

func TestAggregateFanOutStats_UnknownStatusFailsClosed(t *testing.T) {
	items := []youtubetypes.ExtractItem{
		{Status: "processed"},
		{Status: ""},
		{Status: "unexpected"},
	}

	got := aggregateFanOutStats(items)

	require.Equal(t, 3, got.Requested)
	require.Equal(t, 1, got.Processed)
	require.Equal(t, 0, got.Skipped)
	require.Equal(t, 2, got.Failed)
}
