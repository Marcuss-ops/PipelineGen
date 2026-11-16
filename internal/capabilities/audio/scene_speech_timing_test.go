package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSceneSpeechTiming_Validate pins the scene-level invariants: word
// boundaries must be contiguous/monotonic/contained, and each phrase must
// satisfy the local→global mapping.
func TestSceneSpeechTiming_Validate(t *testing.T) {
	valid := SceneSpeechTiming{
		SceneID:          "scene-0",
		VoiceoverAssetID: "vo-0",
		LocalDurationUS:  200_000,
		Words: []SpeechWordTiming{
			{Index: 0, Text: "Jackie", StartUS: 0, EndUS: 100_000},
			{Index: 1, Text: "Chan", StartUS: 100_000, EndUS: 200_000},
		},
		Phrases: []PhraseTiming{{
			SceneIndex:      0,
			PhraseIndex:     0,
			Text:            "Jackie Chan",
			WordStart:       0,
			WordEnd:         1,
			LocalStartUS:    0,
			LocalEndUS:      200_000,
			TimelineStartUS: 5_000_000,
			GlobalStartUS:   5_000_000,
			GlobalEndUS:     5_200_000,
		}},
	}
	require.NoError(t, valid.Validate())

	emptyScene := valid
	emptyScene.SceneID = " "
	require.ErrorIs(t, emptyScene.Validate(), ErrInvalidSceneSpeechTiming)

	negativeDuration := valid
	negativeDuration.LocalDurationUS = -1
	require.ErrorIs(t, negativeDuration.Validate(), ErrInvalidSceneSpeechTiming)

	outOfOrderWords := valid
	outOfOrderWords.Words[1].StartUS = 50_000 // before word 0 end
	require.ErrorIs(t, outOfOrderWords.Validate(), ErrInvalidSceneSpeechTiming)

	wordPastDuration := valid
	wordPastDuration.LocalDurationUS = 150_000 // word 1 ends at 200_000
	require.ErrorIs(t, wordPastDuration.Validate(), ErrInvalidSceneSpeechTiming)

	driftedPhrase := valid
	driftedPhrase.Phrases[0].GlobalEndUS++
	require.ErrorIs(t, driftedPhrase.Validate(), ErrInvalidSceneSpeechTiming)
}
