package scriptgeneration_test

import (
	"encoding/json"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
	"html"
	"strings"
	"testing"
)

func TestDocument_FullAudioAndCanonicalTimelineAreProjected(t *testing.T) {
	t.Parallel()
	timeline := &capabilityaudio.CanonicalTimeline{
		Version:    capabilityaudio.TimelineVersion,
		DurationUS: 125000000,
		Segments: []capabilityaudio.TimelineSegment{{
			ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: 125000000,
			Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover, VoiceoverAssetID: "vo-en-0"},
		}},
	}
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  []scriptpkg.SpecScene{{ID: "scene-0", Text: "AUDIO SCENE"}},
	}}
	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
		Title: "Audio contract", Language: "en", DefaultLanguage: "en",
		FullAudio: &scriptpkg.DocumentAudioRef{
			AssetID: "final-audio-en", Language: "en",
			DriveLink:  "https://drive.google.com/file/d/final-audio-en/view",
			DurationMS: 125000,
		},
		AudioTimeline: timeline,
	})
	human := humanDocumentHTML(t, out)
	require.Contains(t, human, "<h2>Full Audio</h2>")
	require.Contains(t, human, "<strong>Lang:</strong> English")
	require.Contains(t, human, "https://drive.google.com/file/d/final-audio-en/view")
	require.Contains(t, human, "<strong>Duration:</strong> 02:05")
	require.NotContains(t, human, "local_path")

	const marker = "<h2>Audio Timeline JSON</h2><pre><code>"
	pos := strings.Index(out, marker)
	require.NotEqual(t, -1, pos)
	pos += len(marker)
	end := strings.Index(out[pos:], "</code></pre>")
	require.NotEqual(t, -1, end)
	var decoded capabilityaudio.CanonicalTimeline
	require.NoError(t, json.Unmarshal([]byte(html.UnescapeString(out[pos:pos+end])), &decoded))
	require.Equal(t, *timeline, decoded)
}

func TestDocument_FullAudioIsOmittedWithoutCanonicalDriveLink(t *testing.T) {
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1}}
	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
		Language:  "en",
		FullAudio: &scriptpkg.DocumentAudioRef{AssetID: "final-audio-en", Language: "en", DurationMS: 1000},
	})
	require.NotContains(t, out, "Full Audio")
	require.NotContains(t, out, "local_path")
}

func TestBuildSpecSceneDocumentHTML_RendersHumanScenesAndDriveLinks(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "This prose must not be duplicated in the document.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID:    "scene-clip-1",
					Index: 0,
					Text:  "Canonical scene text.",
					Kind:  scriptpkg.SceneNarration,
					Bindings: scriptpkg.SceneBindings{
						Clip: &scriptpkg.ClipBinding{
							ClipID:         "clip-1",
							ClipTitle:      "Opening exchange",
							DriveLink:      "https://drive.google.com/file/d/clip-1/view",
							SubtitleFileID: "subtitle-1",
							SubtitleLink:   "https://drive.google.com/file/d/subtitle-1/view",
						},
						Voiceover: &scriptpkg.VoiceoverBinding{
							Status: "completed",
							Links:  map[string]string{"it": "https://drive.google.com/file/d/voice-1/view"},
						},
					},
				},
			},
		},
	}

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
		Title:           "Canonical Script",
		Language:        "it",
		DefaultLanguage: "it",
	})

	human := humanDocumentHTML(t, out)
	for _, want := range []string{
		"<h1>Canonical Script</h1>",
		"<h2>Scene 1</h2>",
		"Canonical scene text.",
		"<strong>Voiceover:</strong>",
		"https://drive.google.com/file/d/voice-1/view",
		"<strong>Clip:</strong>",
		"https://drive.google.com/file/d/clip-1/view",
		"<strong>Subtitles:</strong>",
		"https://drive.google.com/file/d/subtitle-1/view",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("expected human document section to contain %q; human=%s", want, human)
		}
	}

	for _, unwanted := range []string{
		"<h2>Scenes</h2>",
		"scene-clip-1",
		"This prose must not be duplicated in the document.",
	} {
		if strings.Contains(human, unwanted) {
			t.Errorf("human document section must not contain %q; human=%s", unwanted, human)
		}
	}

	// Technical bindings still live inside the SpecScene JSON snapshot.
	specJSON := extractSpecSceneJSON(t, out)
	for _, want := range []string{
		"scene-clip-1",
		"clip-1",
		"https://drive.google.com/file/d/clip-1/view",
		"https://drive.google.com/file/d/subtitle-1/view",
	} {
		if !strings.Contains(specJSON, want) {
			t.Errorf("SpecScene JSON snapshot must contain %q; JSON=%s", want, specJSON)
		}
	}
}

func TestBuildSpecSceneDocumentHTML_RendersEntityDriveLinks(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
		ID:   "scene-0",
		Text: "John Cena enters the arena.",
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			CanonicalName: "Describe John Cena",
			Text:          "Describe John Cena",
			Image:         &scriptpkg.EntityImageBinding{DriveLink: "https://drive.google.com/file/d/cena/view"},
		}}},
	}}}}

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{Title: "Famous people"})

	human := humanDocumentHTML(t, out)
	require.Contains(t, human, "<strong>Entity image:</strong>")
	require.Contains(t, human, "https://drive.google.com/file/d/cena/view")
	require.Contains(t, human, "John Cena enters the arena.")

	specJSON := extractSpecSceneJSON(t, out)
	require.Contains(t, specJSON, "primary_entities")
	require.Contains(t, specJSON, "drive_link")
	require.Contains(t, specJSON, "https://drive.google.com/file/d/cena/view")
	require.Contains(t, specJSON, "Describe John Cena")
}

