package audio

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLocateSceneSpeechTiming_BundlesWordsAndPhrases pins the scene-level
// projection: it bundles the scene's verbatim word boundaries with the derived
// phrase spans, carrying the voiceover asset id and the certified local
// duration.
func TestLocateSceneSpeechTiming_BundlesWordsAndPhrases(t *testing.T) {
	timing := phraseLocatorArtifact()
	got, err := LocateSceneSpeechTiming(2, "scene-2", "vo-2", 8_200_000, timing, []string{
		"incontro di Teano",
		"Vittorio Emanuele II",
	})
	require.NoError(t, err)

	require.Equal(t, "scene-2", got.SceneID)
	require.Equal(t, "vo-2", got.VoiceoverAssetID)
	require.Equal(t, timing.DurationUS, got.LocalDurationUS)
	require.Len(t, got.Words, len(timing.Words))
	require.Equal(t, timing.Words, got.Words)
	require.Len(t, got.Phrases, 2)
	require.NoError(t, got.Validate())
}

// TestLocateSceneSpeechTiming_GlobalUsesCanonicalTimelineStart pins the master
// coordinate: every phrase global span is the scene's canonical timeline
// offset plus the local span — never recomputed or interpolated.
func TestLocateSceneSpeechTiming_GlobalUsesCanonicalTimelineStart(t *testing.T) {
	const timelineStartUS = int64(8_200_000)
	timing := phraseLocatorArtifact()
	got, err := LocateSceneSpeechTiming(1, "scene-1", "vo-1", timelineStartUS, timing, []string{"incontro di Teano"})
	require.NoError(t, err)

	p := got.Phrases[0]
	require.Equal(t, timelineStartUS, p.TimelineStartUS)
	require.Equal(t, timelineStartUS+p.LocalStartUS, p.GlobalStartUS)
	require.Equal(t, timelineStartUS+p.LocalEndUS, p.GlobalEndUS)
	require.Equal(t, int64(8_200_000+200_000), p.GlobalStartUS)
	require.Equal(t, int64(8_200_000+500_000), p.GlobalEndUS)
}

// TestLocateSceneSpeechTiming_MissingPhraseFailsClosed pins the whole-scene
// fail-closed contract: one phrase that does not occur verbatim aborts the
// projection, never a partial set of plausible-but-wrong timestamps.
func TestLocateSceneSpeechTiming_MissingPhraseFailsClosed(t *testing.T) {
	timing := phraseLocatorArtifact()
	_, err := LocateSceneSpeechTiming(0, "scene-0", "vo-0", 0, timing, []string{"Mussolini"})
	require.ErrorIs(t, err, ErrPhraseNotFound)
}

func TestLocateSceneSpeechTiming_InvalidTimingFailsClosed(t *testing.T) {
	timing := phraseLocatorArtifact()
	timing.BoundaryMode = BoundaryMode("sentence")
	_, err := LocateSceneSpeechTiming(0, "scene-0", "vo-0", 0, timing, []string{"Teano"})
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrPhraseNotFound))
}

func TestLocateSceneSpeechTiming_EmptySceneIDFailsClosed(t *testing.T) {
	timing := phraseLocatorArtifact()
	_, err := LocateSceneSpeechTiming(0, "  ", "vo-0", 0, timing, []string{"Teano"})
	require.ErrorIs(t, err, ErrInvalidSceneSpeechTiming)
}

// TestLocateSceneSpeechTiming_DoesNotAliasArtifactWords pins purity: the
// projection copies the word slice, so mutating the returned Words never
// mutates the caller's canonical artifact.
func TestLocateSceneSpeechTiming_DoesNotAliasArtifactWords(t *testing.T) {
	timing := phraseLocatorArtifact()
	before := timing.DeepCopy()
	got, err := LocateSceneSpeechTiming(0, "scene-0", "vo-0", 0, timing, []string{"Teano"})
	require.NoError(t, err)

	got.Words[0].Text = "MUTATED"
	require.Equal(t, before, timing)
}

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
