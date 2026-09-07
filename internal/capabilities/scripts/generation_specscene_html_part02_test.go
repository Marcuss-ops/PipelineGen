package scriptgeneration_test

import (
	"encoding/json"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
	"html"
	"strings"
	"testing"
)

func TestDocument_AudioOnlySceneShowsNoVideoClip(t *testing.T) {
	t.Parallel()

	timeline := &capabilityaudio.CanonicalTimeline{
		Version:    capabilityaudio.TimelineVersion,
		DurationUS: 134_832_000,
		Segments: []capabilityaudio.TimelineSegment{{
			ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: 134_832_000,
			Audio: capabilityaudio.AudioIntent{
				Mode: capabilityaudio.AudioVoiceover, VoiceoverAssetID: "vo-only",
				SourceDurationUS: 134_832_000, TimelineDurationUS: 134_832_000,
			},
		}},
	}
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  []scriptpkg.SpecScene{{ID: "scene-0", Index: 0, Text: "Audio only."}},
	}}

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
		AudioTimeline: timeline,
		AudioSummary: capabilityaudio.DocumentAudioSummary{
			ClipCount: 0, ClipTotalUS: 0, ClipTotalKnown: true,
			VoiceoverCount: 1, VoiceoverTotalUS: 134_832_000,
		},
	})
	human := humanDocumentHTML(t, out)
	require.Contains(t, human, "<h3>Video Clip</h3><p>None</p>")
	require.Contains(t, human, "<strong>Duration:</strong> 02:14.832")
	require.Contains(t, human, "<strong>Source Duration:</strong> 02:14.832")
	require.Contains(t, human, "<strong>Asset:</strong> vo-only")
	require.Contains(t, human, "<strong>Total Source Clip Duration:</strong> Unknown")
}

func TestDocument_FullAudioDurationIncludesMilliseconds(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1}}
	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
		Title:    "Precision",
		Language: "en",
		FullAudio: &scriptpkg.DocumentAudioRef{
			AssetID: "final-audio-en", Language: "en",
			DriveLink:  "https://drive.google.com/file/d/final-audio-en/view",
			DurationMS: 134_832,
		},
	})

	human := humanDocumentHTML(t, out)
	require.Contains(t, human, "<strong>Duration:</strong> 02:14.832")
	require.NotContains(t, human, "<strong>Duration:</strong> 02:14</p>")
}

func TestDocument_FinalAudioDriveLinkIsPureURL(t *testing.T) {
	t.Parallel()

	const pure = "https://drive.google.com/file/d/final-audio-it/view?usp=drivesdk"
	finalAudio := &scriptgeneration.FinalAudioReference{
		AssetID:      "final-audio-it",
		Path:         "/tmp/final_audio_it.m4a",
		DriveLink:    "[" + pure + "](" + pure + ")",
		DurationMS:   134_832,
		FinalMix:     true,
		CopyEligible: true,
	}
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1}}

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
		Title: "Final audio", Language: "it", FinalAudio: finalAudio,
	})

	const marker = "<h2>Final Audio JSON</h2><pre><code>"
	pos := strings.Index(out, marker)
	require.NotEqual(t, -1, pos)
	pos += len(marker)
	end := strings.Index(out[pos:], "</code></pre>")
	require.NotEqual(t, -1, end)

	var block map[string]any
	require.NoError(t, json.Unmarshal([]byte(html.UnescapeString(out[pos:pos+end])), &block))
	require.Equal(t, pure, block["drive_link"])
	require.NotContains(t, block["drive_link"].(string), "](https")
}

