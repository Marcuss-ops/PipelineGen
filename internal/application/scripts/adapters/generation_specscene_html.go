// Package scripts: generation_specscene_html.go renders the canonical
// Google Doc body for generated scripts.
//
// The document surface combines a caller-facing title with the human scene
// view and one structured SpecScene JSON representation. Technical bindings
// and provenance remain excluded from the human surface and live only inside
// the SpecScene JSON snapshot.
package adapters

import (
	"encoding/json"
	"html"
	"sort"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// SpecSceneDocumentOptions carries the caller-facing inputs the document
// renderer needs. The renderer is deterministic: it renders only the title,
// the human scene surface, and the byte-faithful SpecScene JSON snapshot. It
// never mutates SpecScene and never resolves technical bindings itself.
type SpecSceneDocumentOptions struct {
	Title           string
	Language        string
	DefaultLanguage string
}

// BuildSpecSceneDocumentHTML renders the canonical production Google Doc.
//
// Visible sections include the optional title, followed by the human scene
// view and the complete SpecScene JSON snapshot. The JSON block is the
// canonical machine-consumable surface and must remain byte-faithful to
// model.SpecScene.
func BuildSpecSceneDocumentHTML(
	model *scriptpkg.ModelScriptOutputV1,
	opts SpecSceneDocumentOptions,
) string {
	if model == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")

	documentTitle := strings.TrimSpace(opts.Title)
	if documentTitle != "" {
		b.WriteString("<h1>")
		b.WriteString(html.EscapeString(documentTitle))
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

// resolveDocumentVoiceoverLink picks the single Drive link to surface in the
// human document section for a scene's voiceover binding.
//
// Resolution order:
//  1. The canonical language-specific link in Links[language].
//  2. The legacy/default-language surface (Link) only when no language is
//     requested or when it matches the job's default language.
//  3. A single available link when no language was requested and exactly one
//     link exists.
//
// It deliberately never falls back to a wrong-language link: a document built
// for language X must not show the voiceover of language Y just because it is
// the only one present.
func resolveDocumentVoiceoverLink(
	voiceover *scriptpkg.VoiceoverBinding,
	language string,
	defaultLanguage string,
) string {
	if voiceover == nil {
		return ""
	}

	language = strings.TrimSpace(language)
	defaultLanguage = strings.TrimSpace(defaultLanguage)

	// 1. Canonical language-specific link.
	if language != "" && voiceover.Links != nil {
		if link := strings.TrimSpace(voiceover.Links[language]); link != "" {
			return link
		}
	}

	// 2. Legacy/default-language compatibility.
	if language == "" || language == defaultLanguage {
		if link := strings.TrimSpace(voiceover.Link); link != "" {
			return link
		}
	}

	// 3. No language requested + exactly one available link.
	if language == "" && len(voiceover.Links) == 1 {
		for _, raw := range voiceover.Links {
			if link := strings.TrimSpace(raw); link != "" {
				return link
			}
		}
	}

	return ""
}
