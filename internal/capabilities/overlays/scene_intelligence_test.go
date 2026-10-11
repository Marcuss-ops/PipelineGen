package overlays

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Comparison: qualitative invents nothing ─────────────────────

func TestSIQualitativeComparisonInventsNoNumbers(t *testing.T) {
	got := ComparisonCandidates("scene-07", []string{"toyota", "ford"}, "split_comparison", nil, []int{42, 43}, 0.8, false)
	require.Len(t, got, 1)
	assert.Equal(t, SICategoryMultiEntity, got[0].Category, "qualitative → split-screen, not numbers")
	assert.Nil(t, got[0].Value)
	assert.Equal(t, []int{42, 43}, got[0].Evidence)
}

func TestSIQuantitativeComparisonCarriesOnlyParsedValues(t *testing.T) {
	got := ComparisonCandidates("s", []string{"a", "b"}, "value_comparison", []float64{100, 94}, []int{7}, 0.9, false)
	require.Len(t, got, 2)
	assert.Equal(t, SICategoryNumber, got[0].Category)
	require.NotNil(t, got[0].Value)
	assert.Equal(t, 100.0, *got[0].Value)
	assert.Equal(t, 94.0, *got[1].Value, "only the values present in text")
}

// ── Cause–effect: dopo ≠ causa ──────────────────────────────────

func TestSICausalKeepsSequenceVsCauseSplit(t *testing.T) {
	seq := CausalCandidates("s", "aumento_prezzi", "vendite_calo", "precedes", []int{3}, 0.7, false)
	cause := CausalCandidates("s", "aumento_prezzi", "vendite_calo", "causes", []int{4}, 0.9, false)
	require.Len(t, seq, 1)
	require.Len(t, cause, 1)
	assert.Contains(t, seq[0].Text, "precedes")
	assert.Contains(t, cause[0].Text, "causes")
	assert.NotEqual(t, seq[0].Text, cause[0].Text)
}

// ── Timeline: chronological, relative unresolved ────────────────

func TestSITimelineLabelsChronological(t *testing.T) {
	got := TimelineLabelCandidates("s",
		[]int{2024, 2020, 2022}, []string{"e2024", "e2020", "e2022"}, []int{0, 1, 2}, 0.9)
	require.Len(t, got, 3)
	assert.Equal(t, "e2020", got[0].Text)
	assert.Equal(t, "e2022", got[1].Text)
	assert.Equal(t, "e2024", got[2].Text, "chronological order despite narration order")
	assert.Empty(t, TimelineLabelCandidates("s", nil, nil, nil, 0.9), "relative-only events yield nothing")
}

// ── Density → budget (narrows only) ─────────────────────────────

func TestSIDensityBudgetNarrowsOnly(t *testing.T) {
	assert.Equal(t, 2, DensityOverlapBudget(0.85, 3), "dense scene: fewer simultaneous overlays")
	assert.Equal(t, 3, DensityOverlapBudget(0.2, 3), "normal scene keeps planner budget")
	assert.Equal(t, 2, DensityOverlapBudget(0.9, 5), "dense scene narrows even a wide planner budget, never widens it")
	assert.Equal(t, DefaultOverlapBudget, DensityOverlapBudget(0.1, 0))
}

// ── Continuity: stable identity, rotating asset ─────────────────

func TestSIContinuityKeepsIdentityAcrossDistantScenes(t *testing.T) {
	states := ResolveContinuity([]ContinuityInput{
		{EntityID: "company-example", SceneID: "scene-02", Assets: []string{"asset-123", "asset-456"}, Style: "corporate_clean", Accent: "brand_secondary"},
		{EntityID: "company-example", SceneID: "scene-09", Assets: []string{"asset-123", "asset-789"}, Style: "other_style", Accent: "other_accent"},
	})
	st := states["company-example"]
	assert.Equal(t, "corporate_clean", st.StyleFamily, "style stays pinned to first resolution")
	assert.Equal(t, "brand_secondary", st.AccentToken)
	assert.Equal(t, "scene-09", st.LastSceneID)
	assert.Equal(t, 2, st.SeenScenes)
	assert.NotEqual(t, "asset-123", st.PreferredAsset, "asset rotates when alternatives are offered")
}

