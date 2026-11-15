package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestProjectEditorialCandidatesKeepsEveryBullet(t *testing.T) {
	result, scenes, resolved, inputs := editorialOverlayFixture()
	result.Editorial.Scenes[0].Bullets = append(result.Editorial.Scenes[0].Bullets, scriptpkg.EditorialSceneBullet{
		SentenceStart: 0, SentenceEnd: 1, Text: "Steel output is collapsing.",
	})

	legacy := projectEditorialOverlays(result, "en", scenes, resolved, inputs, nil)
	require.Len(t, legacy[0].Phrases, 2, "the legacy selector keeps one title and one bullet")

	_, _, _, candidateInputs := editorialOverlayFixture()
	candidates := projectEditorialCandidates(result, "en", scenes, resolved, candidateInputs, nil)
	require.Len(t, candidates[0].Phrases, 3, "the candidate set must expose title and both grounded bullets")
	byCandidate := map[string][]capabilityoverlay.TimedAnnotation{}
	for _, phrase := range candidates[0].Phrases {
		byCandidate[phrase.EditorialCandidate] = append(byCandidate[phrase.EditorialCandidate], phrase)
	}
	require.Len(t, byCandidate["title"], 1)
	require.Len(t, byCandidate["bullet"], 2)
	for _, phrase := range candidates[0].Phrases {
		require.NotEmpty(t, phrase.EditorialCandidate)
		require.GreaterOrEqual(t, phrase.StartUS, int64(2_000_000))
		require.LessOrEqual(t, phrase.StartUS+phrase.DurationUS, int64(5_000_000))
	}
}
