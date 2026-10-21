package rustexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolveVisualNERBinary locates the real visualner binary for e2e tests.
// It returns "" when no binary is available so the test can skip cleanly.
func resolveVisualNERBinary() string {
	if fromEnv := os.Getenv("VELOX_RUST_VISUALNER_PATH"); fromEnv != "" {
		if info, err := os.Stat(fromEnv); err == nil && info.Mode().IsRegular() {
			return fromEnv
		}
	}
	candidate := filepath.Join("..", "..", "..", "..", "rust", "target", "release", "visualner")
	if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
		return candidate
	}
	return ""
}

// TestVisualNERAnalyzeRealBinaryProducesBulletsAndChartPoints runs the real
// visualner binary through the Go adapter. entity_count is high so the
// numeric chart value is not truncated by the entity limit.
func TestVisualNERAnalyzeRealBinaryProducesBulletsAndChartPoints(t *testing.T) {
	binary := resolveVisualNERBinary()
	if binary == "" {
		t.Skip("visualner release binary not built; run cargo build --release -p visualner --bin visualner")
	}
	text := "Le vendite sono salite del 25% nel primo trimestre. Il budget è di 1.200 euro."
	executor := newRealExecutor(t, binary, resolveFFmpegBinary())
	adapter, err := NewVisualNERAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()

	analysis, err := adapter.Analyze(context.Background(), "it", text, 20)
	if err != nil {
		t.Fatalf("real visualner analyze failed: %v", err)
	}
	if len(analysis.Bullets) == 0 {
		t.Fatal("expected at least one bullet from real binary")
	}
	foundBulletText := false
	for _, bullet := range analysis.Bullets {
		if bullet.Start < 0 || bullet.End > len(text) || bullet.Start >= bullet.End {
			t.Fatalf("bullet span out of range: %+v", bullet)
		}
		if text[bullet.Start:bullet.End] == bullet.Text {
			foundBulletText = true
		}
		for _, idx := range bullet.Entities {
			if idx < 0 || idx >= len(analysis.Entities) {
				t.Fatalf("bullet entity index out of range: %d", idx)
			}
		}
	}
	if !foundBulletText {
		t.Fatalf("no bullet text matches its source span: %+v", analysis.Bullets)
	}

	found25 := false
	for _, cp := range analysis.ChartPoints {
		if cp.Entity != nil && !validEntityIndex(*cp.Entity, len(analysis.Entities)) {
			t.Fatalf("chart point entity out of range: %+v", cp)
		}
		if cp.Start < 0 || cp.End > len(text) || cp.Start >= cp.End || text[cp.Start:cp.End] != cp.Text {
			t.Fatalf("chart point is not grounded: %+v", cp)
		}
		if cp.Value == 25 {
			found25 = true
		}
	}
	if !found25 {
		t.Fatalf("expected chart point 25 from real binary, got %+v", analysis.ChartPoints)
	}
}

// TestVisualNERSemanticBundleRealBinaryGroundsCharts checks that the unified
// single-analyze bundle returns exact source labels for the worker's chart
// values.
func TestVisualNERSemanticBundleRealBinaryGroundsCharts(t *testing.T) {
	binary := resolveVisualNERBinary()
	if binary == "" {
		t.Skip("visualner release binary not built; run cargo build --release -p visualner --bin visualner")
	}
	text := "Le vendite sono salite del 25% nel primo trimestre."
	executor := newRealExecutor(t, binary, resolveFFmpegBinary())
	adapter, err := NewVisualNERAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()

	bundle, err := adapter.SemanticCandidates(context.Background(), "it", text, 12)
	if err != nil {
		t.Fatalf("semantic candidates failed: %v", err)
	}
	found := false
	for _, c := range bundle.Charts {
		if c.Value == 25 && c.Text == "25%" && strings.Contains(text, c.Text) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected grounded 25%% candidate, got %+v", bundle.Charts)
	}
}

func validEntityIndex(index, count int) bool {
	return index >= 0 && index < count
}