func TestSIV01ThreeScenesCoherentIdentity(t *testing.T) {
	// V01: Toyota in scenes 2, 5, 9 → one coherent visual identity.
	states := ResolveContinuity([]ContinuityInput{
		{EntityID: "toyota", SceneID: "scene-02", Assets: []string{"t1", "t2"}, Style: "corporate_clean", Accent: "red"},
		{EntityID: "toyota", SceneID: "scene-05", Assets: []string{"t1", "t3"}, Style: "corporate_clean", Accent: "red"},
		{EntityID: "toyota", SceneID: "scene-09", Assets: []string{"t1", "t4"}, Style: "corporate_clean", Accent: "red"},
	})
	st := states["toyota"]
	assert.Equal(t, 3, st.SeenScenes)
	assert.Equal(t, "scene-09", st.LastSceneID)
	assert.Equal(t, "corporate_clean", st.StyleFamily, "identity coherent across scenes")
}

func TestSIV03TwoCompaniesNeverConfused(t *testing.T) {
	// V03: different companies keep distinct colors and references.
	states := ResolveContinuity([]ContinuityInput{
		{EntityID: "ford", SceneID: "s1", Assets: []string{"f1"}, Style: "industrial", Accent: "blue"},
		{EntityID: "toyota", SceneID: "s1", Assets: []string{"t1"}, Style: "corporate_clean", Accent: "red"},
		{EntityID: "ford", SceneID: "s2", Assets: []string{"f1", "f2"}, Style: "industrial", Accent: "blue"},
	})
	assert.NotEqual(t, states["ford"].StyleFamily, states["toyota"].StyleFamily)
	assert.NotEqual(t, states["ford"].AccentToken, states["toyota"].AccentToken)
	assert.NotEqual(t, states["ford"].PreferredAsset, states["toyota"].PreferredAsset)
}

func TestSIV04MovedSceneRecalculatesContinuity(t *testing.T) {
	// V04: moving a scene recalculates continuity in the new order —
	// identity stable, positions follow the new narration.
	base := []ContinuityInput{
		{EntityID: "e", SceneID: "s1", Assets: []string{"a1"}, Style: "s", Accent: "x"},
		{EntityID: "e", SceneID: "s2", Assets: []string{"a1", "a2"}, Style: "s", Accent: "x"},
		{EntityID: "e", SceneID: "s3", Assets: []string{"a2", "a3"}, Style: "s", Accent: "x"},
	}
	orig := ResolveContinuity(base)["e"]
	moved := []ContinuityInput{base[2], base[0], base[1]}
	got := ResolveContinuity(moved)["e"]
	assert.Equal(t, orig.StyleFamily, got.StyleFamily, "identity survives the move")
	assert.Equal(t, 3, got.SeenScenes)
	assert.Equal(t, "s2", got.LastSceneID, "continuity follows the new order")
}

func TestSIV02AlternativeAssetsKeepStableIdentity(t *testing.T) {
	states := ResolveContinuity([]ContinuityInput{
		{EntityID: "toyota", SceneID: "scene-02", Assets: []string{"asset-a", "asset-b"}, Style: "corporate", Accent: "red"},
		{EntityID: "toyota", SceneID: "scene-05", Assets: []string{"asset-b", "asset-c"}, Style: "different-style", Accent: "different-accent"},
	})
	state := states["toyota"]
	assert.Equal(t, "corporate", state.StyleFamily, "alternate assets must retain the pinned entity identity")
	assert.Equal(t, "red", state.AccentToken)
	assert.Equal(t, "asset-b", state.PreferredAsset, "an available alternative can rotate without changing entity identity")
	assert.Equal(t, 2, state.SeenScenes)
}

func TestSIV05MissingAssetFallsBackValid(t *testing.T) {
	// V05: previously preferred asset unavailable → valid fallback,
	// never a broken reference.
	states := ResolveContinuity([]ContinuityInput{
		{EntityID: "e", SceneID: "s1", Assets: []string{"gone"}, Style: "s", Accent: "x"},
		{EntityID: "e", SceneID: "s2", Assets: []string{"fresh-a", "fresh-b"}, Style: "s", Accent: "x"},
	})
	st := states["e"]
	assert.NotEmpty(t, st.PreferredAsset, "always a resolvable asset")
	assert.NotEqual(t, "gone", st.PreferredAsset)
	assert.Contains(t, []string{"fresh-a", "fresh-b"}, st.PreferredAsset)
}

