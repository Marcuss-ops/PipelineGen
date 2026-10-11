package overlays

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func deadAirItems() []OverlayItem {
	mk := func(id string, startMS, endMS int64) OverlayItem {
		return OverlayItem{ID: id, TemplateID: "QUOTE", Text: id,
			StartMs: startMS, EndMs: endMS,
			StartUS: startMS * 1000, DurationUS: (endMS - startMS) * 1000}
	}
	return []OverlayItem{
		mk("a", 2000, 6000),
		mk("b", 5000, 9000), // overlaps a → merged coverage 2s..9s
		mk("c", 15000, 18000),
	}
}

func TestFindDeadAirGapsBasic(t *testing.T) {
	windows := []SceneWindow{{SceneID: "s1", StartUS: 0, EndUS: 20_000_000}}
	got := FindDeadAirGaps(windows, deadAirItems(), 3_000_000)
	require.Len(t, got, 1, "0..2s and 18s..20s are below the 3s threshold; only 9s..15s qualifies")
	assert.Equal(t, int64(9_000_000), got[0].StartUS)
	assert.Equal(t, int64(15_000_000), got[0].EndUS)
	assert.Equal(t, int64(6000), got[0].DurationMS)
	all := FindDeadAirGaps(windows, deadAirItems(), 2_000_000)
	require.Len(t, all, 3, "at 2s the leading and trailing stretches qualify too")
	assert.Equal(t, int64(0), all[0].StartUS)
	assert.Equal(t, int64(18_000_000), all[2].StartUS)
}

func TestFindDeadAirGapsThresholdRespected(t *testing.T) {
	windows := []SceneWindow{{SceneID: "s1", StartUS: 0, EndUS: 20_000_000}}
	assert.Empty(t, FindDeadAirGaps(windows, deadAirItems(), 7_000_000), "no gap reaches 7s")
	assert.Empty(t, FindDeadAirGaps(windows, deadAirItems(), 0), "non-positive threshold disables detection")
	assert.Empty(t, FindDeadAirGaps(windows, deadAirItems(), -1))
}

func TestFindDeadAirGapsFullyCoveredAndEmpty(t *testing.T) {
	full := []OverlayItem{{ID: "x", TemplateID: "QUOTE", Text: "x", StartMs: 0, EndMs: 10000, StartUS: 0, DurationUS: 10_000_000}}
	windows := []SceneWindow{{SceneID: "s1", StartUS: 0, EndUS: 10_000_000}}
	assert.Empty(t, FindDeadAirGaps(windows, full, 1_000_000), "fully covered scene has no gaps")
	assert.Empty(t, FindDeadAirGaps(nil, full, 1_000_000))
	assert.Empty(t, FindDeadAirGaps([]SceneWindow{{SceneID: "", StartUS: 0, EndUS: 5}}, full, 1), "invalid windows never invent gaps")
	naked := FindDeadAirGaps(windows, nil, 1_000_000)
	require.Len(t, naked, 1, "no items → whole window is one gap")
	assert.Equal(t, int64(10_000_000), naked[0].EndUS-naked[0].StartUS)
}

func TestSceneCoverageFractions(t *testing.T) {
	windows := []SceneWindow{
		{SceneID: "s1", StartUS: 0, EndUS: 20_000_000},
		{SceneID: "s2", StartUS: 20_000_000, EndUS: 40_000_000},
	}
	got := SceneCoverage(windows, deadAirItems())
	// s1: covered 2..9 (7s) + 15..18 (3s) = 10s of 20s → 0.5; s2: naked → 0.
	assert.Equal(t, 0.5, got["s1"])
	assert.Equal(t, 0.0, got["s2"])
	_, ok := got[""]
	assert.False(t, ok)
}
