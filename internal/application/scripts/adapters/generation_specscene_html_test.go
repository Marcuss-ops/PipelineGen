package adapters_test

import (
	"html"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestBuildSpecSceneDocumentHTML_RendersHumanScenesAndVoiceoverOnly(t *testing.T) {
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
	} {
		if !strings.Contains(human, want) {
			t.Errorf("expected human document section to contain %q; human=%s", want, human)
		}
	}

	for _, unwanted := range []string{
		"<h2>Scenes</h2>",
		"scene-clip-1",
		"<strong>Clip:</strong>",
		"Subtitles ASS",
		"https://drive.google.com/file/d/clip-1/view",
		"https://drive.google.com/file/d/subtitle-1/view",
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

func TestBuildSpecSceneDocumentHTML_EntityLinksOnlyInSpecSceneJSON(t *testing.T) {
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
	require.NotContains(t, human, "<strong>Entità:</strong>")
	require.NotContains(t, human, "<strong>Image link Drive:</strong>")
	require.NotContains(t, human, "Describe John Cena")
	require.NotContains(t, human, "https://drive.google.com/file/d/cena/view")
	require.Contains(t, human, "John Cena enters the arena.")

	specJSON := extractSpecSceneJSON(t, out)
	require.Contains(t, specJSON, "primary_entities")
	require.Contains(t, specJSON, "drive_link")
	require.Contains(t, specJSON, "https://drive.google.com/file/d/cena/view")
	require.Contains(t, specJSON, "Describe John Cena")
}

func TestBuildSpecSceneDocumentHTML_NilModelReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := adapters.BuildSpecSceneDocumentHTML(nil, adapters.SpecSceneDocumentOptions{Title: "ignored"}); got != "" {
		t.Fatalf("expected empty output for nil model, got %q", got)
	}
}

// TestBuildSpecSceneDocumentHTML_TechnicalBindingsStayOutOfHumanSection pins
// that clip, stock, and other technical bindings never leak into the human
// document surface — they only live inside the SpecScene JSON snapshot.
func TestBuildSpecSceneDocumentHTML_TechnicalBindingsStayOutOfHumanSection(t *testing.T) {
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
	if strings.Contains(human, "<strong>Clip:</strong>") {
		t.Errorf("clip binding leaked into the human document section; human=%s", human)
	}
	if strings.Contains(human, maliciousDriveLink) || strings.Contains(human, stockLabel) {
		t.Errorf("stock metadata leaked into the human document section; human=%s", human)
	}
	if strings.Contains(human, "https://drive.google.com/file/d/clip-9/view") {
		t.Errorf("clip drive_link leaked into the human document section; human=%s", human)
	}

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
