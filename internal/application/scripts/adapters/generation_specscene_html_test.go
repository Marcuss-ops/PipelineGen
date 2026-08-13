package adapters_test

import (
	"encoding/json"
	"html"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
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
	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{
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
	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{
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

	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{
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

	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{Title: "Famous people"})

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

	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{
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

func TestDocument_OmitsSceneTimingWithoutCanonicalTimeline(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  []scriptpkg.SpecScene{{ID: "scene-0", Index: 0, Text: "Senza timing.", Kind: scriptpkg.SceneNarration}},
	}}
	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{Title: "No timeline"})
	human := humanDocumentHTML(t, out)

	require.NotContains(t, human, "<strong>Start:</strong>")
	require.NotContains(t, human, "<strong>End:</strong>")
}

func TestBuildSpecSceneDocumentHTML_NilModelReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := adapters.BuildSpecSceneDocumentHTML(nil, adapters.SpecSceneDocumentOptions{Title: "ignored"}); got != "" {
		t.Fatalf("expected empty output for nil model, got %q", got)
	}
}

// TestBuildSpecSceneDocumentHTML_RendersAvailableDriveLinks pins that
// technical metadata stays out of the human surface while available Drive
// resources remain visible and clickable.
func TestBuildSpecSceneDocumentHTML_RendersAvailableDriveLinks(t *testing.T) {
	t.Parallel()

	const maliciousDriveLink = `https://drive.google.com/file/d/stock-1/view?a=1&b=2<script>alert("x")</script>`
	const stockLabel = `Stock <Round 1> & "Quotes"`

	model := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Prosa che non va duplicata nel doc.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID:    "scene-stock-malicious",
					Index: 0,
					Text:  "Scena stock con Drive link malizioso.",
					Kind:  scriptpkg.SceneIntro,
					Bindings: scriptpkg.SceneBindings{
						Stock: &scriptpkg.StockBinding{
							AssetID:   "stock-asset-1",
							Name:      stockLabel,
							Source:    "stock",
							DriveLink: maliciousDriveLink,
						},
					},
				},
				{
					ID:    "scene-clip-and-stock",
					Index: 1,
					Text:  "Scena con entrambi i binding.",
					Kind:  scriptpkg.SceneClip,
					Bindings: scriptpkg.SceneBindings{
						Clip: &scriptpkg.ClipBinding{
							ClipID:    "clip-9",
							ClipTitle: "Clip bound",
							DriveLink: "https://drive.google.com/file/d/clip-9/view",
						},
						Stock: &scriptpkg.StockBinding{
							AssetID:   "stock-asset-9",
							Name:      "Stock bound too",
							DriveLink: "https://drive.google.com/file/d/stock-9/view",
						},
					},
				},
			},
		},
	}

	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{Title: "Stock HTML test"})

	human := humanDocumentHTML(t, out)
	require.Contains(t, human, "<strong>Stock:</strong>")
	require.Contains(t, human, "<strong>Clip:</strong>")
	require.Contains(t, human, "https://drive.google.com/file/d/clip-9/view")
	require.NotContains(t, human, stockLabel)
	require.Contains(t, human, html.EscapeString(maliciousDriveLink))

	// The raw malicious URL is never emitted verbatim anywhere in the HTML.
	require.NotContains(t, out, maliciousDriveLink)

	// The technical bindings are preserved in the JSON snapshot (the stock
	// drive_link value survives; json escaping of <>& is expected).
	specJSON := extractSpecSceneJSON(t, out)
	require.Contains(t, specJSON, "stock-asset-1")
	require.Contains(t, specJSON, "stock-1")
	require.Contains(t, specJSON, "https://drive.google.com/file/d/stock-9/view")
	require.Contains(t, specJSON, "clip-9")
}

// TestDocument_VoiceoverLinkIsHTMLEscaped keeps the XSS regression pin on the
// one link the human surface still renders: the voiceover URL. The raw
// malicious string must never appear; only its html.EscapeString form.
func TestDocument_VoiceoverLinkIsHTMLEscaped(t *testing.T) {
	t.Parallel()

	const maliciousLink = `https://drive.google.com/file?a=1&x="<script>`

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{{
			ID:   "scene-0",
			Text: "Scena.",
			Bindings: scriptpkg.SceneBindings{
				Voiceover: &scriptpkg.VoiceoverBinding{Links: map[string]string{"it": maliciousLink}},
			},
		}},
	}}

	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{
		Title:           "XSS",
		Language:        "it",
		DefaultLanguage: "it",
	})

	require.NotContains(t, out, maliciousLink)
	require.Contains(t, out, html.EscapeString(maliciousLink))
}

