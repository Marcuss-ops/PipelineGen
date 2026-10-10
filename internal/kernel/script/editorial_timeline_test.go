package script

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func timelineWords(text []string, stepMS int64) []WordMark {
	out := make([]WordMark, len(text))
	for i, w := range text {
		out[i] = WordMark{Text: w, StartMS: int64(i) * stepMS, EndMS: int64(i+1) * stepMS}
	}
	return out
}

func TestTimelineAnchorsInsideSpokenWindow(t *testing.T) {
	words := timelineWords([]string{"Germany", "lost", "jobs", "in", "a", "year"}, 400)
	// Spoken 15.2s..18.6s scenario scaled: "lost jobs" = 400..1200.
	ids := []CanonicalID{"scene-01:highlight-00"}
	texts := map[CanonicalID]string{"scene-01:highlight-00": "lost jobs"}
	anchored, unanchored := AnchorElements(ids, texts, "en", words, 300, 0, 2400)
	require.Empty(t, unanchored)
	require.Len(t, anchored, 1)
	assert.Equal(t, int64(400), anchored[0].StartMS)
	assert.Equal(t, int64(1200), anchored[0].EndMS)
}

func TestTimelineNothingBeforeCertifiedAnchor(t *testing.T) {
	words := timelineWords([]string{"alpha", "beta", "gamma"}, 500)
	ids := []CanonicalID{"scene-01:highlight-00"}
	texts := map[CanonicalID]string{"scene-01:highlight-00": "never spoken phrase"}
	anchored, unanchored := AnchorElements(ids, texts, "en", words, 300, 0, 1500)
	assert.Empty(t, anchored, "no invented synchronization without an anchor")
	require.Equal(t, ids, unanchored)
}

func TestTimelineMinDurationExtendsWithinScene(t *testing.T) {
	words := timelineWords([]string{"Jobs", "fell"}, 200)
	ids := []CanonicalID{"scene-01:highlight-00"}
	texts := map[CanonicalID]string{"scene-01:highlight-00": "Jobs"}
	anchored, unanchored := AnchorElements(ids, texts, "en", words, 800, 0, 2000)
	require.Empty(t, unanchored)
	require.Len(t, anchored, 1)
	assert.True(t, anchored[0].MinDurationEnforced)
	assert.GreaterOrEqual(t, anchored[0].EndMS-anchored[0].StartMS, int64(800))
	assert.GreaterOrEqual(t, anchored[0].StartMS, int64(0))
	assert.LessOrEqual(t, anchored[0].EndMS, int64(2000))
}

func TestTimelineTooSmallSceneStaysUnanchored(t *testing.T) {
	words := timelineWords([]string{"Hi"}, 100)
	ids := []CanonicalID{"scene-01:highlight-00"}
	texts := map[CanonicalID]string{"scene-01:highlight-00": "Hi"}
	anchored, unanchored := AnchorElements(ids, texts, "en", words, 5000, 0, 100)
	assert.Empty(t, anchored, "a scene that cannot host the legibility floor stays unsynchronized")
	require.Len(t, unanchored, 1)
}
