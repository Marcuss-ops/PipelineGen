package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func momentArtifact() SpeechTimingArtifact {
	return SpeechTimingArtifact{
		Version:      SpeechTimingVersion,
		BoundaryMode: BoundaryWord,
		TextSHA256:   "text-hash",
		AudioSHA256:  "audio-hash",
		DurationUS:   1_000_000,
		Words: []SpeechWordTiming{
			{Index: 0, Text: "Il", StartUS: 0, EndUS: 100_000},
			{Index: 1, Text: "celebre", StartUS: 100_000, EndUS: 200_000},
			{Index: 2, Text: "incontro", StartUS: 200_000, EndUS: 300_000},
			{Index: 3, Text: "di", StartUS: 300_000, EndUS: 400_000},
			{Index: 4, Text: "Teano", StartUS: 400_000, EndUS: 500_000},
			{Index: 5, Text: "con", StartUS: 500_000, EndUS: 600_000},
			{Index: 6, Text: "Vittorio", StartUS: 600_000, EndUS: 700_000},
			{Index: 7, Text: "Emanuele", StartUS: 700_000, EndUS: 800_000},
			{Index: 8, Text: "II", StartUS: 800_000, EndUS: 900_000},
			{Index: 9, Text: "a", StartUS: 900_000, EndUS: 950_000},
			{Index: 10, Text: "corso", StartUS: 950_000, EndUS: 1_000_000},
		},
	}
}

func TestProjectMoments_JoinsRustAlignmentsToQueries(t *testing.T) {
	timing := momentArtifact()
	moments, err := ProjectMoments(timing, []MomentQuery{
		{Kind: MomentEntity, Value: "Vittorio Emanuele II"},
		{Kind: MomentPhrase, Value: "incontro di Teano"},
		{Kind: MomentKeyword, Value: "Teano"},
	}, []LocatedPhrase{
		{Text: "Vittorio Emanuele II", Occurrence: 1, WordStart: 6, WordEnd: 8, StartUS: 600_000, EndUS: 900_000},
		{Text: "incontro di Teano", Occurrence: 1, WordStart: 2, WordEnd: 4, StartUS: 200_000, EndUS: 500_000},
		{Text: "Teano", Occurrence: 1, WordStart: 4, WordEnd: 4, StartUS: 400_000, EndUS: 500_000},
	})
	require.NoError(t, err)
	require.Len(t, moments, 3)

	entity := moments[0]
	require.Equal(t, MomentEntity, entity.Kind)
	require.Equal(t, "Vittorio Emanuele II", entity.Value)
	require.Equal(t, 6, entity.WordStart)
	require.Equal(t, 8, entity.WordEnd)
	require.Equal(t, int64(600_000), entity.StartUS)
	require.Equal(t, int64(900_000), entity.EndUS)

	phrase := moments[1]
	require.Equal(t, MomentPhrase, phrase.Kind)
	require.Equal(t, int64(200_000), phrase.StartUS)
	require.Equal(t, int64(500_000), phrase.EndUS)

	keyword := moments[2]
	require.Equal(t, MomentKeyword, keyword.Kind)
	require.Equal(t, int64(400_000), keyword.StartUS)
	require.Equal(t, int64(500_000), keyword.EndUS)
}

func TestProjectMoments_RepeatedValueReturnsAllOccurrences(t *testing.T) {
	moments, err := ProjectMoments(momentArtifact(), []MomentQuery{{Kind: MomentEntity, Value: "Garibaldi"}}, []LocatedPhrase{
		{Text: "Garibaldi", Occurrence: 1, WordStart: 0, WordEnd: 0, StartUS: 0, EndUS: 100_000},
		{Text: "Garibaldi", Occurrence: 2, WordStart: 4, WordEnd: 4, StartUS: 400_000, EndUS: 500_000},
	})
	require.NoError(t, err)
	require.Len(t, moments, 2)
	require.Equal(t, 1, moments[0].Occurrence)
	require.Equal(t, int64(0), moments[0].StartUS)
	require.Equal(t, 2, moments[1].Occurrence)
	require.Equal(t, int64(400_000), moments[1].StartUS)
}