func TestBuildSpecSceneDocumentHTML_UsesOptionsTitleOnly(t *testing.T) {
	model := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		SpecScene: scriptpkg.SpecSceneOutput{
			Scenes: []scriptpkg.SpecScene{
				{
					ID:    "scene-0",
					Index: 0,
					Text:  "Testo della scena",
				},
			},
		},
	}

	html := adapters.BuildSpecSceneDocumentHTML(
		model,
		adapters.SpecSceneDocumentOptions{Title: "Titolo & Tyson"},
	)

	require.Contains(t, html, "<h1>Titolo &amp; Tyson</h1>")
	require.NotContains(t, html, "<h2>Description</h2>")
	require.NotContains(t, html, "<h2>Tags</h2>")
	require.Contains(t, html, "Testo della scena")
}

func TestBuildSpecSceneDocumentHTML_PrintsOneTitleOnly(t *testing.T) {
	html := adapters.BuildSpecSceneDocumentHTML(
		&scriptpkg.ModelScriptOutputV1{},
		adapters.SpecSceneDocumentOptions{Title: "Titolo video"},
	)

	require.Equal(t, 1, strings.Count(html, "<h1>"))
	require.Contains(t, html, "<h1>Titolo video</h1>")
}

// humanDocumentHTML isolates the human-facing section of a rendered document
// body: everything before the SpecScene JSON snapshot.
func humanDocumentHTML(t *testing.T, output string) string {
	t.Helper()

	const marker = "<h2>SpecScene JSON</h2>"
	index := strings.Index(output, marker)
	if index < 0 {
		t.Fatal("SpecScene JSON marker missing")
	}
	return output[:index]
}

// extractSpecSceneJSON isolates the embedded SpecScene JSON snapshot from a
// rendered document body and unescapes it so it can be re-parsed and compared
// byte-faithfully against the canonical wire representation.
func extractSpecSceneJSON(t *testing.T, output string) string {
	t.Helper()

	const startMarker = "<h2>SpecScene JSON</h2><pre><code>"
	start := strings.Index(output, startMarker)
	require.NotEqual(t, -1, start, "SpecScene JSON marker missing")
	start += len(startMarker)

	end := strings.Index(output[start:], "</code></pre>")
	require.NotEqual(t, -1, end, "SpecScene JSON closing marker missing")

	return html.UnescapeString(output[start : start+end])
}

// complexSpecSceneFixture builds a rich, multi-scene SpecScene covering clip,
// multi-clip, voiceover, stock, image, and entity annotations. It is used by
// the round-trip and no-mutation tests to prove the embedded JSON snapshot
// preserves the complete canonical object.
func complexSpecSceneFixture() scriptpkg.SpecSceneOutput {
	return scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{
				ID:        "scene-0",
				SegmentID: "segment-0",
				Index:     0,
				Text:      "Scena introduttiva completa.",
				Title:     "Intro",
				Kind:      scriptpkg.SceneIntro,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID:         "clip-a",
						ClipTitle:      "Clip A",
						DriveLink:      "https://drive.google.com/file/d/clip-a/view",
						SubtitleLink:   "https://drive.google.com/file/d/sub-a/view",
						SubtitleFileID: "sub-a",
						StartMs:        1000,
						EndMs:          5000,
						DurationMs:     4000,
					},
					Voiceover: &scriptpkg.VoiceoverBinding{
						Status:     "completed",
						Link:       "https://drive.google.com/file/d/voice-legacy/view",
						Links:      map[string]string{"it": "https://drive.google.com/file/d/voice-it/view", "en": "https://drive.google.com/file/d/voice-en/view"},
						LocalPath:  "/tmp/voice.mp3",
						DurationMs: 4200,
					},
					Stock: &scriptpkg.StockBinding{
						AssetID:    "stock-1",
						Name:       "Stock One",
						Source:     "stock",
						DriveLink:  "https://drive.google.com/file/d/stock-1/view",
						FolderID:   "folder-1",
						FolderLink: "https://drive.google.com/drive/folders/folder-1",
						Score:      0.5,
						StartMs:    0,
						EndMs:      5000,
						DurationMs: 5000,
					},
					Image: &scriptpkg.ImageBinding{
						ImageID:   "img-1",
						Prompt:    "intro image",
						URL:       "https://img.example.com/intro.png",
						LocalPath: "/tmp/intro.png",
						Status:    "generated",
					},
				},
				Annotations: &scriptpkg.SceneAnnotations{
					Version:  1,
					Language: "it",
					PrimaryEntities: []scriptpkg.AnnotatedEntity{
						{ID: "e1", Text: "Jackie Chan", CanonicalName: "Jackie Chan", Type: "person", Confidence: 0.99},
					},
					Status: "completed",
				},
			},
			{
				ID:    "scene-1",
				Index: 1,
				Text:  "Scena con multi-clip.",
				Kind:  scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clips: []scriptpkg.ClipBinding{
						{ClipID: "clip-b", DriveLink: "https://drive.google.com/file/d/clip-b/view"},
						{ClipID: "clip-c", DriveLink: "https://drive.google.com/file/d/clip-c/view"},
					},
				},
			},
		},
	}
}

