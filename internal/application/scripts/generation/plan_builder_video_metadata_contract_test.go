package generation

import (
	"testing"

	"github.com/stretchr/testify/require"

	script "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildPlan_CopiesProvidedVideoMetadata(t *testing.T) {
	item := script.GenerationItemV2{
		ID:       "test-video-1",
		Title:    "Titolo interno",
		Language: "it",
		VideoMetadata: &script.VideoMetadata{
			Title:       "Titolo YouTube manuale",
			Description: "Descrizione manuale",
			Tags:        []string{"boxe", "Mike Tyson"},
		},
		Source: script.SourceSpec{
			Type:       script.SourceText,
			Topic:      "Mike Tyson",
			SourceText: "Testo sorgente",
		},
	}

	plan := BuildPlan(item)

	require.NotNil(t, plan.VideoMetadata)
	require.Equal(t, "Titolo YouTube manuale", plan.VideoMetadata.Title)
	require.Equal(t, "Descrizione manuale", plan.VideoMetadata.Description)
	require.Equal(t, []string{"boxe", "Mike Tyson"}, plan.VideoMetadata.Tags)
	require.Equal(t, "it", plan.VideoMetadata.Language)
}

func TestBuildPlan_VideoMetadataDefaultsLanguageAndClearsTranslationStatus(t *testing.T) {
	item := script.GenerationItemV2{
		Language: "it",
		VideoMetadata: &script.VideoMetadata{
			Language:          "   ",
			Title:             "Titolo manuale",
			TranslationStatus: "translated",
		},
		Source: script.SourceSpec{
			Type:       script.SourceText,
			Topic:      "Test",
			SourceText: "Testo",
		},
	}

	plan := BuildPlan(item)

	require.NotNil(t, plan.VideoMetadata)
	require.Equal(t, "it", plan.VideoMetadata.Language)
	require.Empty(t, plan.VideoMetadata.TranslationStatus)
}

func TestBuildPlan_VideoMetadataIsDefensivelyCopied(t *testing.T) {
	metadata := &script.VideoMetadata{
		Title: "Titolo originale",
		Tags:  []string{"uno", "due"},
	}

	item := script.GenerationItemV2{
		Language:      "it",
		VideoMetadata: metadata,
		Source: script.SourceSpec{
			Type:       script.SourceText,
			Topic:      "Test",
			SourceText: "Testo",
		},
	}

	plan := BuildPlan(item)

	metadata.Title = "Titolo modificato"
	metadata.Tags[0] = "modificato"

	require.Equal(t, "Titolo originale", plan.VideoMetadata.Title)
	require.Equal(t, "uno", plan.VideoMetadata.Tags[0])
}

func TestNewResolvedGenerationPlan_ClonesVideoMetadata(t *testing.T) {
	metadata := &script.VideoMetadata{
		Title:             "Titolo",
		Description:       "Descrizione",
		Tags:              []string{"tag-1", "tag-2"},
		TranslationStatus: "translated",
	}

	plan := script.NewResolvedGenerationPlan(script.ResolvedGenerationPlan{
		Language:      "it",
		VideoMetadata: metadata,
	})

	require.NotNil(t, plan.VideoMetadata)
	metadata.Title = "modificato"
	metadata.Tags[0] = "modificato"

	require.Equal(t, "Titolo", plan.VideoMetadata.Title)
	require.Equal(t, []string{"tag-1", "tag-2"}, plan.VideoMetadata.Tags)
	require.Equal(t, "translated", plan.VideoMetadata.TranslationStatus)
}

func TestBatch_ItemsKeepIndependentVideoMetadata(t *testing.T) {
	items := []script.GenerationItemV2{
		{
			ID: "video-1",
			VideoMetadata: &script.VideoMetadata{
				Title: "Titolo uno",
			},
		},
		{
			ID: "video-2",
			VideoMetadata: &script.VideoMetadata{
				Title: "Titolo due",
			},
		},
	}

	plans := BuildPlans(items)

	require.Equal(t, "Titolo uno", plans[0].VideoMetadata.Title)
	require.Equal(t, "Titolo due", plans[1].VideoMetadata.Title)
}
