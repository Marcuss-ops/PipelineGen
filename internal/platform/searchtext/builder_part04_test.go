package searchtext

import (
	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/searchtext"
	"strings"
	"testing"
)

// TestYoutubeStrategy_Idempotent mirrors the existing
// TestAllStrategies_Idempotent for the YouTube strategy.
func TestYoutubeStrategy_Idempotent(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:     "yt-idem-1",
		Source:      "youtube",
		Title:       "Title",
		Description: "Desc",
		Tags:        []string{"a", "b"},
		Additional: map[string]string{
			"hook": "Hook",
		},
	}
	first := youtubeStrategy(input)
	second := youtubeStrategy(input)
	if first != second {
		t.Errorf("strategy must be idempotent; first=%q second=%q", first, second)
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────

func mustContainAll(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output must contain %q; got %q", w, got)
		}
	}
}
