package script

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Manifest contract ───────────────────────────────────────────

func TestSIRegistryAdmitsOnlyKnownRelations(t *testing.T) {
	for _, k := range []string{RelationCauses, RelationContributesTo, RelationPrecedes, RelationContrastsWith, RelationOwns, RelationFunds, RelationPartnersWith, RelationCompetesWith} {
		assert.True(t, IsKnownRelation(k), k)
	}
	assert.False(t, IsKnownRelation("collaborates_maybe"))
}

func TestSIValidateRejectsBadManifests(t *testing.T) {
	bad := &SceneIntelligenceManifest{SchemaVersion: "scene_intelligence.v99"}
	assert.Error(t, bad.Validate())

	dup := &SceneIntelligenceManifest{
		SchemaVersion: SceneIntelligenceManifestVersion, AnalyzerVersion: SceneIntelligenceAnalyzerVersion,
		RelationRegistryVersion: RelationRegistryVersion,
		Scenes: []SISceneSignals{
			{SceneID: "s1", Density: SIDensity{SceneID: "s1", Score: 0.2}},
			{SceneID: "s1", Density: SIDensity{SceneID: "s1", Score: 0.2}},
		},
	}
	assert.Error(t, dup.Validate(), "duplicate scene identity")

	qual := &SceneIntelligenceManifest{
		SchemaVersion: SceneIntelligenceManifestVersion, AnalyzerVersion: SceneIntelligenceAnalyzerVersion,
		RelationRegistryVersion: RelationRegistryVersion,
		Scenes: []SISceneSignals{{
			SceneID: "s1", Density: SIDensity{SceneID: "s1", Score: 0.2},
			Comparisons: []SIComparison{{
				SceneID: "s1", Kind: "comparison", Subjects: []string{"ford", "toyota"},
				Relation: RelationContrastsWith, CandidateVisual: "split_comparison", Values: []float64{42},
			}},
		}},
	}
	assert.Error(t, qual.Validate(), "qualitative comparison must not carry values")
}

func TestSIFingerprintDiffersOnContent(t *testing.T) {
	base := SIFingerprintInput{
		SceneIDs: []string{"s1"}, SceneTexts: []string{"Germany lost jobs."},
		EntitySurfaces: [][]string{{"germany"}}, Language: "en",
		Analyzer: SceneIntelligenceAnalyzerVersion, Registry: RelationRegistryVersion,
	}
	a, err := ComputeSIFingerprint(base)
	require.NoError(t, err)
	base.SceneTexts = []string{"Germany gained jobs."}
	b, err := ComputeSIFingerprint(base)
	require.NoError(t, err)
	assert.NotEqual(t, a, b)
	_, err = ComputeSIFingerprint(SIFingerprintInput{})
	assert.Error(t, err, "empty scenes fail closed")
}

func TestSIManifestOrderingDeterministic(t *testing.T) {
	m := &SceneIntelligenceManifest{
		SchemaVersion: SceneIntelligenceManifestVersion, AnalyzerVersion: SceneIntelligenceAnalyzerVersion,
		RelationRegistryVersion: RelationRegistryVersion,
		Scenes: []SISceneSignals{
			{SceneID: "scene-02", Density: SIDensity{SceneID: "scene-02", Score: 0.1}},
			{SceneID: "scene-01", Density: SIDensity{SceneID: "scene-01", Score: 0.1}},
		},
	}
	require.NoError(t, m.Validate())
	assert.Equal(t, []string{"scene-01", "scene-02"}, m.SceneIDs())
	require.NotNil(t, m.SignalsFor("scene-01"))
	assert.Nil(t, m.SignalsFor("missing"))
}

// ── Optimization: constraints ───────────────────────────────────

func TestSelectLocalEnforcesAnchorAndMinDuration(t *testing.T) {
	cands := []OptCandidate{
		{ID: "early", SceneID: "s", R: 0.9, Q: 0.9, StartMS: 100, EndMS: 2000, AnchorMS: 500, MinDurationMS: 300},
		{ID: "short", SceneID: "s", R: 0.9, Q: 0.9, StartMS: 600, EndMS: 700, AnchorMS: 500, MinDurationMS: 300},
		{ID: "good", SceneID: "s", R: 0.5, Q: 0.5, StartMS: 600, EndMS: 2000, AnchorMS: 500, MinDurationMS: 300},
	}
	got, rejected := SelectLocal(cands, DefaultOptWeights(), 3)
	require.Len(t, got, 1)
	assert.Equal(t, "good", got[0].ID)
	assert.Equal(t, InvalidBeforeAnchor, rejected["early"], "nothing before the certified anchor")
	assert.Equal(t, InvalidTooShort, rejected["short"], "legibility floor enforced")
}