func TestSIV06TenScenesRotateAmongEquivalents(t *testing.T) {
	// V06: ten scenes on one subject with equivalent alternatives →
	// rotation avoids excess repetition while identity stays pinned.
	var apps []ContinuityInput
	for i := 0; i < 10; i++ {
		apps = append(apps, ContinuityInput{
			EntityID: "star", SceneID: "s" + string(rune('0'+i)),
			Assets: []string{"a1", "a2", "a3"}, Style: "portrait", Accent: "gold",
		})
	}
	full := ResolveContinuity(apps)["star"]
	assert.Equal(t, "portrait", full.StyleFamily, "identity pinned over ten scenes")
	assert.Equal(t, 10, full.SeenScenes)
	direct := ResolveContinuity([]ContinuityInput{
		{EntityID: "x", SceneID: "s1", Assets: []string{"a1", "a2"}, Style: "s", Accent: "c"},
		{EntityID: "x", SceneID: "s2", Assets: []string{"a2", "a1"}, Style: "s", Accent: "c"},
	})["x"]
	assert.Equal(t, "a2", direct.PreferredAsset, "equivalent alternative preferred over repetition")
}

func TestSIContinuityEmptyAssetsKeepIdentity(t *testing.T) {
	states := ResolveContinuity([]ContinuityInput{
		{EntityID: "e", SceneID: "s1", Assets: []string{"a1"}, Style: "s", Accent: "a"},
		{EntityID: "e", SceneID: "s2"},
	})
	assert.Equal(t, "a1", states["e"].PreferredAsset, "empty lists never clear identity")
	assert.Equal(t, "s", states["e"].StyleFamily)
	sorted := ContinuityStatesSorted(states)
	require.Len(t, sorted, 1)
}

// ── Recap: only covered content ─────────────────────────────────

func TestSINegatedComparisonNeverPositive(t *testing.T) {
	got := ComparisonCandidates("s", []string{"ford", "toyota"}, "value_comparison", []float64{5}, []int{1}, 0.8, true)
	require.Len(t, got, 1)
	assert.Equal(t, SICategoryMultiEntity, got[0].Category, "negated quantitative degrades to split-screen")
	assert.Nil(t, got[0].Value)
	assert.Contains(t, got[0].Text, "[negated]", "negation tag preserved")
}

func TestSIHypotheticalCausalGated(t *testing.T) {
	got := CausalCandidates("s", "costi", "margini", "causes", []int{1}, 0.9, true)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Text, "[hypothetical]")
	assert.Equal(t, 0.45, got[0].Score, "hypothetical score halved for the renderer gate")
}

func TestSIR01R02PlacementPoints(t *testing.T) {
	// R01: long video with chapters → useful recap points; R02: short
	// single-topic video → no recap at all.
	assert.Nil(t, RecapPlacementPoints(1, false))
	assert.Nil(t, RecapPlacementPoints(2, true), "short video may have no recap")
	got := RecapPlacementPoints(5, false)
	require.NotEmpty(t, got, "long video identifies recap points")
	for _, p := range got {
		assert.GreaterOrEqual(t, p, 0)
		assert.Less(t, p, 5)
	}
	withShift := RecapPlacementPoints(5, true)
	assert.GreaterOrEqual(t, len(withShift), len(got), "thematic turn adds a point")
	assert.Equal(t, withShift, RecapPlacementPoints(5, true), "deterministic")
}

