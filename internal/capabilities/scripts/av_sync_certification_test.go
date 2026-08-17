package scriptgeneration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// combinedScene builds a COMBINED_TIMELINE clip-bound scene: a clip whose
// visual window is [0, clipMS) and a certified voiceover of voSeconds. The
// clip audio intent carries its own timeline window (clipUS) so the clip
// track ends with the clip instead of stretching to the scene window.
func combinedScene(sceneID, clipID string, index int, clipMS int64, voSeconds float64) Scene {
	clipUS := clipMS * 1000
	clip := &ClipReference{ID: clipID, AudioPath: "/media/" + clipID + ".m4a", SourceInMS: 0, SourceOutMS: clipMS}
	clipIntent := audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: clipID, SourceInUS: 0, SourceDurationUS: clipUS, TimelineOffsetUS: 0, TimelineDurationUS: clipUS, UseOriginalAudio: true}
	return Scene{
		ID:    sceneID,
		Index: index,
		Clip:  clip,
		Clips: []*ClipReference{clip},
		Audio: clipIntent,
		AudioIntents: []audio.AudioIntent{
			clipIntent,
		},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-" + clipID, FilePath: "/media/vo-" + clipID + ".m4a", Duration: voSeconds}},
	}
}

func voiceoverIntent(t *testing.T, segment audio.TimelineSegment) audio.AudioIntent {
	t.Helper()
	for _, intent := range segment.EffectiveAudioIntents() {
		if intent.Mode == audio.AudioVoiceover {
			return intent
		}
	}
	t.Fatalf("no voiceover intent on segment %s: %+v", segment.ID, segment.AudioIntents)
	return audio.AudioIntent{}
}

// TestAVSyncCertificationFreezeTail certifies the canonical freeze case:
// clip 16s + VO 17.52s → scene duration 17.52s, video 16s real + 1.52s
// freeze, VO source 17.52s, and the clip audio track ending at 16s.
func TestAVSyncCertificationFreezeTail(t *testing.T) {
	result := GenerateResult{AudioMode: audio.AudioModeCombinedTimeline, Scenes: []Scene{combinedScene("scene-8", "clip-16", 0, 16000, 17.52)}}
	timeline, plan, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	require.NoError(t, err)

	// Scene duration = max(video 16s, VO 17.52s) = 17.52s.
	require.Equal(t, int64(17_520_000), timeline.DurationUS)
	require.Equal(t, timeline.DurationUS, plan.DurationUS)

	// Video: real clip 16s + synthetic freeze tail 1.52s.
	videos := timeline.Segments[0].EffectiveVideoSegments()
	require.Len(t, videos, 2)
	require.False(t, videos[0].Freeze)
	require.Equal(t, int64(16_000_000), videos[0].TimelineDurationUS)
	require.True(t, videos[1].Freeze)
	require.Equal(t, int64(16_000_000), videos[1].TimelineOffsetUS)
	require.Equal(t, int64(1_520_000), videos[1].TimelineDurationUS)

	// VO source duration is the certified speech length, not the clip.
	vo := voiceoverIntent(t, timeline.Segments[0])
	require.Equal(t, int64(17_520_000), vo.SourceDurationUS)
	require.Equal(t, int64(17_520_000), vo.TimelineDurationUS)

	// Clip audio ends with the clip (16s), never stretched to the scene.
	clipEvents := eventsForRole(plan, audio.TrackClipAudio)
	require.Len(t, clipEvents, 1)
	require.Equal(t, int64(16_000_000), clipEvents[0].DurationUS)
}