func TestSelectLocalOverlapBudgetAndDeterminism(t *testing.T) {
	mk := func(id string, score float64) OptCandidate {
		return OptCandidate{ID: id, SceneID: "s", R: score, StartMS: 0, EndMS: 1000, AnchorMS: 0}
	}
	base := []OptCandidate{mk("a", 0.9), mk("b", 0.8), mk("c", 0.7)}
	a, _ := SelectLocal(base, DefaultOptWeights(), 2)
	rev := []OptCandidate{base[2], base[1], base[0]}
	b, _ := SelectLocal(rev, DefaultOptWeights(), 2)
	require.Equal(t, a, b, "input order must not change the set")
	assert.Len(t, a, 2, "Overlap(t) <= K enforced")
}

// ── Optimization: global vs local ───────────────────────────────

func TestPlanSequenceVariesWhenCompatible(t *testing.T) {
	bars := SeqOption{VisualID: "bars", Recipe: "bars", Family: "chart", Motion: "rise", Quality: 0.9}
	broll := SeqOption{VisualID: "broll", Recipe: "broll", Family: "footage", Motion: "pan", Quality: 0.85}
	kpi := SeqOption{VisualID: "kpi", Recipe: "kpi", Family: "stat", Motion: "roll", Quality: 0.84}
	scenes := [][]SeqOption{{bars, broll, kpi}, {bars, broll, kpi}, {bars, broll, kpi}, {bars, broll, kpi}, {bars, broll, kpi}}
	got, err := PlanSequence(scenes, 0.6)
	require.NoError(t, err)
	used := map[string]int{}
	for _, o := range got {
		used[o.Recipe]++
	}
	assert.Greater(t, len(used), 1, "global planning must not repeat bars 5x when equivalents exist")

	// Determinism.
	again, err := PlanSequence(scenes, 0.6)
	require.NoError(t, err)
	assert.Equal(t, got, again)
}

func TestPlanSequenceNeverOverridesEligibility(t *testing.T) {
	only := [][]SeqOption{{{VisualID: "bars", Recipe: "bars", Family: "chart", Quality: 0.9}}}
	got, err := PlanSequence(only, 0.6)
	require.NoError(t, err)
	assert.Equal(t, "bars", got[0].Recipe, "bar-only datum stays bars: variety never converts it")
	_, err = PlanSequence([][]SeqOption{{}}, 0.6)
	assert.Error(t, err, "no eligible visual means an error, not a fabricated plan")
	_, err = PlanSequence(nil, 0.6)
	assert.Error(t, err)
}

func TestProfilesChangeObjectivesNotFacts(t *testing.T) {
	o := SeqOption{VisualID: "n", Recipe: "numbers", Family: "number", Quality: 0.5}
	assert.Equal(t, 0.5+0.2, NewsProfile().ApplyBoosts(o), "news boosts numbers")
	assert.Equal(t, 0.5, DocumentaryProfile().ApplyBoosts(o), "documentary does not")
	assert.Equal(t, "documentary", DocumentaryProfile().Name)
	assert.Equal(t, "news", NewsProfile().Name)
	assert.Equal(t, "educational", EducationalProfile().Name)
}

func TestDensityPerSecondRisesWithSpeechRate(t *testing.T) {
	// D05: same content spoken faster → higher load per second.
	slow, err := DensityPerSecond(0.6, 20)
	require.NoError(t, err)
	fast, err := DensityPerSecond(0.6, 10)
	require.NoError(t, err)
	assert.Greater(t, fast, slow)
	assert.InDelta(t, 0.06, fast, 1e-9)
	_, err = DensityPerSecond(0.6, 0)
	assert.Error(t, err, "no certified duration means no per-second load")
	_, err = DensityPerSecond(1.5, 10)
	assert.Error(t, err)
}

