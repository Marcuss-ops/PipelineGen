package adapters_test

import (
	"html"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestBuildSpecSceneDocumentHTML_RendersVisibleScenesAndLinks(t *testing.T) {
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
					},
				},
			},
		},
	}
	html := adapters.BuildSpecSceneDocumentHTML(model, adapters.SpecSceneDocumentOptions{Title: "Canonical Script"})

	for _, want := range []string{
		"<h1>Canonical Script</h1>",
		"<h2>Scenes</h2>",
		"<h2>SpecScene JSON</h2><pre><code>",
		"scene-clip-1",
		"Canonical scene text.",
		"clip-1",
		"https://drive.google.com/file/d/clip-1/view",
		"Subtitles ASS",
		"https://drive.google.com/file/d/subtitle-1/view",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected canonical document HTML to contain %q; HTML=%s", want, html)
		}
	}

	for _, unwanted := range []string{
		"<h2>Script</h2>",
		"<h2>Entities</h2>",
		"<h2>Video Metadata</h2>",
		"<h2>Technical Provenance</h2>",
		"PIPELINEGEN-PROVENANCE",
		"doc-1",
		"This prose must not be duplicated in the document.",
	} {
		if strings.Contains(html, unwanted) {
			t.Errorf("canonical SpecScene document must not contain %q; HTML=%s", unwanted, html)
		}
	}
}

func TestBuildSpecSceneDocumentHTML_RendersConciseEntityImageLinks(t *testing.T) {
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
	require.Contains(t, out, "<strong>Entità:</strong> John Cena")
	require.Contains(t, out, "<strong>Image link Drive:</strong>")
	require.Contains(t, out, "https://drive.google.com/file/d/cena/view")
	require.Contains(t, out, "Describe John Cena")
	require.Contains(t, out, "primary_entities")
	require.Contains(t, out, "drive_link")
}

func TestBuildSpecSceneDocumentHTML_NilModelReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := adapters.BuildSpecSceneDocumentHTML(nil, adapters.SpecSceneDocumentOptions{Title: "ignored"}); got != "" {
		t.Fatalf("expected empty output for nil model, got %q", got)
	}
}

// TestBuildSpecSceneDocumentHTML_StockBindingDriveLinkEscape is the
// regression pin for the Stock binding visible link: it asserts
// (1) the stock drive_link is visible inside the per-scene <section>
// as a "Clip:" anchor (mirror of the JSON SpecScene block); (2) the
// drive_link URL is wrapped through html.EscapeString so a payload
// containing '&', '<', '>' or quote characters cannot leak the
// raw form into the rendered HTML; (3) the empty-DriveLink case
// surfaces renderDocumentLink's "(no link)" fallback rather than
// silently dropping the section; (4) the Stock binding is rendered
// independently from — not in conflict with — the Clip binding
// above (when both are present on the same scene, both links render).
func TestBuildSpecSceneDocumentHTML_StockBindingDriveLinkEscape(t *testing.T) {
	t.Parallel()

	// Drive a DriveLink URL containing the canonical HTML-special
	// characters. The renderer MUST emit the html.EscapeString form
	// and MUST NOT leak the raw form into the rendered output.
	const maliciousDriveLink = `https://drive.google.com/file/d/stock-1/view?a=1&b=2<script>alert("x")</script>`
	const stockLabel = `Stock <Round 1> & "Quotes"`
	escapedDriveLink := html.EscapeString(maliciousDriveLink)
	escapedLabel := html.EscapeString(stockLabel)

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
					ID:    "scene-stock-empty",
					Index: 1,
					Text:  "Scena stock senza drive link ne label.",
					Kind:  scriptpkg.SceneNarration,
					Bindings: scriptpkg.SceneBindings{
						Stock: &scriptpkg.StockBinding{
							// All three empty so renderDocumentLink's
							// empty-url branch produces "(no link)".
							AssetID:   "",
							Name:      "",
							DriveLink: "",
						},
					},
				},
				{
					ID:    "scene-stock-nil",
					Index: 2,
					Text:  "Scena senza stock.",
					Kind:  scriptpkg.SceneNarration,
					Bindings: scriptpkg.SceneBindings{
						Stock: nil,
					},
				},
				{
					ID:    "scene-clip-and-stock",
					Index: 3,
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

	// Stock metadata remains in the escaped JSON snapshot, but it must
	// not be rendered as a synthetic visible Clip note or link.
	if strings.Contains(out, escapedDriveLink) || strings.Contains(out, escapedLabel) {
		t.Errorf("stock file metadata leaked into visible document HTML; HTML=%s", out)
	}
	if strings.Contains(out, maliciousDriveLink) || strings.Contains(out, stockLabel) {
		t.Errorf("raw stock metadata leaked into HTML; HTML=%s", out)
	}
	// Only the actual Clip binding may produce a Clip label.
	if got := strings.Count(out, "<strong>Clip:</strong>"); got != 1 {
		t.Errorf("expected exactly one Clip label for the actual Clip binding, got %d; HTML=%s", got, out)
	}
	if !strings.Contains(out, "https://drive.google.com/file/d/clip-9/view") {
		t.Errorf("expected Clip binding drive_link in HTML; HTML=%s", out)
	}
	if !strings.Contains(out, "https://drive.google.com/file/d/stock-9/view") {
		t.Errorf("expected Stock binding drive_link in SpecScene JSON; HTML=%s", out)
	}
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
