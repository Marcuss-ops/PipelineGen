package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TestDocumentAudioSummaryFor_SumsClipAndVoiceoverTotals pins the capability
// boundary resolver: clip totals and voiceover totals are summed from the
// canonical clip durations and the canonical timeline, so the document
// renderer never performs that arithmetic itself.
func TestDocumentAudioSummaryFor_SumsClipAndVoiceoverTotals(t *testing.T) {
	result := &GenerateResult{
		Scenes: []Scene{
			{ID: "scene-0", Index: 0, Clip: &ClipReference{ID: "clip-0", DurationUS: 18_420_000, DurationSource: kernelasset.DurationProbe}},
			{ID: "scene-1", Index: 1, Clips: []*ClipReference{{ID: "clip-1", DurationUS: 24_300_000, DurationSource: kernelasset.DurationProbe}}},
		},
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Version: capabilityaudio.TimelineVersion,
			Segments: []capabilityaudio.TimelineSegment{
				{ID: "scene-0", Index: 0, AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioVoiceover, SourceDurationUS: 600_000}}},
				{ID: "scene-1", Index: 1, AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioVoiceover, SourceDurationUS: 700_000}}},
			},
		},
	}

	s := documentAudioSummaryFor(result)
	require.Equal(t, 2, s.ClipCount)
	require.True(t, s.ClipTotalKnown)
	require.Equal(t, int64(42_720_000), s.ClipTotalUS)
	require.Equal(t, 2, s.VoiceoverCount)
	require.Equal(t, int64(1_300_000), s.VoiceoverTotalUS)
}

// TestDocumentAudioSummaryFor_UnknownClipMarksTotalUnknown pins the
// fail-closed behavior: a clip with no known total duration marks the whole
// clip total as unknown instead of fabricating a zero-sum.
func TestDocumentAudioSummaryFor_UnknownClipMarksTotalUnknown(t *testing.T) {
	result := &GenerateResult{
		Scenes: []Scene{
			{ID: "scene-0", Index: 0, Clip: &ClipReference{ID: "clip-0"}},
		},
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{},
	}

	s := documentAudioSummaryFor(result)
	require.Equal(t, 1, s.ClipCount)
	require.False(t, s.ClipTotalKnown)
	require.Zero(t, s.ClipTotalUS)
}

// TestDocumentAudioSummaryFor_NilResultIsEmpty pins the nil receiver guard.
func TestDocumentAudioSummaryFor_NilResultIsEmpty(t *testing.T) {
	s := documentAudioSummaryFor(nil)
	require.Zero(t, s.ClipCount)
	require.Zero(t, s.VoiceoverCount)
	require.False(t, s.ClipTotalKnown)
}
