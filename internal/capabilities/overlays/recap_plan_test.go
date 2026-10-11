package overlays

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func recapDemoSources() []RecapSource {
	return []RecapSource{
		{ID: "ch1", Title: "Nel 2020 fu annunciato il progetto", SentenceIDs: []int{0}, Score: 0.85},
		{ID: "ch2", Title: "L'aumento dei costi ha ridotto i margini", SentenceIDs: []int{1}, Score: 0.95},
		{ID: "ch3", Title: "La crisi non è stata causata dall'inflazione", SentenceIDs: []int{2}, Score: 0.90},
		{ID: "ch4", Title: "Nel 2024 il progetto venne completato", SentenceIDs: []int{3}, Score: 0.99},
	}
}

func TestBuildRecapPlanItemsLiveShape(t *testing.T) {
	got := BuildRecapPlanItems("plan-live", []RecapPlacement{{
		SceneID: "scene-04", WindowStartUS: 60_000_000, WindowEndUS: 90_000_000,
		Sources: recapDemoSources(), NarratedThrough: 2, MaxCards: 3,
	}})
	require.Len(t, got, 3, "three narrated chapters → three cards")
	// Score order, verbatim titles (negation intact).
	assert.Equal(t, "L'aumento dei costi ha ridotto i margini", got[0].Text)
	assert.Equal(t, "La crisi non è stata causata dall'inflazione", got[1].Text)
	// Certified wire shape: quote primitive with preset + motion.
	for _, it := range got {
		assert.Equal(t, "quote", it.Kind)
		assert.Equal(t, "QUOTE", it.TemplateID)
		assert.NotEmpty(t, it.PresetID, "QUOTE requires a preset at the worker")
		assert.NotEmpty(t, it.MotionID)
		assert.Equal(t, "scene-04", it.SceneID)
		assert.Greater(t, it.EndMs, it.StartMs)
		assert.Equal(t, it.StartMs, it.StartUS/1000, "ms projection floors start_us")
		assert.Equal(t, it.EndMs, (it.StartUS+it.DurationUS+999)/1000, "ms projection ceils end_us")
	}
	// Sequential, non-overlapping, inside the window.
	for i := 1; i < len(got); i++ {
		assert.GreaterOrEqual(t, got[i].StartUS, got[i-1].StartUS+got[i-1].DurationUS)
	}
	assert.GreaterOrEqual(t, got[0].StartUS, int64(60_000_000))
	assert.LessOrEqual(t, got[2].StartUS+got[2].DurationUS, int64(90_000_000))
	// Deterministic.
	again := BuildRecapPlanItems("plan-live", []RecapPlacement{{
		SceneID: "scene-04", WindowStartUS: 60_000_000, WindowEndUS: 90_000_000,
		Sources: recapDemoSources(), NarratedThrough: 2, MaxCards: 3,
	}})
	assert.Equal(t, got, again)
}

func TestBuildRecapPlanItemsNeverLeaksFuture(t *testing.T) {
	got := BuildRecapPlanItems("p", []RecapPlacement{{
		SceneID: "s", WindowStartUS: 0, WindowEndUS: 60_000_000,
		Sources: recapDemoSources(), NarratedThrough: 2, MaxCards: 5,
	}})
	for _, it := range got {
		assert.NotContains(t, it.Text, "2024", "future chapter must not leak despite top score")
	}
}

func TestBuildRecapPlanItemsWindowGuards(t *testing.T) {
	src := recapDemoSources()
	// Invalid window → no items, never guessed timing.
	assert.Empty(t, BuildRecapPlanItems("p", []RecapPlacement{{
		SceneID: "s", WindowStartUS: 10, WindowEndUS: 10, Sources: src, NarratedThrough: 2,
	}}))
	assert.Empty(t, BuildRecapPlanItems("p", []RecapPlacement{{
		SceneID: "", WindowStartUS: 0, WindowEndUS: 60_000_000, Sources: src, NarratedThrough: 2,
	}}))
	// Tiny window → leftover stays empty rather than squeezing.
	assert.Empty(t, BuildRecapPlanItems("p", []RecapPlacement{{
		SceneID: "s", WindowStartUS: 0, WindowEndUS: 1_000_000, Sources: src, NarratedThrough: 2,
	}}))
	// Short window fits exactly one card; nothing overflows.
	one := BuildRecapPlanItems("p", []RecapPlacement{{
		SceneID: "s", WindowStartUS: 0, WindowEndUS: 5_000_000, Sources: src, NarratedThrough: 2, MaxCards: 3,
	}})
	require.Len(t, one, 1)
	assert.LessOrEqual(t, one[0].StartUS+one[0].DurationUS, int64(5_000_000))
}

func TestBuildRecapPlanItemsUniqueIDs(t *testing.T) { // "A—B" (em-dash) and "A-B" (hyphen) differ after normalization
	// (em-dash survives, hyphen is dropped) yet slugify to the same id.
	src := []RecapSource{
		{ID: "a", Title: "Costi — su", SentenceIDs: []int{0}, Score: 0.9},
		{ID: "b", Title: "Costi!su", SentenceIDs: []int{1}, Score: 0.8},
	}
	got := BuildRecapPlanItems("p", []RecapPlacement{{
		SceneID: "s", WindowStartUS: 0, WindowEndUS: 60_000_000, Sources: src, NarratedThrough: 1,
	}})
	require.Len(t, got, 2)
	assert.NotEqual(t, got[0].ID, got[1].ID, "slug collisions stay unique for plan validation")
}