func TestSINewFieldsRoundTrip(t *testing.T) {
	neg := true
	attr := "Secondo gli analisti"
	y, ye := 2020, 2023
	m := &SceneIntelligenceManifest{
		SchemaVersion: SceneIntelligenceManifestVersion, AnalyzerVersion: SceneIntelligenceAnalyzerVersion,
		RelationRegistryVersion: RelationRegistryVersion,
		Scenes: []SISceneSignals{{
			SceneID: "s", Density: SIDensity{SceneID: "s", Score: 0.3},
			Comparisons: []SIComparison{{
				SceneID: "s", Kind: "comparison", Subjects: []string{"ford", "toyota"},
				Relation: RelationContrastsWith, CandidateVisual: "split_comparison", Negated: neg,
			}},
			Causal: []SICausalRelation{{
				Kind: "causal_relation", Cause: "c", Effect: "e", Relation: RelationCauses,
				ExtractionConfidence: 0.6, Attribution: &attr, Hypothetical: true,
			}},
			TemporalEvents: []SITemporalEvent{{
				EventID: "event-0000", TextSpan: "2020–2023", TemporalExpression: "2020–2023",
				NormalizedYear: &y, NormalizedEndYear: &ye, Certainty: 0.9,
			}},
		}},
	}
	require.NoError(t, m.Validate())
	assert.True(t, m.Scenes[0].Comparisons[0].Negated)
	assert.True(t, m.Scenes[0].Causal[0].Hypothetical)
	require.NotNil(t, m.Scenes[0].TemporalEvents[0].NormalizedEndYear)
}

func TestSIDenseSceneReducesSimultaneity(t *testing.T) {
	// D06: a very dense scene with 8 overlay candidates → admission cuts
	// simultaneity while respecting anchor/duration/overlap constraints.
	// Budget 2 is the SI "low" hint contract (overlays.DensityOverlapBudget
	// maps dense scenes to 2); kernel/script must not import overlays, so
	// the integration point is exercised literally here and against the
	// real helper in the scripts-package X02/D06 test.
	budget := 2
	assert.Equal(t, 2, budget)
	cands := make([]OptCandidate, 0, 8)
	for i := 0; i < 8; i++ {
		cands = append(cands, OptCandidate{
			ID: string(rune('a' + i)), SceneID: "dense", R: 0.9 - float64(i)*0.01, Q: 0.8,
			StartMS: 0, EndMS: 2000, AnchorMS: 0, MinDurationMS: 300,
		})
	}
	got, rejected := SelectLocal(cands, DefaultOptWeights(), budget)
	assert.LessOrEqual(t, len(got), 2, "dense scene admits few simultaneous items")
	for _, a := range got {
		assert.Equal(t, ValidOK, CheckValidity(a))
	}
	assert.NotEmpty(t, rejected, "losers reported with reasons, not silently dropped")
}

func TestSISequenceTwentyScenesVariedAndPertinent(t *testing.T) {
	// X03: 20 scenes → global variety without sacrificing pertinence:
	// every choice stays eligible (high quality) yet recipes rotate.
	bars := SeqOption{VisualID: "bars", Recipe: "bars", Family: "chart", Motion: "rise", Quality: 0.9}
	broll := SeqOption{VisualID: "broll", Recipe: "broll", Family: "footage", Motion: "pan", Quality: 0.88}
	kpi := SeqOption{VisualID: "kpi", Recipe: "kpi", Family: "stat", Motion: "roll", Quality: 0.87}
	timeline := SeqOption{VisualID: "timeline", Recipe: "timeline", Family: "diagram", Motion: "wipe", Quality: 0.86}
	scenes := make([][]SeqOption, 20)
	for i := range scenes {
		scenes[i] = []SeqOption{bars, broll, kpi, timeline}
	}
	got, err := PlanSequence(scenes, DocumentaryProfile().Lambda)
	require.NoError(t, err)
	used := map[string]int{}
	for _, o := range got {
		used[o.Recipe]++
		assert.GreaterOrEqual(t, o.Quality, 0.86, "pertinence never sacrificed")
	}
	assert.Greater(t, len(used), 1, "global planning varies across 20 scenes")
	assert.Less(t, used["bars"], 20, "no single recipe dominates the run")
}

func TestSICrossLanguageIdentityWithLocalTiming(t *testing.T) {
	// X04: EN → IT → DE as projections from EN: same canonical ids,
	// per-language certified timing.
	r := NewIdentityRegistry("en")
	id, err := r.Register("scene-03", EditorialKindHighlight, "Industrial employment")
	require.NoError(t, err)
	require.NoError(t, r.Localize(id, "it", "Occupazione industriale"))
	require.NoError(t, r.Localize(id, "de", "Industriebeschäftigung"))
	enWords := []WordMark{{Text: "Industrial", StartMS: 15200, EndMS: 16000}, {Text: "employment", StartMS: 16000, EndMS: 18600}}
	itWords := []WordMark{{Text: "Occupazione", StartMS: 15200, EndMS: 16800}, {Text: "industriale", StartMS: 16800, EndMS: 19200}}
	texts := map[CanonicalID]string{id: "Industrial employment"}
	en, un := AnchorElements([]CanonicalID{id}, texts, "en", enWords, 300, 15000, 20000)
	require.Empty(t, un)
	require.Len(t, en, 1)
	itTexts := map[CanonicalID]string{id: "Occupazione industriale"}
	it, un := AnchorElements([]CanonicalID{id}, itTexts, "it", itWords, 300, 15000, 20000)
	require.Empty(t, un)
	require.Len(t, it, 1)
	assert.Equal(t, en[0].CanonicalID, it[0].CanonicalID, "identity shared across languages")
	assert.NotEqual(t, en[0].EndMS, it[0].EndMS, "timing local per language")
	require.NoError(t, r.AnchorTiming(id, "it", it[0].StartMS, it[0].EndMS))
	got, ok := r.GetLocalized(id, "it")
	require.True(t, ok)
	assert.True(t, got.Anchored)
}