func TestSIR03RecapNeverLeaksLaterChapters(t *testing.T) {
	// R03 + crucial test: a recap boundary is explicit and sources from
	// later narration indices are rejected, including mixed-span sources.
	sources := []RecapSource{
		{ID: "ch1", Title: "Cause", SentenceIDs: []int{1, 2}, Score: 0.9},
		{ID: "ch2", Title: "Costi", SentenceIDs: []int{3, 4}, Score: 0.8},
		{ID: "ch3", Title: "Effetti", SentenceIDs: []int{5, 6}, Score: 0.7},
		{ID: "ch4", Title: "Sviluppo successivo", SentenceIDs: []int{7, 8}, Score: 1.0},
		{ID: "mixed", Title: "Copertura mista", SentenceIDs: []int{5, 9}, Score: 2.0},
	}
	got := BuildRecapCandidatesThrough(sources, 5, 6)
	require.Len(t, got, 3)
	for _, c := range got {
		for _, sentenceID := range c.SentenceIDs {
			assert.LessOrEqual(t, sentenceID, 6, "no sentence from chapters after the recap point")
		}
		assert.NotContains(t, c.SourceIDs, "ch4")
		assert.NotContains(t, c.SourceIDs, "mixed", "mixed temporal provenance is rejected whole")
	}
	assert.Empty(t, BuildRecapCandidatesThrough(sources, 3, -1), "an invalid recap boundary cannot admit content")
}

func TestSIR04EquivalentBulletsDeduplicated(t *testing.T) {
	// R04: two equivalent bullets → one card, highest score wins.
	got := BuildRecapCandidates([]RecapSource{
		{ID: "b1", Title: "Cause della crisi", SentenceIDs: []int{1}, Score: 0.7},
		{ID: "b2", Title: "cause della crisi!", SentenceIDs: []int{2}, Score: 0.9},
	}, 3)
	require.Len(t, got, 1, "equivalent bullets collapse")
	assert.Equal(t, "cause della crisi!", got[0].Title)
}

func TestSIR05NegationsPreservedVerbatim(t *testing.T) {
	// R05: negations and attributions survive verbatim into the recap.
	got := BuildRecapCandidates([]RecapSource{
		{ID: "b1", Title: "La crisi non è stata causata dall'inflazione", SentenceIDs: []int{1}, Score: 0.9},
		{ID: "b2", Title: "Secondo gli analisti, i margini crolleranno", SentenceIDs: []int{2}, Score: 0.8},
	}, 3)
	require.Len(t, got, 2)
	assert.Contains(t, got[0].Title, "non è stata causata")
	assert.Contains(t, got[1].Title, "Secondo gli analisti")
}

func TestSIR06ChapterEditInvalidatesDependents(t *testing.T) {
	// R06: editing an earlier chapter invalidates recaps built on it.
	cands := BuildRecapCandidates([]RecapSource{
		{ID: "ch2", Title: "Costi", SentenceIDs: []int{3}, Score: 0.9},
		{ID: "ch4", Title: "Ripresa", SentenceIDs: []int{9}, Score: 0.8},
	}, 3)
	require.Len(t, cands, 2)
	assert.True(t, RecapInvalidatedBy(cands[0], []string{"ch2"}))
	assert.False(t, RecapInvalidatedBy(cands[1], []string{"ch2"}), "unrelated recaps survive")
	assert.False(t, RecapInvalidatedBy(cands[0], []string{"ch9"}))
}

func TestSIRecapOnlyCoveredContent(t *testing.T) {
	got := BuildRecapCandidates([]RecapSource{
		{ID: "ch1", Title: "Cause della crisi", SentenceIDs: []int{1, 2}, Score: 0.9},
		{ID: "", Title: "Senza provenienza", SentenceIDs: []int{3}, Score: 1.0},
		{ID: "ch2", Title: "Vuoto", SentenceIDs: nil, Score: 1.0},
		{ID: "ch3", Title: "Effetti sulle imprese", SentenceIDs: []int{9}, Score: 0.8},
	}, 5)
	require.Len(t, got, 2, "sources without provenance are dropped, never invented")
	assert.Equal(t, "Cause della crisi", got[0].Title, "score order")
	assert.Equal(t, []int{1, 2}, got[0].SentenceIDs)
	assert.NotContains(t, got[0].Title+got[1].Title, "Senza provenienza")

	capped := BuildRecapCandidates([]RecapSource{
		{ID: "a", Title: "A", SentenceIDs: []int{1}, Score: 0.1},
		{ID: "b", Title: "B", SentenceIDs: []int{2}, Score: 0.9},
	}, 1)
	require.Len(t, capped, 1)
	assert.Equal(t, "B", capped[0].Title)
}