func TestBuildSpecSceneDocumentHTML_DoesNotMutateSpecScene(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: complexSpecSceneFixture()}

	before, err := json.Marshal(model.SpecScene)
	require.NoError(t, err)

	_ = adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{Title: "No mutation"})

	after, err := json.Marshal(model.SpecScene)
	require.NoError(t, err)

	require.Equal(t, string(before), string(after), "renderer must not mutate SpecScene")
}

func TestDocument_HumanSceneLabelsAreOneBased(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "Prima scena.", Kind: scriptpkg.SceneNarration},
			{ID: "scene-1", Index: 1, Text: "Seconda scena.", Kind: scriptpkg.SceneNarration},
		},
	}}

	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{Title: "Ordinal"})
	human := humanDocumentHTML(t, out)

	require.Contains(t, human, "<h2>Scene 1</h2>")
	require.Contains(t, human, "<h2>Scene 2</h2>")
	require.NotContains(t, human, "scene-0")
	require.NotContains(t, human, "scene-1")
}

func TestDocument_UsesCanonicalSceneText(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{
		Text: "TESTO GLOBALE DA NON STAMPARE",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{ID: "scene-0", Index: 0, Text: "TESTO SCENA CORRETTO", Kind: scriptpkg.SceneNarration},
			},
		},
	}

	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{Title: "Testo"})
	human := humanDocumentHTML(t, out)

	require.Contains(t, human, "TESTO SCENA CORRETTO")
	require.NotContains(t, human, "TESTO GLOBALE DA NON STAMPARE")
}

func TestDocument_SpecSceneJSONIsComplete(t *testing.T) {
	t.Parallel()

	original := complexSpecSceneFixture()
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: original}

	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{Title: "Completo"})
	raw := extractSpecSceneJSON(t, out)

	var decoded scriptpkg.SpecSceneOutput
	require.NoError(t, json.Unmarshal([]byte(raw), &decoded))
	require.Equal(t, original, decoded, "embedded SpecScene JSON must round-trip byte-faithfully")
}

func TestDocument_SpecSceneJSONAppearsAfterAllHumanScenes(t *testing.T) {
	t.Parallel()

	sceneTexts := []string{"SCENE_TEXT_1", "SCENE_TEXT_2", "SCENE_TEXT_3"}
	scenes := make([]scriptpkg.SpecScene, 0, len(sceneTexts))
	for i, text := range sceneTexts {
		scenes = append(scenes, scriptpkg.SpecScene{ID: "s" + string(rune('0'+i)), Index: i, Text: text, Kind: scriptpkg.SceneNarration})
	}

	out := adapters.BuildSpecSceneDocumentHTML(&scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}}, adapters.SpecSceneDocumentOptions{Title: "Ordine"})

	specPos := strings.Index(out, "<h2>SpecScene JSON</h2>")
	require.GreaterOrEqual(t, specPos, 0)

	for _, text := range sceneTexts {
		scenePos := strings.Index(out, text)
		require.GreaterOrEqual(t, scenePos, 0)
		require.Greater(t, specPos, scenePos, "SpecScene JSON must appear after scene text %q", text)
	}
}

func TestDocument_TitleIsFirstVisibleElement(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{ID: "scene-0", Index: 0, Text: "Testo", Kind: scriptpkg.SceneNarration}}}}
	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{Title: "Titolo"})

	bodyStart := strings.Index(out, "<body>")
	titleStart := strings.Index(out, "<h1>")
	require.Greater(t, titleStart, bodyStart)

	// Nothing but the title heading may sit between <body> and <h1>.
	between := out[bodyStart+len("<body>") : titleStart]
	require.NotContains(t, between, "<")
}

