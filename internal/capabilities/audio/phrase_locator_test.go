package audio

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// phraseLocatorArtifact builds the golden scene timing
// ("Il celebre incontro di Teano con re Vittorio Emanuele II cambiò il
// corso degli eventi.") with 100ms per word so every assertion is exact.
func phraseLocatorArtifact() SpeechTimingArtifact {
	texts := []string{
		"Il", "celebre", "incontro", "di", "Teano", "con", "re",
		"Vittorio", "Emanuele", "II", "cambiò", "il", "corso", "degli", "eventi",
	}
	words := make([]SpeechWordTiming, len(texts))
	for i, text := range texts {
		words[i] = SpeechWordTiming{
			Index:   i,
			Text:    text,
			StartUS: int64(i) * 100_000,
			EndUS:   int64(i+1) * 100_000,
		}
	}
	return SpeechTimingArtifact{
		Version:      SpeechTimingVersion,
		Provider:     "edge_tts",
		BoundaryMode: BoundaryWord,
		Language:     "it",
		TextSHA256:   "text-hash",
		AudioSHA256:  "audio-hash",
		DurationUS:   int64(len(texts)) * 100_000,
		Words:        words,
	}
}

func TestPhraseLocator_SingleWord(t *testing.T) {
	results, err := LocatePhrase(phraseLocatorArtifact(), "Teano")
	require.NoError(t, err)
	require.Len(t, results, 1)
	got := results[0]
	require.Equal(t, "Teano", got.Text)
	require.Equal(t, 1, got.Occurrence)
	require.Equal(t, 4, got.WordStart)
	require.Equal(t, 4, got.WordEnd)
	require.Equal(t, int64(400_000), got.StartUS)
	require.Equal(t, int64(500_000), got.EndUS)
}

func TestPhraseLocator_MultiWord(t *testing.T) {
	results, err := LocatePhrase(phraseLocatorArtifact(), "incontro di Teano")
	require.NoError(t, err)
	require.Len(t, results, 1)
	got := results[0]
	require.Equal(t, "incontro di Teano", got.Text)
	require.Equal(t, 2, got.WordStart)
	require.Equal(t, 4, got.WordEnd)
	require.Equal(t, int64(200_000), got.StartUS)
	require.Equal(t, int64(500_000), got.EndUS)
}

// TestPhraseLocator_UsesFirstStartLastEnd pins the span contract: the
// phrase's StartUS is the FIRST word's start and EndUS is the LAST word's
// end — never interpolated or invented.
func TestPhraseLocator_UsesFirstStartLastEnd(t *testing.T) {
	results, err := LocatePhrase(phraseLocatorArtifact(), "Vittorio Emanuele II")
	require.NoError(t, err)
	require.Len(t, results, 1)
	got := results[0]
	require.Equal(t, 7, got.WordStart)
	require.Equal(t, 9, got.WordEnd)
	require.Equal(t, int64(700_000), got.StartUS)
	require.Equal(t, int64(1_000_000), got.EndUS)
}

func TestPhraseLocator_CaseInsensitive(t *testing.T) {
	timing := SpeechTimingArtifact{
		Version:      SpeechTimingVersion,
		BoundaryMode: BoundaryWord,
		TextSHA256:   "text-hash",
		AudioSHA256:  "audio-hash",
		DurationUS:   200_000,
		Words: []SpeechWordTiming{
			{Index: 0, Text: "Garibaldi", StartUS: 0, EndUS: 100_000},
			{Index: 1, Text: "vinse", StartUS: 100_000, EndUS: 200_000},
		},
	}
	for _, phrase := range []string{"GARIBALDI", "garibaldi", "gArIbAlDi"} {
		results, err := LocatePhrase(timing, phrase)
		require.NoError(t, err, "phrase %q", phrase)
		require.Len(t, results, 1)
		require.Equal(t, 0, results[0].WordStart)
		require.Equal(t, int64(0), results[0].StartUS)
		require.Equal(t, int64(100_000), results[0].EndUS)
	}
}

func TestPhraseLocator_PunctuationInsensitive(t *testing.T) {
	// Trailing punctuation on the query and on the artifact word both match.
	results, err := LocatePhrase(phraseLocatorArtifact(), "Teano.")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 4, results[0].WordStart)

	// Leading/trailing quotes around the phrase.
	results, err = LocatePhrase(phraseLocatorArtifact(), `"Vittorio Emanuele II,"`)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 7, results[0].WordStart)
	require.Equal(t, 9, results[0].WordEnd)
}

func TestPhraseLocator_ItalianApostrophes(t *testing.T) {
	timing := SpeechTimingArtifact{
		Version:      SpeechTimingVersion,
		BoundaryMode: BoundaryWord,
		TextSHA256:   "text-hash",
		AudioSHA256:  "audio-hash",
		DurationUS:   200_000,
		Words: []SpeechWordTiming{
			{Index: 0, Text: "Nel", StartUS: 0, EndUS: 50_000},
			{Index: 1, Text: "cuore", StartUS: 50_000, EndUS: 100_000},
			{Index: 2, Text: "dell'Italia", StartUS: 100_000, EndUS: 150_000},
			{Index: 3, Text: "risiede", StartUS: 150_000, EndUS: 200_000},
		},
	}
	// ASCII apostrophe in the artifact, typographic (U+2019) in the query.
	results, err := LocatePhrase(timing, "dell’Italia")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 2, results[0].WordStart)
	require.Equal(t, int64(100_000), results[0].StartUS)
	require.Equal(t, int64(150_000), results[0].EndUS)

	// Reverse: typographic apostrophe in the artifact, ASCII in the query.
	timing.Words[2].Text = "dell’Italia"
	results, err = LocatePhrase(timing, "dell'Italia")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 2, results[0].WordStart)
}