func TestDocument_ProjectsFinalAudioCertification(t *testing.T) {
	t.Parallel()

	finalAudio := &scriptgeneration.FinalAudioReference{
		AssetID:          "final-audio-it",
		Path:             "/tmp/final_audio_it.m4a",
		DriveLink:        "https://drive.google.com/file/d/final-audio-it/view",
		Container:        "m4a",
		PlanSHA256:       "plan-sha",
		FinalAudioSHA256: "final-sha",
		Codec:            "aac",
		Profile:          "LC",
		SampleRate:       48000,
		Channels:         2,
		ChannelLayout:    "stereo",
		DurationUS:       45_000_000,
		DurationMS:       45000,
		FinalMix:         true,
		CopyEligible:     true,
	}
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1}}

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
		Title: "Final audio", Language: "it", FinalAudio: finalAudio,
	})

	require.Contains(t, out, "<h2>Final Audio JSON</h2>")
	require.NotContains(t, out, "/tmp/final_audio_it.m4a")

	const marker = "<h2>Final Audio JSON</h2><pre><code>"
	pos := strings.Index(out, marker)
	require.NotEqual(t, -1, pos)
	pos += len(marker)
	end := strings.Index(out[pos:], "</code></pre>")
	require.NotEqual(t, -1, end)

	var block map[string]any
	require.NoError(t, json.Unmarshal([]byte(html.UnescapeString(out[pos:pos+end])), &block))

	require.Equal(t, "final-audio-it", block["audio_asset_id"])
	require.Equal(t, "it", block["language"])
	require.Equal(t, "m4a", block["container"])
	require.Equal(t, "aac", block["codec"])
	require.Equal(t, "LC", block["profile"])
	require.Equal(t, float64(48000), block["sample_rate"])
	require.Equal(t, float64(2), block["channels"])
	require.Equal(t, "stereo", block["channel_layout"])
	require.Equal(t, float64(45_000_000), block["duration_us"])
	require.Equal(t, "plan-sha", block["audio_plan_sha256"])
	require.Equal(t, "final-sha", block["final_audio_sha256"])
	require.Equal(t, true, block["final_mix"])
	require.Equal(t, true, block["copy_eligible"])
	require.NotContains(t, block, "path")
}

func TestDocument_ProjectsRenderedOverlayPublishedRef(t *testing.T) {
	t.Parallel()

	overlay := &scriptpkg.DocumentOverlayRef{
		ArtifactID:   "art-1",
		JobID:        "render-123",
		URL:          "https://store.example/overlay/art-1/overlay.mp4",
		SHA256:       "abc123",
		DurationUS:   18_200_000,
		ProfileID:    "velox-copy-v1",
		CopyEligible: true,
	}
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1}}

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
		Title: "Overlay doc", Overlay: overlay,
	})

	human := humanDocumentHTML(t, out)
	require.Contains(t, human, "<h2>Rendered Overlay</h2>")
	require.Contains(t, human, "https://store.example/overlay/art-1/overlay.mp4")
	require.Contains(t, human, "render-123")
	require.Contains(t, human, "velox-copy-v1")
	require.Contains(t, human, "00:18.200")

	require.Contains(t, out, "<h2>Rendered Overlay JSON</h2>")
	const marker = "<h2>Rendered Overlay JSON</h2><pre><code>"
	pos := strings.Index(out, marker)
	require.NotEqual(t, -1, pos)
	pos += len(marker)
	end := strings.Index(out[pos:], "</code></pre>")
	require.NotEqual(t, -1, end)

	var block scriptpkg.DocumentOverlayRef
	require.NoError(t, json.Unmarshal([]byte(html.UnescapeString(out[pos:pos+end])), &block))
	require.Equal(t, *overlay, block)

	// The overlay projection must never leak a local path or storage key.
	require.NotContains(t, out, "local_path")
	require.NotContains(t, out, "storage_key")
}

func TestDocument_OmitsOverlayWithoutReference(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1}}
	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{Title: "No overlay"})

	require.NotContains(t, out, "Rendered Overlay")
}

func TestDocument_OmitsFinalAudioJSONWithoutReference(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1}}
	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{Title: "No final audio"})

	require.NotContains(t, out, "Final Audio JSON")
}

func TestDocument_OmitsSceneTimingWithoutCanonicalTimeline(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  []scriptpkg.SpecScene{{ID: "scene-0", Index: 0, Text: "Senza timing.", Kind: scriptpkg.SceneNarration}},
	}}
	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{Title: "No timeline"})
	human := humanDocumentHTML(t, out)

	require.NotContains(t, human, "<strong>Start:</strong>")
	require.NotContains(t, human, "<strong>End:</strong>")
}

func TestBuildSpecSceneDocumentHTML_NilModelReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := mustRender(t, nil, scriptgeneration.DocumentRenderOptions{Title: "ignored"}); got != "" {
		t.Fatalf("expected empty output for nil model, got %q", got)
	}
}