func TestDocument_PreservesSpecSceneOrder(t *testing.T) {
	t.Parallel()

	scenes := []scriptpkg.SpecScene{
		{ID: "scene-A", Index: 0, Text: "ALPHA", Kind: scriptpkg.SceneNarration},
		{ID: "scene-B", Index: 1, Text: "BRAVO", Kind: scriptpkg.SceneNarration},
		{ID: "scene-C", Index: 2, Text: "CHARLIE", Kind: scriptpkg.SceneNarration},
	}

	out := adapters.BuildSpecSceneDocumentHTML(&scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}}, adapters.SpecSceneDocumentOptions{Title: "Ordine"})

	posAlpha := strings.Index(out, "ALPHA")
	posBravo := strings.Index(out, "BRAVO")
	posCharlie := strings.Index(out, "CHARLIE")
	specPos := strings.Index(out, "<h2>SpecScene JSON</h2>")

	require.Greater(t, posBravo, posAlpha)
	require.Greater(t, posCharlie, posBravo)
	require.Greater(t, specPos, posCharlie)
}

func TestDocument_EmptySceneTextDoesNotInventFallbackText(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{
		Text: "TESTO GLOBALE DI FALLBACK",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{ID: "scene-0", Index: 0, Text: "", Kind: scriptpkg.SceneNarration},
			},
		},
	}

	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{Title: "Vuota"})
	human := humanDocumentHTML(t, out)

	require.Contains(t, human, "<h2>Scene 1</h2>")
	require.NotContains(t, human, "TESTO GLOBALE DI FALLBACK")
}

func TestDocument_Golden_HumanSurfacePlusCompleteSpecScene(t *testing.T) {
	t.Parallel()

	original := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{
				ID:    "scene-0",
				Index: 0,
				Text:  "TESTO SCENA UNO",
				Kind:  scriptpkg.SceneIntro,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{ClipID: "CLIP-A", DriveLink: "CLIP-A-DRIVE"},
					Voiceover: &scriptpkg.VoiceoverBinding{
						Status: "completed",
						Links:  map[string]string{"it": "VOICE-IT-1"},
					},
				},
			},
			{
				ID:    "scene-1",
				Index: 1,
				Text:  "TESTO SCENA DUE",
				Kind:  scriptpkg.SceneNarration,
				Bindings: scriptpkg.SceneBindings{
					Clips: []scriptpkg.ClipBinding{
						{ClipID: "CLIP-B", DriveLink: "CLIP-B-DRIVE"},
						{ClipID: "CLIP-C", DriveLink: "CLIP-C-DRIVE"},
					},
				},
			},
		},
	}

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: original}
	out := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{
		Title:           "TITOLO TEST",
		Language:        "it",
		DefaultLanguage: "it",
	})

	human := humanDocumentHTML(t, out)
	for _, want := range []string{
		"TITOLO TEST",
		"<h2>Scene 1</h2>",
		"TESTO SCENA UNO",
		"<strong>Voiceover:</strong>",
		"VOICE-IT-1",
		"<h2>Scene 2</h2>",
		"TESTO SCENA DUE",
	} {
		require.Contains(t, human, want)
	}

	for _, forbidden := range []string{
		"Description", "Tags",
	} {
		require.NotContains(t, human, forbidden)
	}
	for _, want := range []string{
		"<strong>Clip:</strong>", "CLIP-A-DRIVE", "CLIP-B-DRIVE", "CLIP-C-DRIVE",
	} {
		require.Contains(t, human, want)
	}

	specJSON := extractSpecSceneJSON(t, out)
	for _, want := range []string{
		"scene-0", "scene-1",
		"CLIP-A", "CLIP-A-DRIVE", "CLIP-B", "CLIP-B-DRIVE", "CLIP-C", "CLIP-C-DRIVE",
		"VOICE-IT-1", "intro", "narration",
	} {
		require.Contains(t, specJSON, want)
	}

	var decoded scriptpkg.SpecSceneOutput
	require.NoError(t, json.Unmarshal([]byte(specJSON), &decoded))
	require.Equal(t, original, decoded, "golden SpecScene must round-trip byte-faithfully")
}

func TestDocument_DoesNotRepeatLegacyClipAlias(t *testing.T) {
	t.Parallel()

	const link = "https://drive.google.com/file/d/clip-once/view"
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{{
			ID:   "scene-0",
			Text: "Una clip.",
			Bindings: scriptpkg.SceneBindings{
				Clips: []scriptpkg.ClipBinding{{ClipID: "clip-once", DriveLink: link}},
				Clip:  &scriptpkg.ClipBinding{ClipID: "clip-once", DriveLink: link},
			},
		}},
	}}

	human := humanDocumentHTML(t, adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{}))
	require.Equal(t, 1, strings.Count(human, `href="`+link+`">`), "legacy Clip alias must not duplicate the same Drive link")
}
