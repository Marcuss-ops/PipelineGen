// Package scripts: generation_specscene_html.go renders the canonical
// Google Doc body for generated scripts.
//
// The document surface combines a caller-facing title with the human scene
// view (scene text + optional voiceover URL) and one structured SpecScene
// JSON representation. Technical bindings (clip, stock, subtitle, entity
// images) remain excluded from the human surface and live only inside the
// SpecScene JSON snapshot.
package adapters

import (
	"encoding/json"
	"fmt"
	"html"
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
// view ("Scene N" + scene text + optional voiceover URL) and the complete
// SpecScene JSON snapshot. The JSON block is the canonical machine-consumable
// surface and must remain byte-faithful to model.SpecScene.
func BuildSpecSceneDocumentHTML(
	model *scriptpkg.ModelScriptOutputV1,
	opts SpecSceneDocumentOptions,
) string {
	if model == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")

	if title := strings.TrimSpace(opts.Title); title != "" {
		b.WriteString("<h1>")
		b.WriteString(html.EscapeString(title))
		b.WriteString("</h1>")
	}

	for i := range model.SpecScene.Scenes {
		scene := &model.SpecScene.Scenes[i]

		b.WriteString("<section>")
		fmt.Fprintf(&b, "<h2>Scene %d</h2>", i+1)

		if text := strings.TrimSpace(scene.Text); text != "" {
			b.WriteString("<p>")
			b.WriteString(html.EscapeString(text))
			b.WriteString("</p>")
		}

		writeDocumentVoiceover(&b, scene.Bindings.Voiceover, opts)

		b.WriteString("</section>")
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

// writeDocumentVoiceover renders the scene's voiceover URL into the human
// section. It is intentionally silent when no link resolves, and it renders
// the raw URL as both the anchor target and the visible label so operators
// can read and copy it directly.
func writeDocumentVoiceover(b *strings.Builder, voiceover *scriptpkg.VoiceoverBinding, opts SpecSceneDocumentOptions) {
	link := resolveDocumentVoiceoverLink(voiceover, opts.Language, opts.DefaultLanguage)
	if link == "" {
		return
	}

	b.WriteString("<p><strong>Voiceover:</strong> ")
	b.WriteString(renderDocumentLink(link, link, link))
	b.WriteString("</p>")
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