func TestPhraseLocator_AccentedText(t *testing.T) {
	// NFC precomposed "cambiò" in the artifact; NFD decomposed query.
	results, err := LocatePhrase(phraseLocatorArtifact(), "cambio\u0300")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 10, results[0].WordStart)
	require.Equal(t, int64(1_000_000), results[0].StartUS)
	require.Equal(t, int64(1_100_000), results[0].EndUS)

	// Uppercase accented query matches the lowercase artifact word.
	results, err = LocatePhrase(phraseLocatorArtifact(), "CAMBIÒ")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 10, results[0].WordStart)
}

func TestPhraseLocator_RepeatedPhraseReturnsAll(t *testing.T) {
	timing := SpeechTimingArtifact{
		Version:      SpeechTimingVersion,
		BoundaryMode: BoundaryWord,
		TextSHA256:   "text-hash",
		AudioSHA256:  "audio-hash",
		DurationUS:   300_000,
		Words: []SpeechWordTiming{
			{Index: 0, Text: "Garibaldi", StartUS: 0, EndUS: 100_000},
			{Index: 1, Text: "incontrò", StartUS: 100_000, EndUS: 200_000},
			{Index: 2, Text: "Garibaldi", StartUS: 200_000, EndUS: 300_000},
		},
	}
	results, err := LocatePhrase(timing, "Garibaldi")
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, 1, results[0].Occurrence)
	require.Equal(t, 0, results[0].WordStart)
	require.Equal(t, int64(0), results[0].StartUS)
	require.Equal(t, 2, results[1].Occurrence)
	require.Equal(t, 2, results[1].WordStart)
	require.Equal(t, int64(200_000), results[1].StartUS)
}

func TestPhraseLocator_SelectOccurrence(t *testing.T) {
	timing := SpeechTimingArtifact{
		Version:      SpeechTimingVersion,
		BoundaryMode: BoundaryWord,
		TextSHA256:   "text-hash",
		AudioSHA256:  "audio-hash",
		DurationUS:   300_000,
		Words: []SpeechWordTiming{
			{Index: 0, Text: "a", StartUS: 0, EndUS: 50_000},
			{Index: 1, Text: "Garibaldi", StartUS: 50_000, EndUS: 100_000},
			{Index: 2, Text: "b", StartUS: 100_000, EndUS: 150_000},
			{Index: 3, Text: "Garibaldi", StartUS: 150_000, EndUS: 200_000},
			{Index: 4, Text: "c", StartUS: 200_000, EndUS: 250_000},
			{Index: 5, Text: "Garibaldi", StartUS: 250_000, EndUS: 300_000},
		},
	}
	results, err := LocatePhrase(timing, "Garibaldi")
	require.NoError(t, err)
	require.Len(t, results, 3)
	// The second occurrence is the caller's "select occurrence 2" anchor.
	second := results[1]
	require.Equal(t, 2, second.Occurrence)
	require.Equal(t, 3, second.WordStart)
	require.Equal(t, int64(150_000), second.StartUS)
	third := results[2]
	require.Equal(t, 3, third.Occurrence)
	require.Equal(t, 5, third.WordStart)
	require.Equal(t, int64(250_000), third.StartUS)
}

func TestPhraseLocator_NotFoundTypedError(t *testing.T) {
	_, err := LocatePhrase(phraseLocatorArtifact(), "Mussolini")
	require.ErrorIs(t, err, ErrPhraseNotFound)

	// Empty phrase and punctuation-only phrase are both "not found", never
	// fuzzy-matched and never silently empty results.
	_, err = LocatePhrase(phraseLocatorArtifact(), "")
	require.ErrorIs(t, err, ErrPhraseNotFound)
	_, err = LocatePhrase(phraseLocatorArtifact(), "!!!")
	require.ErrorIs(t, err, ErrPhraseNotFound)
}

// TestPhraseLocator_RejectsInvalidTiming pins fail-closed behavior: an
// artifact that fails Validate must surface the validation error, never a
// plausible timestamp.
func TestPhraseLocator_RejectsInvalidTiming(t *testing.T) {
	timing := phraseLocatorArtifact()
	timing.Words[2].EndUS = 50_000 // end before start
	_, err := LocatePhrase(timing, "incontro")
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrPhraseNotFound))
}

// TestPhraseLocator_DoesNotMutateArtifact pins purity: locating must never
// modify the artifact (callers hold the SSOT).
func TestPhraseLocator_DoesNotMutateArtifact(t *testing.T) {
	timing := phraseLocatorArtifact()
	before := timing.DeepCopy()
	_, err := LocatePhrase(timing, "Vittorio Emanuele II")
	require.NoError(t, err)
	require.Equal(t, before, timing)
}