// TestAVSyncCertificationClipWins certifies the inverse case: clip 18.8s +
// VO 17.544s → scene duration 18.8s, no freeze, and the ducking window ends
// at 17.544s (the real speech length), leaving a 1.256s base-gain remainder.
func TestAVSyncCertificationClipWins(t *testing.T) {
	result := GenerateResult{AudioMode: audio.AudioModeCombinedTimeline, Scenes: []Scene{combinedScene("scene-0", "clip-18", 0, 18800, 17.544)}}
	timeline, plan, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	require.NoError(t, err)

	// Scene duration = max(video 18.8s, VO 17.544s) = 18.8s.
	require.Equal(t, int64(18_800_000), timeline.DurationUS)

	// No freeze: the clip covers the scene.
	videos := timeline.Segments[0].EffectiveVideoSegments()
	require.Len(t, videos, 1)
	require.False(t, videos[0].Freeze)
	require.Equal(t, int64(18_800_000), videos[0].TimelineDurationUS)

	// VO source duration is the certified 17.544s, placed in the 18.8s window.
	vo := voiceoverIntent(t, timeline.Segments[0])
	require.Equal(t, int64(17_544_000), vo.SourceDurationUS)
	require.Equal(t, int64(18_800_000), vo.TimelineDurationUS)

	// Ducking ends at the real speech end (17.544s), not the scene window.
	require.Len(t, plan.Automation, 1)
	duck := plan.Automation[0]
	require.Equal(t, int64(0), duck.StartUS)
	require.Equal(t, int64(17_544_000), duck.EndUS)
	require.Equal(t, audio.DuckClipActiveGainDB, duck.GainDB)
	require.Equal(t, int64(1_256_000), int64(18_800_000)-duck.EndUS, "base-gain remainder must be 18.8s - 17.544s = 1.256s")
}

// TestAVSyncCertificationFullExample certifies the complete 10-scene example:
// the canonical timeline must be exactly 181.920s and every duration surface —
// CanonicalTimeline, AudioPlan, and FinalAudio — must coincide within the
// master tolerance.
func TestAVSyncCertificationFullExample(t *testing.T) {
	type row struct {
		clipID string
		clipMS int64
		voSec  float64
	}
	rows := []row{
		{"clip-0", 18800, 17.544},
		{"clip-1", 18800, 17.112},
		{"clip-2", 16400, 16.128},
		{"clip-3", 16400, 14.784},
		{"clip-4", 19200, 17.256},
		{"clip-5", 18400, 16.896},
		{"clip-6", 19200, 17.424},
		{"clip-7", 18800, 17.856},
		{"clip-8", 16000, 17.520}, // freeze: VO 17.52s > clip 16s
		{"clip-9", 18400, 16.465},
	}
	const expectedCanonicalUS = int64(181_920_000)

	scenes := make([]Scene, len(rows))
	for i, r := range rows {
		scenes[i] = combinedScene("scene-"+r.clipID, r.clipID, i, r.clipMS, r.voSec)
	}
	result := GenerateResult{AudioMode: audio.AudioModeCombinedTimeline, Scenes: scenes}

	timeline, plan, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	require.NoError(t, err)

	// CanonicalTimeline.Duration must be exactly 181.920s.
	require.Equal(t, expectedCanonicalUS, timeline.DurationUS)
	// AudioPlan.Duration must agree exactly.
	require.Equal(t, expectedCanonicalUS, plan.DurationUS)

	// Scene 8 is the freeze scene: 17.52s with a synthetic freeze tail.
	require.Equal(t, int64(17_520_000), timeline.Segments[8].DurationUS)
	seg8Videos := timeline.Segments[8].EffectiveVideoSegments()
	require.Len(t, seg8Videos, 2)
	require.True(t, seg8Videos[1].Freeze)

	// FinalAudio: certify a master whose duration equals the canonical plan.
	finalAudio := FinalAudioReference{
		AssetID:              "final-audio-full-example",
		Path:                 "/audio/final_audio.m4a",
		AudioContractVersion: audio.AudioContractVersion,
		AudioPlanVersion:     plan.Version,
		PlanSHA256:           plan.PlanSHA256,
		FinalAudioSHA256:     strings.Repeat("0", 64),
		Codec:                plan.Output.Codec,
		Profile:              plan.Output.Profile,
		SampleRate:           plan.Output.SampleRate,
		Channels:             plan.Output.Channels,
		ChannelLayout:        plan.Output.ChannelLayout,
		Bitrate:              128000,
		DurationUS:           expectedCanonicalUS,
		DurationMS:           expectedCanonicalUS / 1000,
		StartPTS:             0,
		SizeBytes:            1,
		FinalMix:             true,
		CopyEligible:         true,
	}
	require.NoError(t, ValidateFinalAudioReference(finalAudio, plan))
	require.NoError(t, ValidateMasterAudioInvariants(timeline, plan, finalAudio))

	// Every audio duration surface must coincide with the canonical timeline
	// within the master tolerance. PipelineGen is audio-only: there is no
	// video render surface anymore.
	require.Equal(t, expectedCanonicalUS, timeline.DurationUS)
	require.Equal(t, expectedCanonicalUS, plan.DurationUS)
	require.Equal(t, expectedCanonicalUS, finalAudio.DurationUS)
}
