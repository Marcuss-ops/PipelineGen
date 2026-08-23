package generation

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func requiredTiming() *audio.TimingRequest {
	return &audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord, Formats: []audio.TimingFormat{audio.TimingJSON}}
}

func TestBuildPlan_PropagatesVoiceoverTiming(t *testing.T) {
	// Top-level audio.timing wins over output.audio.timing.
	plan := BuildPlan(scriptpkg.GenerationItemV2{
		Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"},
		Audio:  scriptpkg.AudioOutputConfig{Mode: "combined_timeline", Timing: requiredTiming()},
		Output: scriptpkg.OutputSpec{Audio: scriptpkg.AudioOutputConfig{Timing: &audio.TimingRequest{Mode: audio.TimingBestEffort}}},
	})
	require.NotNil(t, plan.Timing)
	require.Equal(t, audio.TimingRequired, plan.Timing.Mode)
	require.Equal(t, audio.BoundaryWord, plan.Timing.BoundaryMode)

	// Fallback to output.audio.timing when top-level audio.timing is nil.
	plan = BuildPlan(scriptpkg.GenerationItemV2{
		Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"},
		Output: scriptpkg.OutputSpec{Audio: scriptpkg.AudioOutputConfig{Timing: requiredTiming()}},
	})
	require.NotNil(t, plan.Timing)
	require.Equal(t, audio.TimingRequired, plan.Timing.Mode)

	// No timing anywhere → nil (canonical best_effort default applies later).
	plan = BuildPlan(scriptpkg.GenerationItemV2{Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"}})
	require.Nil(t, plan.Timing)
}