func TestDocument_EntityImageRenderedInline(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
		ID:   "scene-0",
		Text: "Dwayne Johnson enters the arena.",
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			CanonicalName: "Dwayne Johnson",
			Text:          "Dwayne Johnson",
			Image: &scriptpkg.EntityImageBinding{
				Status:     "resolved",
				DriveLink:  "https://drive.google.com/file/d/dwayne/view",
				PreviewURL: "https://images.example/dwayne.jpg",
			},
		}}},
	}}}}

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{Title: "People"})

	human := humanDocumentHTML(t, out)
	// IDEAL PASS: the entity photograph is rendered inline, not only linked.
	require.Contains(t, human, `<img src="https://images.example/dwayne.jpg"`)
	require.Contains(t, human, `alt="Dwayne Johnson"`)
	// The canonical Drive link is still present.
	require.Contains(t, human, `<strong>Entity image:</strong>`)
	require.Contains(t, human, "https://drive.google.com/file/d/dwayne/view")
}

func TestDocument_ProjectsSceneTimingFromCanonicalTimeline(t *testing.T) {
	t.Parallel()

	timeline := &capabilityaudio.CanonicalTimeline{
		Version:    capabilityaudio.TimelineVersion,
		DurationUS: 45_100_000,
		Segments: []capabilityaudio.TimelineSegment{
			{ID: "scene-1", Index: 0, TimelineStartUS: 0, DurationUS: 12_430_000},
			{ID: "scene-2", Index: 1, TimelineStartUS: 12_430_000, DurationUS: 14_390_000},
			{ID: "scene-3", Index: 2, TimelineStartUS: 26_820_000, DurationUS: 18_280_000},
		},
	}
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-1", Index: 0, Text: "Scena uno.", Kind: scriptpkg.SceneNarration},
			{ID: "scene-2", Index: 1, Text: "Scena due.", Kind: scriptpkg.SceneNarration},
			{ID: "scene-3", Index: 2, Text: "Scena tre.", Kind: scriptpkg.SceneNarration},
		},
	}}

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
		Title: "Timing", AudioTimeline: timeline,
	})
	human := humanDocumentHTML(t, out)

	for _, want := range []string{
		"<strong>Start:</strong> 00:00.000",
		"<strong>End:</strong> 00:12.430",
		"<strong>Start:</strong> 00:12.430",
		"<strong>End:</strong> 00:26.820",
		"<strong>Start:</strong> 00:26.820",
		"<strong>End:</strong> 00:45.100",
	} {
		require.Contains(t, human, want)
	}

	// The end timestamp is a derived projection, never a stored SSOT field.
	require.NotContains(t, extractSpecSceneJSON(t, out), "end_us")
}

func TestDocument_ProjectsClipAndVoiceoverDurationsSeparately(t *testing.T) {
	t.Parallel()

	timeline := &capabilityaudio.CanonicalTimeline{
		Version:    capabilityaudio.TimelineVersion,
		DurationUS: 30_000_000,
		Segments: []capabilityaudio.TimelineSegment{{
			ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: 30_000_000,
			Video: capabilityaudio.VideoSegment{
				AssetID: "clip-123", SourceInUS: 5_000_000,
				SourceDurationUS: 12_000_000, TimelineDurationUS: 12_000_000,
			},
			AudioIntents: []capabilityaudio.AudioIntent{{
				Mode: capabilityaudio.AudioVoiceover, VoiceoverAssetID: "vo-123",
				SourceDurationUS: 30_000_000, TimelineDurationUS: 30_000_000,
			}},
		}},
	}
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{{ID: "scene-0", Index: 0, Text: "Clip and voiceover.", Bindings: scriptpkg.SceneBindings{
			Clip: &scriptpkg.ClipBinding{ClipID: "clip-123", TotalDurationMs: 18_420},
		}}},
	}}

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
		AudioTimeline: timeline,
		ClipMetadata:  []capabilityaudio.ClipAssetMetadata{{AssetID: "clip-123", Duration: kernelasset.ProbedDuration(18_420_000)}},
		AudioSummary: capabilityaudio.DocumentAudioSummary{
			ClipCount: 1, ClipTotalUS: 18_420_000, ClipTotalKnown: true,
			VoiceoverCount: 1, VoiceoverTotalUS: 30_000_000,
		},
	})
	human := humanDocumentHTML(t, out)
	for _, want := range []string{
		"<strong>Duration:</strong> 00:30.000",
		"<h3>Video Clip</h3>",
		"<strong>Asset:</strong> clip-123",
		"<strong>Source In:</strong> 00:05.000",
		"<strong>Source Duration:</strong> 00:12.000",
		"<strong>Timeline Duration:</strong> 00:12.000",
		"<strong>Total Duration:</strong> 00:18.420",
		"<h3>Voiceover</h3>",
		"<strong>Asset:</strong> vo-123",
		"<strong>Source Duration:</strong> 00:30.000",
		"<strong>Timeline Duration:</strong> 00:30.000",
		"<h2>Summary</h2>",
		"<strong>Total Source Clip Duration:</strong> 00:18.420",
		"<strong>Total Edge TTS Duration:</strong> 00:30.000",
		"<strong>Canonical Timeline:</strong> 00:30.000",
	} {
		require.Contains(t, human, want)
	}
}