func TestProjectMoments_UnreturnedValueIsSkipped(t *testing.T) {
	moments, err := ProjectMoments(momentArtifact(), []MomentQuery{
		{Kind: MomentEntity, Value: "Teano"},
		{Kind: MomentEntity, Value: "Mussolini"}, // not aligned by the worker — skipped
	}, []LocatedPhrase{
		{Text: "Teano", Occurrence: 1, WordStart: 4, WordEnd: 4, StartUS: 400_000, EndUS: 500_000},
	})
	require.NoError(t, err)
	require.Len(t, moments, 1)
	require.Equal(t, "Teano", moments[0].Value)
}

func TestProjectMoments_EmptyValueSkipped(t *testing.T) {
	moments, err := ProjectMoments(momentArtifact(), []MomentQuery{
		{Kind: MomentEntity, Value: "   "},
		{Kind: MomentPhrase, Value: "Teano"},
	}, []LocatedPhrase{
		{Text: "Teano", Occurrence: 1, WordStart: 4, WordEnd: 4, StartUS: 400_000, EndUS: 500_000},
	})
	require.NoError(t, err)
	require.Len(t, moments, 1)
	require.Equal(t, MomentPhrase, moments[0].Kind)
}

func TestProjectMoments_DeduplicatesIdenticalQueries(t *testing.T) {
	moments, err := ProjectMoments(momentArtifact(), []MomentQuery{
		{Kind: MomentEntity, Value: "Teano"},
		{Kind: MomentEntity, Value: "Teano"},
		{Kind: MomentEntity, Value: "teano"},
	}, []LocatedPhrase{
		{Text: "Teano", Occurrence: 1, WordStart: 4, WordEnd: 4, StartUS: 400_000, EndUS: 500_000},
	})
	require.NoError(t, err)
	require.Len(t, moments, 1)
}

func TestProjectMoments_PreservesQueryOrder(t *testing.T) {
	moments, err := ProjectMoments(momentArtifact(), []MomentQuery{
		{Kind: MomentKeyword, Value: "corso"},
		{Kind: MomentEntity, Value: "Teano"},
		{Kind: MomentPhrase, Value: "incontro di Teano"},
	}, []LocatedPhrase{
		{Text: "corso", Occurrence: 1, WordStart: 10, WordEnd: 10, StartUS: 950_000, EndUS: 1_000_000},
		{Text: "Teano", Occurrence: 1, WordStart: 4, WordEnd: 4, StartUS: 400_000, EndUS: 500_000},
		{Text: "incontro di Teano", Occurrence: 1, WordStart: 2, WordEnd: 4, StartUS: 200_000, EndUS: 500_000},
	})
	require.NoError(t, err)
	require.Len(t, moments, 3)
	require.Equal(t, MomentKeyword, moments[0].Kind)
	require.Equal(t, MomentEntity, moments[1].Kind)
	require.Equal(t, MomentPhrase, moments[2].Kind)
}

func TestProjectMoments_RejectsInvalidTiming(t *testing.T) {
	timing := momentArtifact()
	timing.Words[1].EndUS = 0 // end before start
	_, err := ProjectMoments(timing, []MomentQuery{{Kind: MomentEntity, Value: "Teano"}}, nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrPhraseNotFound)
}

func TestProjectMoments_NoQueriesReturnsEmpty(t *testing.T) {
	moments, err := ProjectMoments(momentArtifact(), nil, nil)
	require.NoError(t, err)
	require.Empty(t, moments)
}

func TestProjectMoments_DoesNotMutateArtifact(t *testing.T) {
	timing := momentArtifact()
	before := timing.DeepCopy()
	_, err := ProjectMoments(timing, []MomentQuery{{Kind: MomentEntity, Value: "Vittorio Emanuele II"}}, []LocatedPhrase{
		{Text: "Vittorio Emanuele II", Occurrence: 1, WordStart: 6, WordEnd: 8, StartUS: 600_000, EndUS: 900_000},
	})
	require.NoError(t, err)
	require.Equal(t, before, timing)
}
