package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocateMoments_EntityPhraseKeyword(t *testing.T) {
	timing := phraseLocatorArtifact()
	moments, err := LocateMoments(timing, []MomentQuery{
		{Kind: MomentEntity, Value: "Vittorio Emanuele II"},
		{Kind: MomentPhrase, Value: "incontro di Teano"},
		{Kind: MomentKeyword, Value: "Teano"},
	})
	require.NoError(t, err)
	require.Len(t, moments, 3)

	entity := moments[0]
	require.Equal(t, MomentEntity, entity.Kind)
	require.Equal(t, "Vittorio Emanuele II", entity.Value)
	require.Equal(t, 7, entity.WordStart)
	require.Equal(t, 9, entity.WordEnd)
	require.Equal(t, int64(700_000), entity.StartUS)
	require.Equal(t, int64(1_000_000), entity.EndUS)

	phrase := moments[1]
	require.Equal(t, MomentPhrase, phrase.Kind)
	require.Equal(t, int64(200_000), phrase.StartUS)
	require.Equal(t, int64(500_000), phrase.EndUS)

	keyword := moments[2]
	require.Equal(t, MomentKeyword, keyword.Kind)
	require.Equal(t, int64(400_000), keyword.StartUS)
	require.Equal(t, int64(500_000), keyword.EndUS)
}

func TestLocateMoments_RepeatedValueReturnsAllOccurrences(t *testing.T) {
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
	moments, err := LocateMoments(timing, []MomentQuery{{Kind: MomentEntity, Value: "Garibaldi"}})
	require.NoError(t, err)
	require.Len(t, moments, 2)
	require.Equal(t, 1, moments[0].Occurrence)
	require.Equal(t, int64(0), moments[0].StartUS)
	require.Equal(t, 2, moments[1].Occurrence)
	require.Equal(t, int64(200_000), moments[1].StartUS)
}

func TestLocateMoments_NotFoundIsSkipped(t *testing.T) {
	timing := phraseLocatorArtifact()
	moments, err := LocateMoments(timing, []MomentQuery{
		{Kind: MomentEntity, Value: "Teano"},
		{Kind: MomentEntity, Value: "Mussolini"}, // not present — skipped
	})
	require.NoError(t, err)
	require.Len(t, moments, 1)
	require.Equal(t, "Teano", moments[0].Value)
}

func TestLocateMoments_EmptyValueSkipped(t *testing.T) {
	timing := phraseLocatorArtifact()
	moments, err := LocateMoments(timing, []MomentQuery{
		{Kind: MomentEntity, Value: "   "},
		{Kind: MomentPhrase, Value: "Teano"},
	})
	require.NoError(t, err)
	require.Len(t, moments, 1)
	require.Equal(t, MomentPhrase, moments[0].Kind)
}

func TestLocateMoments_DeduplicatesIdenticalQueries(t *testing.T) {
	timing := phraseLocatorArtifact()
	moments, err := LocateMoments(timing, []MomentQuery{
		{Kind: MomentEntity, Value: "Teano"},
		{Kind: MomentEntity, Value: "Teano"},
		{Kind: MomentEntity, Value: "teano"},
	})
	require.NoError(t, err)
	require.Len(t, moments, 1)
}

func TestLocateMoments_PreservesQueryOrder(t *testing.T) {
	timing := phraseLocatorArtifact()
	moments, err := LocateMoments(timing, []MomentQuery{
		{Kind: MomentKeyword, Value: "corso"},
		{Kind: MomentEntity, Value: "Teano"},
		{Kind: MomentPhrase, Value: "incontro di Teano"},
	})
	require.NoError(t, err)
	require.Len(t, moments, 3)
	require.Equal(t, MomentKeyword, moments[0].Kind)
	require.Equal(t, MomentEntity, moments[1].Kind)
	require.Equal(t, MomentPhrase, moments[2].Kind)
}

func TestLocateMoments_RejectsInvalidTiming(t *testing.T) {
	timing := phraseLocatorArtifact()
	timing.Words[1].EndUS = 0 // end before start
	_, err := LocateMoments(timing, []MomentQuery{{Kind: MomentEntity, Value: "Teano"}})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrPhraseNotFound)
}

func TestLocateMoments_NoQueriesReturnsEmpty(t *testing.T) {
	moments, err := LocateMoments(phraseLocatorArtifact(), nil)
	require.NoError(t, err)
	require.Empty(t, moments)
}

func TestLocateMoments_DoesNotMutateArtifact(t *testing.T) {
	timing := phraseLocatorArtifact()
	before := timing.DeepCopy()
	_, err := LocateMoments(timing, []MomentQuery{{Kind: MomentEntity, Value: "Vittorio Emanuele II"}})
	require.NoError(t, err)
	require.Equal(t, before, timing)
}
