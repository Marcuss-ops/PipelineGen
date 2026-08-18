package entities

import (
	"testing"

	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// TestImportanceScore_PinsCanonicalFormula pins the plan's ranking formula:
//
//	importance =
//	    0.30 * entity_type_weight
//	  + 0.25 * frequency
//	  + 0.20 * scene_relevance
//	  + 0.15 * novelty
//	  + 0.10 * asset_quality
func TestImportanceScore_PinsCanonicalFormula(t *testing.T) {
	// All-maximum input → 1.0.
	require.InDelta(t, 1.0, ImportanceScore(EntityRankInput{
		EntityTypeWeight: 1, Frequency: 1, SceneRelevance: 1, Novelty: 1, AssetQuality: 1,
	}), 1e-9)

	// All-zero input → 0.
	require.InDelta(t, 0.0, ImportanceScore(EntityRankInput{}), 1e-9)

	// A person (type 1.0) with high frequency but no asset:
	// 0.30*1.0 + 0.25*1.0 + 0.20*0.9 + 0.15*1.0 + 0.10*0 = 0.88.
	require.InDelta(t, 0.88, ImportanceScore(EntityRankInput{
		EntityTypeWeight: 1.0, Frequency: 1.0, SceneRelevance: 0.9, Novelty: 1.0, AssetQuality: 0,
	}), 1e-9)

	// Out-of-range components are clamped, never amplified.
	require.InDelta(t, 1.0, ImportanceScore(EntityRankInput{
		EntityTypeWeight: 5, Frequency: 2, SceneRelevance: 3, Novelty: 4, AssetQuality: 1,
	}), 1e-9)
}

// TestEntityTypeWeight pins the editorial type weights used by the ranker.
func TestEntityTypeWeight(t *testing.T) {
	require.Equal(t, 1.0, EntityTypeWeight("PERSON"))
	require.Equal(t, 1.0, EntityTypeWeight("person"))
	require.Equal(t, 0.9, EntityTypeWeight("ORGANIZATION"))
	require.Equal(t, 0.8, EntityTypeWeight("GPE"))
	require.Equal(t, 0.65, EntityTypeWeight("CONCEPT"))
	require.Equal(t, 0.55, EntityTypeWeight("NUMBER"))
	require.Equal(t, 0.5, EntityTypeWeight("SOMETHING_UNKNOWN"))
}

// TestRankScene_TopNByImportance pins the plan's editorial rule: a scene
// never renders every extracted entity — with MaxEntityOverlaysPerScene=2
// only the top-2 by importance survive, and they keep their timeline order.
func TestRankScene_TopNByImportance(t *testing.T) {
	// Three occurrences in one scene: Tim Cook (PERSON, freq 2, conf 0.98),
	// Apple (ORG, freq 1, conf 0.9), artificial intelligence (CONCEPT,
	// freq 1, conf 0.85). Tim Cook must rank first (person + highest
	// frequency + highest confidence); between Apple (0.9) and AI (0.85)
	// Apple wins on type weight (0.9 vs 0.65).
	occ := func(entityID, name, typ string, conf float64) EntityOccurrence {
		return EntityOccurrence{
			EntityID: StableEntityID(typ, name), Name: name, Type: typ,
			SceneID: "scene-0", SceneIndex: 0, Confidence: conf,
			TextStart: 0, TextEnd: 1, WordStart: 0, WordEnd: 1,
			LocalStartUS: 0, LocalEndUS: 100_000, TimelineStartUS: 0,
			AudioStartUS: 0, AudioEndUS: 100_000,
		}
	}
	timCook := occ(StableEntityID("PERSON", "Tim Cook"), "Tim Cook", "PERSON", 0.98)
	apple := occ(StableEntityID("ORGANIZATION", "Apple"), "Apple", "ORGANIZATION", 0.9)
	ai := occ(StableEntityID("CONCEPT", "artificial intelligence"), "artificial intelligence", "CONCEPT", 0.85)
	// Tim Cook appears twice in the run (frequency 2 vs 1 for the others) —
	// the run feeds the context; the SCENE carries one occurrence per entity.
	all := []EntityOccurrence{timCook, apple, timCook, ai}
	ctx := NewRankContext(all)
	scene := []EntityOccurrence{timCook, apple, ai}

	ranked := RankScene(scene, ctx, RankConfig{MaxEntityOverlaysPerScene: 2})
	require.Len(t, ranked, 2)
	require.Equal(t, "Tim Cook", ranked[0].Occurrence.Name, "person + highest frequency must rank first")
	require.Equal(t, "Apple", ranked[1].Occurrence.Name, "org beats concept on type weight")
	require.Greater(t, ranked[0].Importance, ranked[1].Importance)
}

// TestRankScene_NoCapKeepsAll pins that a zero cap keeps every occurrence
// (the unlimited resolver contract).
func TestRankScene_NoCapKeepsAll(t *testing.T) {
	occ := func(name, typ string) EntityOccurrence {
		return EntityOccurrence{
			EntityID: StableEntityID(typ, name), Name: name, Type: typ,
			SceneID: "scene-0", SceneIndex: 0, Confidence: 0.9,
			TextStart: 0, TextEnd: 1, WordStart: 0, WordEnd: 1,
			LocalStartUS: 0, LocalEndUS: 100_000, TimelineStartUS: 0,
			AudioStartUS: 0, AudioEndUS: 100_000,
		}
	}
	all := []EntityOccurrence{occ("Tim Cook", "PERSON"), occ("Apple", "ORGANIZATION"), occ("Los Angeles", "GPE")}
	ctx := NewRankContext(all)
	ranked := RankScene(all, ctx, RankConfig{})
	require.Len(t, ranked, 3)
}

// TestRankScene_NoveltyAndAssetQualityInfluenceScore pins that the run-level
// context really moves the score: a first-mention entity with a bound asset
// outranks a repeated mention without one.
func TestRankScene_NoveltyAndAssetQualityInfluenceScore(t *testing.T) {
	occ := func(entityID, name, typ string) EntityOccurrence {
		return EntityOccurrence{
			EntityID: entityID, Name: name, Type: typ,
			SceneID: "scene-0", SceneIndex: 0, Confidence: 0.9,
			TextStart: 0, TextEnd: 1, WordStart: 0, WordEnd: 1,
			LocalStartUS: 0, LocalEndUS: 100_000, TimelineStartUS: 0,
			AudioStartUS: 0, AudioEndUS: 100_000,
		}
	}
	timCookID := StableEntityID("PERSON", "Tim Cook")
	appleID := StableEntityID("ORGANIZATION", "Apple")
	all := []EntityOccurrence{
		occ(timCookID, "Tim Cook", "PERSON"),
		occ(appleID, "Apple", "ORGANIZATION"),
		occ(timCookID, "Tim Cook", "PERSON"), // Tim Cook repeats → novelty 0.5
	}
	ctx := NewRankContext(all)
	// Apple has a high-quality asset; Tim Cook has none.
	ctx.AssetQuality = func(entityID string) float64 {
		if entityID == appleID {
			return 1.0
		}
		return 0
	}

	ranked := RankScene(all, ctx, RankConfig{})
	byName := map[string]RankedEntity{}
	for _, r := range ranked {
		byName[r.Occurrence.Name] = r
	}
	// Apple (freq 1/2=0.5, novelty 1.0, asset 1.0):
	//   0.30*0.9 + 0.25*0.5 + 0.20*0.9 + 0.15*1.0 + 0.10*1.0 = 0.825
	// Tim Cook (freq 2/2=1.0, novelty 0.5, no asset):
	//   0.30*1.0 + 0.25*1.0 + 0.20*0.9 + 0.15*0.5 + 0.10*0 = 0.805
	require.InDelta(t, 0.825, byName["Apple"].Importance, 1e-9)
	require.InDelta(t, 0.805, byName["Tim Cook"].Importance, 1e-9)
	require.Greater(t, byName["Apple"].Importance, byName["Tim Cook"].Importance,
		"asset quality + novelty must lift Apple above the repeated Tim Cook")
}

// TestResolveRankedEntityOverlayPlan_CapsPerScene pins the planner path: with
// MaxEntityOverlaysPerScene=2, a scene with 3 entities resolves only the
// top-2 by importance; every survivor keeps its certified timing.
func TestResolveRankedEntityOverlayPlan_CapsPerScene(t *testing.T) {
	timeline := rankedTimelineFixture(t)

	plan, err := ResolveRankedEntityOverlayPlan(timeline, "plan-ranked-001", "video-001", "", 1280, 720, 30, RankConfig{MaxEntityOverlaysPerScene: 2})
	require.NoError(t, err)
	require.NoError(t, plan.Validate())

	// scene-0 has 3 entities → only the top-2 survive; scene-1 has 1.
	require.Len(t, plan.Items, 3)
	names := map[string]bool{}
	for _, item := range plan.Items {
		names[item.Text] = true
		require.NotEmpty(t, item.RenderKey)
	}
	require.True(t, names["Tim Cook"], "person must survive the cap")
	require.True(t, names["Apple"], "org must survive the cap")
	require.False(t, names["artificial intelligence"], "lowest-importance entity must be cut by the cap")
}

// TestResolveRankedEntityOverlayPlan_ZeroCapsEqualUnlimited pins the
// compatibility contract: a zero RankConfig resolves byte-identically to the
// unlimited resolver.
func TestResolveRankedEntityOverlayPlan_ZeroCapsEqualUnlimited(t *testing.T) {
	timeline := rankedTimelineFixture(t)

	unlimited, err := ResolveEntityOverlayPlan(timeline, "plan-ranked-002", "video-002", "", 1280, 720, 30)
	require.NoError(t, err)
	ranked, err := ResolveRankedEntityOverlayPlan(timeline, "plan-ranked-002", "video-002", "", 1280, 720, 30, RankConfig{})
	require.NoError(t, err)

	require.Len(t, ranked.Items, len(unlimited.Items))
	for i := range unlimited.Items {
		require.Equal(t, unlimited.Items[i], ranked.Items[i], "zero-cap ranked plan must equal the unlimited plan item %d", i)
	}
	require.Equal(t, unlimited.Fingerprint, ranked.Fingerprint)
}

// rankedTimelineFixture returns a two-scene EntityTimeline:
//
//	scene-0 (offset 0): Tim Cook (PERSON, 0.98), Apple (ORG, 0.9),
//	                    artificial intelligence (CONCEPT, 0.85)
//	scene-1 (offset 1s): Los Angeles (GPE, 0.9)
func rankedTimelineFixture(t *testing.T) EntityTimeline {
	t.Helper()
	occ := func(name, typ string, conf float64, sceneID string, sceneIndex int, startUS int64) EntityOccurrence {
		return EntityOccurrence{
			EntityID: StableEntityID(typ, name), Name: name, Type: typ,
			SceneID: sceneID, SceneIndex: sceneIndex, Confidence: conf,
			TextStart: 0, TextEnd: 1, WordStart: 0, WordEnd: 1,
			LocalStartUS: startUS, LocalEndUS: startUS + 200_000,
			TimelineStartUS: int64(sceneIndex) * 1_000_000,
			AudioStartUS:    int64(sceneIndex)*1_000_000 + startUS,
			AudioEndUS:      int64(sceneIndex)*1_000_000 + startUS + 200_000,
		}
	}
	timeline := EntityTimeline{
		Version:    EntityTimelineVersion,
		DurationUS: 2_400_000,
		Scenes: []SceneEntityTimeline{
			{SceneID: "scene-0", SceneIndex: 0, TimelineStartUS: 0, Entities: []EntityOccurrence{
				occ("Tim Cook", "PERSON", 0.98, "scene-0", 0, 0),
				occ("Apple", "ORGANIZATION", 0.9, "scene-0", 0, 300_000),
				occ("artificial intelligence", "CONCEPT", 0.85, "scene-0", 0, 600_000),
			}},
			{SceneID: "scene-1", SceneIndex: 1, TimelineStartUS: 1_000_000, Entities: []EntityOccurrence{
				occ("Los Angeles", "GPE", 0.9, "scene-1", 1, 0),
			}},
		},
	}
	require.NoError(t, timeline.Validate())
	return timeline
}

// TestResolveRankedEntityOverlayPlan_TemplateResolution pins that the ranked
// path resolves templates through the single registry, exactly like the
// unlimited path.
func TestResolveRankedEntityOverlayPlan_TemplateResolution(t *testing.T) {
	timeline := rankedTimelineFixture(t)
	plan, err := ResolveRankedEntityOverlayPlan(timeline, "plan-ranked-003", "video-003", "", 1280, 720, 30, RankConfig{MaxEntityOverlaysPerScene: 2})
	require.NoError(t, err)

	for _, item := range plan.Items {
		// The resolver stamps the kind (EntityTypeToKind result) on the item;
		// the template must resolve through the single registry from that
		// kind, exactly like the unlimited path.
		entry, err := capabilityoverlay.DefaultChrononOverlayRegistry.Resolve(item.Kind)
		require.NoError(t, err)
		require.Equal(t, entry.Template, item.TemplateID, "item %q must resolve through the registry", item.ID)
	}
}
