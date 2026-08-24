package adapters

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

func TestMomentQueriesFromAnnotations(t *testing.T) {
	queries := momentQueriesFromAnnotations(&scriptpkg.SceneAnnotations{
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{
				Text:          "Vittorio Emanuele II",
				CanonicalName: "Vittorio Emanuele II",
				Type:          "PERSON",
				Mentions: []scriptpkg.AnnotationSpan{
					{Text: "Vittorio Emanuele II", StartRune: 30, EndRune: 51},
				},
			},
		},
		SecondaryEntities: []scriptpkg.AnnotatedEntity{
			{
				Text:          "Teano",
				CanonicalName: "Teano",
				Type:          "GPE",
				Mentions: []scriptpkg.AnnotationSpan{
					{Text: "Teano", StartRune: 20, EndRune: 25},
				},
			},
		},
		ImportantPhrases: []scriptpkg.AnnotationSpan{
			{Text: "incontro di Teano", StartRune: 10, EndRune: 25},
		},
		ImportantWords: []scriptpkg.AnnotationSpan{
			{Text: "Garibaldi", StartRune: 60, EndRune: 69},
		},
	})

	require.Len(t, queries, 4)
	require.Equal(t, audio.MomentEntity, queries[0].Kind)
	require.Equal(t, "Vittorio Emanuele II", queries[0].Value)
	require.Equal(t, audio.MomentEntity, queries[1].Kind)
	require.Equal(t, "Teano", queries[1].Value)
	require.Equal(t, audio.MomentPhrase, queries[2].Kind)
	require.Equal(t, "incontro di Teano", queries[2].Value)
	require.Equal(t, audio.MomentKeyword, queries[3].Kind)
	require.Equal(t, "Garibaldi", queries[3].Value)
}

func TestMomentQueriesFromAnnotations_NilAndEmpty(t *testing.T) {
	require.Nil(t, momentQueriesFromAnnotations(nil))
	require.Empty(t, momentQueriesFromAnnotations(&scriptpkg.SceneAnnotations{}))
}

func TestMomentQueriesFromAnnotations_EntityWithoutMentionsFallsBackToText(t *testing.T) {
	queries := momentQueriesFromAnnotations(&scriptpkg.SceneAnnotations{
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{Text: "Giuseppe Garibaldi", CanonicalName: "Giuseppe Garibaldi", Type: "PERSON"},
		},
	})
	require.Len(t, queries, 1)
	require.Equal(t, audio.MomentEntity, queries[0].Kind)
	require.Equal(t, "Giuseppe Garibaldi", queries[0].Value)
}
