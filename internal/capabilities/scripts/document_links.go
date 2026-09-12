// Package scriptgeneration — document_links.go: the voiceover/scene link
// rendering half of the canonical document renderer.
//
// Extracted from document_html.go (2026-09-12) to keep each renderer half
// under max_lines_per_file_strict=600 (godlike/08 forward-prevention cap):
// document_html.go owns the skeleton + late-bound injection and the JSON
// blocks; this file owns the human-facing Drive/voiceover link surface and
// its binding resolution helpers.
package scriptgeneration

import (
	"html"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// writeDocumentSceneLinks renders only usable external links. Labels are
// human-facing, while IDs, paths, durations and statuses stay in the JSON.
func writeDocumentSceneLinks(b *strings.Builder, scene *scriptpkg.SpecScene, opts DocumentRenderOptions) {
	if scene == nil {
		return
	}

	seen := make(map[string]struct{})
	write := func(label, link string) {
		link = strings.TrimSpace(link)
		if link == "" {
			return
		}
		key := label + "\x00" + link
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		b.WriteString("<p><strong>")
		b.WriteString(html.EscapeString(label))
		b.WriteString(":</strong> ")
		b.WriteString(renderDocumentLink(link, link, link))
		b.WriteString("</p>")
	}

	// Clip is a legacy alias for the first Clips entry. Render each resource
	// once even when both compatibility fields are populated.
	for _, clip := range scene.Bindings.Clips {
		write("Clip", clip.DriveLink)
		write("Subtitles", clip.SubtitleLink)
	}
	if clip := scene.Bindings.Clip; clip != nil {
		write("Clip", clip.DriveLink)
		write("Subtitles", clip.SubtitleLink)
	}

	if stock := scene.Bindings.Stock; stock != nil {
		write("Stock", stock.DriveLink)
		write("Stock folder", stock.FolderLink)
	}

	if image := scene.Bindings.Image; image != nil {
		write("Image", image.URL)
	}

	for _, media := range scene.Bindings.Media {
		label := "Media"
		if slot := strings.TrimSpace(media.Slot); slot != "" {
			label += " " + slot
		}
		write(label, media.DriveLink)
	}

	if annotations := scene.Annotations; annotations != nil {
		for _, entity := range append(append([]scriptpkg.AnnotatedEntity{}, annotations.PrimaryEntities...), annotations.SecondaryEntities...) {
			if entity.Image != nil && strings.TrimSpace(entity.Image.DriveLink) != "" {
				writeDocumentEntityImage(b, entity)
			}
		}
	}

	writeDocumentVoiceover(b, scene.Bindings.Voiceover, opts, write)
	writeDocumentTimingLinks(b, scene.Bindings.Voiceover, opts, write)
}

// writeDocumentEntityImage renders one entity read-only from the binding
// surface. The human document intentionally does not inline or preview the
// image; it shows the compact entity line and canonical Drive link. It never
// recomputes NLP or bindings — the entity-image SSOT is the projection
// produced upstream.
func writeDocumentEntityImage(b *strings.Builder, entity scriptpkg.AnnotatedEntity) {
	if entity.Image == nil {
		return
	}
	name := strings.TrimSpace(entity.CanonicalName)
	if name == "" {
		name = strings.TrimSpace(entity.Text)
	}
	drive := strings.TrimSpace(entity.Image.DriveLink)
	if name == "" || drive == "" {
		return // not_found or no usable link — never fabricate an image
	}
	writeDocumentEntityLine(b, entity)
}

func writeDocumentVoiceover(b *strings.Builder, voiceover *scriptpkg.VoiceoverBinding, opts DocumentRenderOptions, write func(string, string)) {
	link := resolveDocumentVoiceoverLink(voiceover, string(opts.Language), string(opts.DefaultLanguage))
	if link == "" {
		return
	}
	write("Voiceover", link)
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

// writeDocumentTimingLinks renders the published timing bundle links
// (timing.json SSOT + optional SRT/VTT projections) for the resolved
// language. Word-level boundaries are never inlined — they stay in the
// published timing.json the links point to.
func writeDocumentTimingLinks(b *strings.Builder, voiceover *scriptpkg.VoiceoverBinding, opts DocumentRenderOptions, write func(string, string)) {
	timing, ok := resolveDocumentTimingBinding(voiceover, string(opts.Language), string(opts.DefaultLanguage))
	if !ok {
		return
	}
	write("Timing JSON", timing.JSONLink)
	write("Timing SRT", timing.SRTLink)
	write("Timing VTT", timing.VTTLink)
}

// resolveDocumentTimingBinding picks the timing bundle binding for the
// requested language using the same resolution order as the voiceover audio
// link: canonical language-specific entry first, then the default/legacy
// surface, then a single available entry when no language was requested.
func resolveDocumentTimingBinding(voiceover *scriptpkg.VoiceoverBinding, language, defaultLanguage string) (scriptpkg.VoiceoverTimingBinding, bool) {
	if voiceover == nil || voiceover.Timing == nil {
		return scriptpkg.VoiceoverTimingBinding{}, false
	}
	language = strings.TrimSpace(language)
	defaultLanguage = strings.TrimSpace(defaultLanguage)
	if language != "" {
		if timing, ok := voiceover.Timing[language]; ok {
			return timing, true
		}
	}
	if language == "" || language == defaultLanguage {
		if timing, ok := voiceover.Timing[""]; ok {
			return timing, true
		}
	}
	if language == "" && len(voiceover.Timing) == 1 {
		for _, timing := range voiceover.Timing {
			return timing, true
		}
	}
	return scriptpkg.VoiceoverTimingBinding{}, false
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
