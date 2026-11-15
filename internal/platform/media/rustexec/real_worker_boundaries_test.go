package rustexec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"go.uber.org/zap"
)

func TestRealMediasamplerScoresLocalSearchBatch(t *testing.T) {
	binary := filepath.Join("..", "..", "..", "..", "bin", "mediasampler")
	if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Skip("mediasampler release binary not installed")
	}
	executor := NewExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	defer executor.Close()
	adapter, err := NewLocalScorerAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	scores, err := adapter.ScoreLocal(context.Background(), search.LocalScoreQuery{
		Text: "red apple", Tags: []string{"fruit", "red"}, Source: "artlist", MinDurationMS: 1000,
	}, []search.LocalScoreCandidate{
		{ID: "apple", Title: "red apple", Tags: []string{"fruit", "red"}, Source: "artlist", DurationMS: 1000},
		{ID: "sky", Title: "blue sky", Tags: []string{"weather"}, Source: "other", DurationMS: 500},
	})
	if err != nil {
		t.Fatalf("real mediasampler score_local failed: %v", err)
	}
	if len(scores) != 2 || scores[0].ID != "apple" || scores[1].ID != "sky" {
		t.Fatalf("score_local changed candidate identity/order: %+v", scores)
	}
	if scores[0].Score != 0.85 || scores[1].Score != 0 {
		t.Fatalf("Rust scorer changed the retired Go weighted formula: %+v", scores)
	}
	if scores[0].Score <= scores[1].Score {
		t.Fatalf("matching local title should outrank unrelated title: %+v", scores)
	}
}

func TestRealSemanticEngineResolvesMapOverlapThroughAdapter(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not installed")
	}
	executor := NewExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	defer executor.Close()
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	items := []capmodel.OverlayItem{
		{ID: "map", SceneID: "scene-1", Kind: "map", StartMs: 1000, EndMs: 5000, StartUS: 1_000_000, DurationUS: 4_000_000, Map: &capmodel.MapOverlay{}},
		{ID: "phrase", SceneID: "scene-1", Kind: "text_phrase", StartMs: 1000, EndMs: 2000, StartUS: 1_000_000, DurationUS: 1_000_000},
	}
	resolved, err := adapter.ResolveMapOverlaps(context.Background(), items, map[string][2]int64{"scene-1": {0, 10_000_000}}, 24, 1)
	if err != nil {
		t.Fatalf("real semantic-engine map overlap failed: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("resolver returned %d items, want 2: %+v", len(resolved), resolved)
	}
	if resolved[1].StartUS < resolved[0].StartUS+resolved[0].DurationUS {
		t.Fatalf("phrase still overlaps the map after Rust resolution: map=%+v phrase=%+v", resolved[0], resolved[1])
	}
}

func TestRealSemanticEngineRanksEntityOverlaysThroughAdapter(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not installed")
	}
	executor := NewExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	defer executor.Close()
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	ranked, err := adapter.RankEntityOverlays(context.Background(), []capabilityentities.EntityOverlayRankingScene{
		{Entities: []capabilityentities.EntityOverlayRankingOccurrence{
			{EntityID: "person-tim", OverlayID: "overlay-s1-tim", EntityType: "PERSON", Confidence: .98},
			{EntityID: "org-apple", OverlayID: "overlay-s1-apple", EntityType: "ORG", Confidence: .9},
			{EntityID: "person-tim", OverlayID: "overlay-s1-tim", EntityType: "PERSON", Confidence: .98},
		}},
		{Entities: []capabilityentities.EntityOverlayRankingOccurrence{
			{EntityID: "place-rome", OverlayID: "overlay-s2-rome", EntityType: "PLACE", Confidence: .9},
			{EntityID: "person-tim", OverlayID: "overlay-s2-tim", EntityType: "PERSON", Confidence: .98},
		}},
	})
	if err != nil {
		t.Fatalf("real semantic-engine entity overlay ranking failed: %v", err)
	}
	if len(ranked) != 2 || len(ranked[0]) != 2 || len(ranked[1]) != 2 {
		t.Fatalf("unexpected selected overlay indices: %+v", ranked)
	}
	if ranked[0][0] != 0 || ranked[0][1] != 1 || ranked[1][0] != 1 || ranked[1][1] != 0 {
		t.Fatalf("Rust rank order or stable duplicate collapse changed: %+v", ranked)
	}
}

func TestRealSemanticEngineCascadesEntrancesThroughAdapter(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not installed")
	}
	executor := NewExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	defer executor.Close()
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	items := []capmodel.OverlayItem{
		{ID: "portrait", SceneID: "s1", Kind: "entity_image", StartMs: 100, EndMs: 3_000,
			MotionID: "image_snap", MotionParams: map[string]any{"enter_frames": 8, "opaque": "keep"}},
		{ID: "phrase", SceneID: "s1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "One sentence", StartMs: 0, EndMs: 3_000,
			MotionID: "phrase_snap", MotionParams: map[string]any{"enter_frames": 8}},
		{ID: "composite", SceneID: "s1", Kind: "entity_image", StartMs: 0, EndMs: 3_000,
			ImageLayers: []capmodel.OverlayImageLayer{{ID: "one"}, {ID: "two"}}, MotionParams: map[string]any{"enter_frames": 8}},
	}
	resolved, err := adapter.CascadeClusteredEntrances(context.Background(), items, 24, 1)
	if err != nil {
		t.Fatalf("real semantic-engine entrance cascade failed: %v", err)
	}
	if got := resolved[0].MotionParams["enter_frames"]; got != int64(20) && got != 20 {
		t.Fatalf("portrait entrance=%v, want 20 frames", got)
	}
	if resolved[0].MotionParams["opaque"] != "keep" || resolved[0].StartMs != items[0].StartMs || resolved[0].EndMs != items[0].EndMs {
		t.Fatalf("cascade changed unrelated renderer fields: before=%+v after=%+v", items[0], resolved[0])
	}
	if resolved[2].MotionParams["enter_frames"] != items[2].MotionParams["enter_frames"] {
		t.Fatalf("cascade changed a composite parent: %+v", resolved[2])
	}
}
