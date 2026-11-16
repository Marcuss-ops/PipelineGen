package rustexec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
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
