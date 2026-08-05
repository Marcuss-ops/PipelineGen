package local

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/stretchr/testify/require"
)

func TestExtractor_MayaTextFindsUsefulEntitiesAndKeywords(t *testing.T) {
	extractor := NewExtractor()

	result, err := extractor.ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{
		Text: `La civiltà Maya si sviluppò in Mesoamerica.
		Tikal, Palenque e Chichén Itzá furono importanti città.
		Nella penisola dello Yucatán vennero costruiti templi, piramidi e osservatori astronomici.`,
		Title:       "La civiltà Maya",
		Language:    "it",
		Device:      "cpu",
		EntityCount: 10,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	allValues := make([]string, 0)
	for _, entity := range result.Persons {
		allValues = append(allValues, entity.Value)
	}
	for _, entity := range result.Places {
		allValues = append(allValues, entity.Value)
	}
	for _, entity := range result.Concepts {
		allValues = append(allValues, entity.Value)
	}

	require.NotEmpty(t, allValues)
	require.NotEmpty(t, result.ImportantPhrases)
	require.NotEmpty(t, result.ImportantWords)
	t.Logf("entities=%v", allValues)
	t.Logf("phrases=%v", result.ImportantPhrases)
	t.Logf("keywords=%v", result.ImportantWords)
}
