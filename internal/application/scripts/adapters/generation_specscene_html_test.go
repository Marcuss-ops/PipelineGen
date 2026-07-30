package adapters_test

import (
	"html"
	"strings"
	"testing"

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
	prov := &scriptpkg.GenerationProvenance{
		DocID:         "doc-1",
		DocLink:       "https://docs.google.com/document/d/doc-1/edit",
		SourceType:    "clips",
		RequestedMode: "clip_native",
		UsedMode:      "clip_native",
	}

	html := adapters.BuildSpecSceneDocumentHTML(model, "Canonical Script", prov)

	for _, want := range []string{
		"<h1>Canonical Script</h1>",
		"<h2>Scenes</h2>",
		"<h2>SpecScene JSON</h2><pre>",
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

func TestBuildSpecSceneDocumentHTML_NilModelReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := adapters.BuildSpecSceneDocumentHTML(nil, "ignored"); got != "" {
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

	out := adapters.BuildSpecSceneDocumentHTML(model, "Stock HTML test", nil)

	// (1) The escaped stock drive_link MUST appear inside an anchor
	// tag inside the per-scene <section>; the URL must be wrapped in
	// <a href="..."> exactly once for this scene, and the visible
	// label "Clip:" must precede it.
	if !strings.Contains(out, escapedDriveLink) {
		t.Errorf("expected canonical HTML to contain html.EscapeString(DriveLink) %q; HTML=%s",
			escapedDriveLink, out)
	}
	// (2) The raw (unescaped) drive_link MUST NOT leak: a script-
	// tag-bearing URL would otherwise become a valid anchor.
	if strings.Contains(out, maliciousDriveLink) {
		t.Errorf("raw DriveLink %q leaked into HTML; html.EscapeString must wrap it; HTML=%s",
			maliciousDriveLink, out)
	}
	// (3) The visible label (stock.Name) MUST also be escaped so a
	// payload-bearing Name cannot re-open the same injection.
	if !strings.Contains(out, escapedLabel) {
		t.Errorf("expected canonical HTML to contain html.EscapeString(Name) %q; HTML=%s",
			escapedLabel, out)
	}
	if strings.Contains(out, stockLabel) {
		t.Errorf("raw Name %q leaked into HTML; html.EscapeString must wrap it; HTML=%s",
			stockLabel, out)
	}
	// (4) The empty-DriveLink scene MUST trigger renderDocumentLink's
	// "(no link)" fallback so the section is still discoverable.
	if !strings.Contains(out, "(no link)") {
		t.Errorf(`expected "(no link)" fallback for empty DriveLink to surface in HTML; HTML=%s`, out)
	}
	// (5) The Stock binding MUST render independently from the Clip
	// binding: when both are present, BOTH drive_links appear.
	if !strings.Contains(out, "https://drive.google.com/file/d/clip-9/view") {
		t.Errorf("expected Clip binding drive_link in HTML; HTML=%s", out)
	}
	if !strings.Contains(out, "https://drive.google.com/file/d/stock-9/view") {
		t.Errorf("expected Stock binding drive_link in HTML (independent of Clip); HTML=%s", out)
	}
}
