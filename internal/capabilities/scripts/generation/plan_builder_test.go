package generation

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
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
