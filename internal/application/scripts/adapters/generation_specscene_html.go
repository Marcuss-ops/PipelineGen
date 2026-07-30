// Package scripts: generation_specscene_html.go renders the canonical
// Google Doc body for generated scripts.
//
// The document surface is exactly one structured representation: SpecScene
// JSON. Titles, prose, duplicate scene listings, and technical provenance are
// intentionally excluded from the generated document.
package adapters

import (
	"encoding/json"
	"html"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// BuildSpecSceneDocumentHTML renders the canonical production Google Doc.
//
// Visible section:
//
//	<h2>SpecScene JSON</h2><pre>...</pre>
func BuildSpecSceneDocumentHTML(
	model *scriptpkg.ModelScriptOutputV1,
	title string,
	provenance ...*scriptpkg.GenerationProvenance,
) string {
	if model == nil {
		return ""
	}
	// Keep the narrow compatibility signature for existing callers. These
	// values are pipeline metadata, not document content.
	_ = title
	_ = provenance

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")
	if strings.TrimSpace(title) != "" {
		b.WriteString("<h1>")
		b.WriteString(html.EscapeString(strings.TrimSpace(title)))
		b.WriteString("</h1>")
	}

	// Render the operational scene view before the JSON snapshot. Google
	// Docs imports these anchors as clickable links, so operators can open
	// the exact clip and its associated ASS subtitle directly from the doc.
	if len(model.SpecScene.Scenes) > 0 {
		b.WriteString("<h2>Scenes</h2>")
		for i := range model.SpecScene.Scenes {
			scene := &model.SpecScene.Scenes[i]
			b.WriteString("<section>")
			b.WriteString("<h3>")
			b.WriteString(html.EscapeString(scene.ID))
			b.WriteString("</h3>")
			if text := strings.TrimSpace(scene.Text); text != "" {
				b.WriteString("<p>")
				b.WriteString(html.EscapeString(text))
				b.WriteString("</p>")
			}
			if clip := scene.Bindings.Clip; clip != nil {
				b.WriteString("<p><strong>Clip:</strong> ")
				b.WriteString(renderDocumentLink(clip.DriveLink, clip.ClipTitle, clip.ClipID))
				b.WriteString("</p>")
				if clip.SubtitleLink != "" {
					b.WriteString("<p><strong>Subtitles ASS:</strong> ")
					b.WriteString(renderDocumentLink(clip.SubtitleLink, clip.SubtitleFileID, clip.SubtitleFileID))
					b.WriteString("</p>")
				}
			}
			// Render the Stock binding drive_link as a visible "Clip" link
			// so the scene-N section mirrors what the JSON SpecScene
			// block already carries: stock bindings were previously only
			// visible inside the JSON snapshot, never in the per-scene
			// <section>. renderDocumentLink escapes both url and label,
			// so the bound drive_link is html-escape-safe even when it
			// carries '&', '<', '>' or quote characters; the visible label
			// uses stock.Name (human-readable) and the fallback is
			// stock.AssetID so we still emit a visible anchor when Name is
			// empty. Independent from the Clip binding above: when both
			// bindings are present on the same scene, both links render
			// side-by-side in the same <section>.
			if stock := scene.Bindings.Stock; stock != nil {
				b.WriteString("<p><strong>Clip:</strong> ")
				b.WriteString(renderDocumentLink(stock.DriveLink, stock.Name, stock.AssetID))
				b.WriteString("</p>")
			}
			b.WriteString("</section>")
		}
	}

	raw, err := json.MarshalIndent(model.SpecScene, "", "  ")
	if err == nil {
		b.WriteString("<h2>SpecScene JSON</h2><pre>")
		b.WriteString(html.EscapeString(string(raw)))
		b.WriteString("</pre>")
	}

	b.WriteString("</body></html>")
	return b.String()
}

func renderDocumentLink(url, label, fallback string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		if fallback == "" {
			return "(no link)"
		}
		return html.EscapeString(fallback)
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = url
	}
	return "<a href=\"" + html.EscapeString(url) + "\">" + html.EscapeString(label) + "</a>"
}
