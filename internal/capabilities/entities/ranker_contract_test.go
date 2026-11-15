package entities

import (
	"context"
	"errors"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/stretchr/testify/require"
)

type fixedEntityOverlayRanker struct {
	indices [][]int
	err     error
	scenes  []EntityOverlayRankingScene
}

func (r *fixedEntityOverlayRanker) RankEntityOverlays(_ context.Context, scenes []EntityOverlayRankingScene) ([][]int, error) {
	r.scenes = scenes
	return r.indices, r.err
}

func rankedTimelineFixture(t *testing.T) EntityTimeline {
	t.Helper()
	occ := func(name, typ string, confidence float64, sceneID string, sceneIndex int, startUS int64) EntityOccurrence {
		return EntityOccurrence{
			EntityID: StableEntityID(typ, name), Name: name, Type: typ,
			SceneID: sceneID, SceneIndex: sceneIndex, Confidence: confidence,
			TextStart: 0, TextEnd: 1, WordStart: 0, WordEnd: 1,
			LocalStartUS: startUS, LocalEndUS: startUS + 200_000,
			TimelineStartUS: int64(sceneIndex) * 1_000_000,
			AudioStartUS:    int64(sceneIndex)*1_000_000 + startUS,
			AudioEndUS:      int64(sceneIndex)*1_000_000 + startUS + 200_000,
		}
	}
	timeline := EntityTimeline{
		Version: EntityTimelineVersion, DurationUS: 2_400_000,
		Scenes: []SceneEntityTimeline{
			{SceneID: "scene-0", SceneIndex: 0, Entities: []EntityOccurrence{
				occ("Tim Cook", "PERSON", .98, "scene-0", 0, 0),
				occ("Apple", "ORGANIZATION", .9, "scene-0", 0, 300_000),
				occ("artificial intelligence", "CONCEPT", .85, "scene-0", 0, 600_000),
			}},
			{SceneID: "scene-1", SceneIndex: 1, TimelineStartUS: 1_000_000, Entities: []EntityOccurrence{
				occ("Los Angeles", "GPE", .9, "scene-1", 1, 0),
			}},
		},
	}
	require.NoError(t, timeline.Validate())
	return timeline
}

func resolveEntityPlanForTest(t *testing.T, timeline EntityTimeline, ranker EntityOverlayRanker) capmodel.OverlayPlan {
	t.Helper()
	plan, err := ResolveEntityOverlayPlanWithRanker(context.Background(), timeline, "plan-ranked", "video-ranked", "", 1280, 720, 30, 1, ranker)
	require.NoError(t, err)
	require.NoError(t, plan.Validate())
	return plan
}

func TestResolveEntityOverlayPlanUsesBrainRustOrderAndCanonicalInputs(t *testing.T) {
	timeline := rankedTimelineFixture(t)
	ranker := &fixedEntityOverlayRanker{indices: [][]int{{2, 0, 1}, {0}}}
	plan := resolveEntityPlanForTest(t, timeline, ranker)

	require.Equal(t, []string{"artificial intelligence", "Tim Cook", "Apple", "Los Angeles"}, []string{
		plan.Items[0].Text, plan.Items[1].Text, plan.Items[2].Text, plan.Items[3].Text,
	})
	require.Len(t, ranker.scenes, 2)
	require.Equal(t, "ORG", ranker.scenes[0].Entities[1].EntityType)
	require.Equal(t, "PLACE", ranker.scenes[1].Entities[0].EntityType)
	require.Equal(t, "overlay-scene-0-artificial-intelligence", ranker.scenes[0].Entities[2].OverlayID)
	for _, item := range plan.Items {
		entry, err := capmodel.DefaultChrononOverlayRegistry.Resolve(item.Kind)
		require.NoError(t, err)
		require.Equal(t, entry.Template, item.TemplateID)
	}
}

func TestResolveEntityOverlayPlanFailsClosedWithoutBrainRustRanker(t *testing.T) {
	_, err := ResolveEntityOverlayPlanWithRanker(context.Background(), rankedTimelineFixture(t), "p", "v", "", 1280, 720, 30, 1, nil)
	require.ErrorContains(t, err, "BrainRust ranker is required")
}

func TestResolveEntityOverlayPlanRejectsInvalidBrainRustIndices(t *testing.T) {
	tests := []struct {
		name    string
		indices [][]int
		want    string
	}{
		{name: "out of range", indices: [][]int{{3}, {0}}, want: "outside scene"},
		{name: "duplicate index", indices: [][]int{{0, 0, 1, 2}, {0}}, want: "repeated occurrence index"},
		{name: "omitted identity", indices: [][]int{{0}, {0}}, want: "selected 1 overlay identities for 3 unique identities"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveEntityOverlayPlanWithRanker(context.Background(), rankedTimelineFixture(t), "p", "v", "", 1280, 720, 30, 1, &fixedEntityOverlayRanker{indices: tt.indices})
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestResolveEntityOverlayPlanPropagatesBrainRustFailure(t *testing.T) {
	_, err := ResolveEntityOverlayPlanWithRanker(context.Background(), rankedTimelineFixture(t), "p", "v", "", 1280, 720, 30, 1, &fixedEntityOverlayRanker{err: errors.New("worker unavailable")})
	require.ErrorContains(t, err, "worker unavailable")
}

func TestResolveEntityOverlayPlanClampsMentionToSceneEnd(t *testing.T) {
	occurrence := func(name, typ, sceneID string, sceneIndex int, sceneStartUS, startUS, endUS int64) EntityOccurrence {
		return EntityOccurrence{
			EntityID: StableEntityID(typ, name), Name: name, Type: typ,
			SceneID: sceneID, SceneIndex: sceneIndex, Confidence: .9,
			TextStart: 0, TextEnd: 1, WordStart: 0, WordEnd: 1,
			LocalStartUS: startUS - sceneStartUS, LocalEndUS: endUS - sceneStartUS,
			TimelineStartUS: sceneStartUS, AudioStartUS: startUS, AudioEndUS: endUS,
		}
	}
	timeline := EntityTimeline{
		Version: EntityTimelineVersion, DurationUS: 2_000_000,
		Scenes: []SceneEntityTimeline{
			{SceneID: "scene-0", SceneIndex: 0, Entities: []EntityOccurrence{occurrence("Tim Cook", "PERSON", "scene-0", 0, 0, 900_000, 950_000)}},
			{SceneID: "scene-1", SceneIndex: 1, TimelineStartUS: 1_000_000, Entities: []EntityOccurrence{occurrence("Pomezia", "GPE", "scene-1", 1, 1_000_000, 1_000_000, 1_100_000)}},
		},
	}
	require.NoError(t, timeline.Validate())
	plan := resolveEntityPlanForTest(t, timeline, &fixedEntityOverlayRanker{indices: [][]int{{0}, {0}}})
	require.Equal(t, int64(900_000), plan.Items[0].StartUS)
	require.Equal(t, int64(100_000), plan.Items[0].DurationUS)
	require.Equal(t, int64(900), plan.Items[0].StartMs)
	require.Equal(t, int64(1_000), plan.Items[0].EndMs)
}
