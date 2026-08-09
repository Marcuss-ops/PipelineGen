// Package scripts: generation_specscene_html.go renders the canonical
// Google Doc body for generated scripts.
//
// The document surface combines caller-facing video metadata with one
// structured SpecScene JSON representation. Technical provenance remains
// excluded from the generated document.
package adapters

import (
	"encoding/json"
	"html"
	"sort"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// BuildSpecSceneDocumentHTML renders the canonical production Google Doc.
//
// Visible sections include the optional metadata title, description, and tags,
// followed by the operational scene view and the complete SpecScene JSON
// snapshot. The JSON block is the canonical machine-consumable surface and
// must remain byte-faithful to model.SpecScene.
func BuildSpecSceneDocumentHTML(
	model *scriptpkg.ModelScriptOutputV1,
	title string,
	metadata *scriptpkg.VideoMetadata,
	provenance ...*scriptpkg.GenerationProvenance,
) string {
	if model == nil {
		return ""
	}
	// Provenance remains an optional pipeline detail and is not rendered into
	// the document body.
	_ = provenance

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")

	documentTitle := strings.TrimSpace(title)

	if metadata != nil && strings.TrimSpace(metadata.Title) != "" {
		documentTitle = strings.TrimSpace(metadata.Title)
	}

	if documentTitle != "" {
		b.WriteString("<h1>")
		b.WriteString(html.EscapeString(documentTitle))
		b.WriteString("</h1>")
	}

	if metadata != nil {
		if description := strings.TrimSpace(metadata.Description); description != "" {
			b.WriteString("<h2>Description</h2>")
			b.WriteString("<p>")
			b.WriteString(html.EscapeString(description))
			b.WriteString("</p>")
		}

		cleanTags := make([]string, 0, len(metadata.Tags))
		for _, raw := range metadata.Tags {
			tag := strings.TrimSpace(raw)
			if tag != "" {
				cleanTags = append(cleanTags, tag)
			}
		}

		if len(cleanTags) > 0 {
			b.WriteString("<h2>Tags</h2>")
			b.WriteString("<p>")
			b.WriteString(html.EscapeString(strings.Join(cleanTags, ", ")))
			b.WriteString("</p>")
		}
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
			if stock := scene.Bindings.Stock; stock != nil && strings.TrimSpace(stock.FolderLink) != "" {
				b.WriteString("<p><strong>Stock folder:</strong> ")
				b.WriteString(renderDocumentLink(stock.FolderLink, stock.FolderID, stock.FolderID))
				b.WriteString("</p>")
			}
			if voiceover := scene.Bindings.Voiceover; voiceover != nil && len(voiceover.Links) > 0 {
				b.WriteString("<p><strong>Voiceovers:</strong> ")
				languages := make([]string, 0, len(voiceover.Links))
				for language := range voiceover.Links {
					languages = append(languages, language)
				}
				sort.Strings(languages)
				for i, language := range languages {
					if i > 0 {
						b.WriteString(" · ")
					}
					b.WriteString(renderDocumentLink(voiceover.Links[language], language, language))
				}
				b.WriteString("</p>")
			}
			if scene.Annotations != nil {
				for _, entity := range scene.Annotations.PrimaryEntities {
					if entity.Image == nil || strings.TrimSpace(entity.Image.DriveLink) == "" {
						continue
					}
					name := documentEntityName(entity)
					if name == "" {
						continue
					}
					b.WriteString("<p><strong>Entità:</strong> ")
					b.WriteString(html.EscapeString(name))
					b.WriteString("</p><p><strong>Image link Drive:</strong> ")
					b.WriteString(renderDocumentLink(entity.Image.DriveLink, entity.Image.DriveLink, entity.Image.DriveLink))
					b.WriteString("</p>")
				}
			}
			b.WriteString("</section>")
		}
	}

	raw, err := json.MarshalIndent(model.SpecScene, "", "  ")
	if err == nil {
		b.WriteString("<h2>SpecScene JSON</h2><pre><code>")
		b.WriteString(html.EscapeString(string(raw)))
		b.WriteString("</code></pre>")
	}

	b.WriteString("</body></html>")
	return b.String()
}

func documentEntityName(entity scriptpkg.AnnotatedEntity) string {
	name := strings.TrimSpace(entity.CanonicalName)
	if name == "" {
		name = strings.TrimSpace(entity.Text)
	}
	name = strings.TrimSpace(strings.TrimPrefix(name, "Describe "))
	return name
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