func TestSIInvalidationScene8Change(t *testing.T) {
	// X05: changing only scene 8 invalidates dependents, reuses the rest.
	g := NewInvalidationGraph()
	s8v1, err := FingerprintBytes("scene-08 text v1")
	require.NoError(t, err)
	s7, err := FingerprintBytes("scene-07 text")
	require.NoError(t, err)
	profile, err := FingerprintBytes("scene_intelligence.v1")
	require.NoError(t, err)
	g.Upsert(DepNode{ID: "si:scene-07", Inputs: []string{s7, profile}, Product: "si7"})
	g.Upsert(DepNode{ID: "si:scene-08", Inputs: []string{s8v1, profile}, Product: "si8"})
	g.Upsert(DepNode{ID: "timeline:scene-08:it", Inputs: []string{s8v1, "tts:it"}, Product: "tl8"})
	g.Upsert(DepNode{ID: "recap:job", Inputs: []string{s7, s8v1, profile}, Product: "rc", Global: true})
	invalid := g.Invalidate([]string{s8v1})
	assert.Contains(t, invalid, "si:scene-08")
	assert.Contains(t, invalid, "timeline:scene-08:it")
	assert.Contains(t, invalid, "recap:job", "global recap invalidates on scene change")
	assert.NotContains(t, invalid, "si:scene-07", "independent scenes reused")
}

func TestSIDisabledKeepsBaselineAdmission(t *testing.T) {
	// X08 (kernel half): with SI off, the baseline admission is a pure
	// stable function — 50 identical runs give identical outputs, so
	// legacy script/overlay/job results cannot drift. (The other half —
	// disabled projection returns nil — lives in the scripts package.)
	cands := []OptCandidate{
		{ID: "a", SceneID: "s", R: 0.9, StartMS: 0, EndMS: 1000, AnchorMS: 0},
		{ID: "b", SceneID: "s", R: 0.8, StartMS: 0, EndMS: 1000, AnchorMS: 0},
		{ID: "c", SceneID: "s", R: 0.7, StartMS: 0, EndMS: 1000, AnchorMS: 0},
	}
	first, _ := SelectLocal(cands, DefaultOptWeights(), 3)
	for i := 0; i < 50; i++ {
		got, _ := SelectLocal(cands, DefaultOptWeights(), 3)
		assert.Equal(t, first, got)
	}
}

func TestSIUnknownRelationRejectedBeforeRender(t *testing.T) {
	// X09: a detector emitting outside the registry is rejected at the
	// manifest gate — never reaches admission or render.
	m := &SceneIntelligenceManifest{
		SchemaVersion: SceneIntelligenceManifestVersion, AnalyzerVersion: SceneIntelligenceAnalyzerVersion,
		RelationRegistryVersion: RelationRegistryVersion,
		Scenes: []SISceneSignals{{
			SceneID: "s", Density: SIDensity{SceneID: "s", Score: 0.2},
			Relations: []SIEntityRelation{{Subject: "a", Object: "b", Relation: "best_friends_forever"}},
		}},
	}
	assert.Error(t, m.Validate())
}

func TestTransitionCostRanksRepetition(t *testing.T) {
	a := SeqOption{VisualID: "x", Recipe: "bars", Family: "chart", Motion: "rise"}
	assert.Equal(t, 1.0, TransitionCost(a, a), "identical recipe capped at 1.0")
	b := SeqOption{VisualID: "y", Recipe: "line", Family: "chart", Motion: "trace"}
	assert.Greater(t, TransitionCost(a, a), TransitionCost(a, b))
	assert.Equal(t, 0.0, TransitionCost(SeqOption{}, b), "first scene has no transition cost")
}
