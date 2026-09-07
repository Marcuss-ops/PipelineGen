package scriptgeneration_test

import (
	"encoding/json"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
	"html"
	"strings"
	"testing"
)

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

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{Title: "Stock HTML test"})

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

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{
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

	html := mustRender(t,
		model,
		scriptgeneration.DocumentRenderOptions{Title: "Titolo & Tyson"},
	)

	require.Contains(t, html, "<h1>Titolo &amp; Tyson</h1>")
	require.NotContains(t, html, "<h2>Description</h2>")
	require.NotContains(t, html, "<h2>Tags</h2>")
	require.Contains(t, html, "Testo della scena")
}

func TestBuildSpecSceneDocumentHTML_PrintsOneTitleOnly(t *testing.T) {
	html := mustRender(t,
		&scriptpkg.ModelScriptOutputV1{},
		scriptgeneration.DocumentRenderOptions{Title: "Titolo video"},
	)

	require.Equal(t, 1, strings.Count(html, "<h1>"))
	require.Contains(t, html, "<h1>Titolo video</h1>")
}

func TestBuildSpecSceneDocumentHTML_DoesNotMutateSpecScene(t *testing.T) {
	t.Parallel()

	model := &scriptpkg.ModelScriptOutputV1{SpecScene: complexSpecSceneFixture()}

	before, err := json.Marshal(model.SpecScene)
	require.NoError(t, err)

	_ = mustRender(t, model, scriptgeneration.DocumentRenderOptions{Title: "No mutation"})

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

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{Title: "Ordinal"})
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

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{Title: "Testo"})
	human := humanDocumentHTML(t, out)

	require.Contains(t, human, "TESTO SCENA CORRETTO")
	require.NotContains(t, human, "TESTO GLOBALE DA NON STAMPARE")
}

func TestDocument_SpecSceneJSONIsComplete(t *testing.T) {
	t.Parallel()

	original := complexSpecSceneFixture()
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: original}

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{Title: "Completo"})
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

	out := mustRender(t, &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}}, scriptgeneration.DocumentRenderOptions{Title: "Ordine"})

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
	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{Title: "Titolo"})

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

	out := mustRender(t, &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}}, scriptgeneration.DocumentRenderOptions{Title: "Ordine"})

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

	out := mustRender(t, model, scriptgeneration.DocumentRenderOptions{Title: "Vuota"})
	human := humanDocumentHTML(t, out)

	require.Contains(t, human, "<h2>Scene 1</h2>")
	require.NotContains(t, human, "TESTO GLOBALE DI FALLBACK")
}
