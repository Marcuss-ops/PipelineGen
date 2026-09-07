package generation

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildPlan_CanonicalGenerationPackage(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"}})
	require.Equal(t, "topic", plan.Topic)
	require.Equal(t, "topic", plan.Title)
	require.Equal(t, "text", plan.Mode)
}

func TestBuildPlan_CopiesMetadataAndCanonicalizesProcessorNames(t *testing.T) {
	metadata := &scriptpkg.VideoMetadata{Title: "manual", Tags: []string{"one"}, TranslationStatus: "translated"}
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Language: "it", VideoMetadata: metadata,
		Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceClips, ClipIDs: []string{"clip-1"}},
		Output: scriptpkg.OutputSpec{TranslateTo: "it", SaveToDB: true},
	})
	require.NotNil(t, plan.VideoMetadata)
	require.Equal(t, "it", plan.VideoMetadata.Language)
	require.Empty(t, plan.VideoMetadata.TranslationStatus)
	metadata.Tags[0] = "changed"
	require.Equal(t, "one", plan.VideoMetadata.Tags[0])
	canonical := make(map[adapters.ProcessorName]struct{})
	for _, name := range adapters.CanonicalProcessorNames() {
		canonical[name] = struct{}{}
	}
	for _, name := range plan.Postprocessors {
		_, ok := canonical[adapters.ProcessorName(name)]
		require.True(t, ok, name)
	}
}

func TestBuildPlans_EmptyInputRemainsNil(t *testing.T) {
	require.Nil(t, BuildPlans(nil))
}

func TestBuildPlanPreservesStyleAndExtractionSelection(t *testing.T) {
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Style:  "documentary cinematic",
		Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "Ada"},
		MediaPlan: mediadomain.MediaPlanSpec{Extraction: mediadomain.MediaExtractionPolicy{
			Enabled:               true,
			Include:               []string{mediadomain.ExtractionIncludeEntities, mediadomain.ExtractionIncludeImportantPhrases},
			MaxEntitiesPerSegment: 3,
		}},
	})
	require.Equal(t, "documentary cinematic", plan.Style)
	require.True(t, plan.MediaPlan.Extraction.Includes(mediadomain.ExtractionIncludeEntities))
	require.True(t, plan.MediaPlan.Extraction.Includes(mediadomain.ExtractionIncludeImportantPhrases))
}
