// Package scripts: generation_html.go renders the Google Doc HTML body
// for a generated script result. Used by DocumentProcessor.
//
// PR 3 (June 2026): BuildGenerationDocumentHTML is the canonical typed
// renderer. Replaces the pre-PR-3 BuildSectionDocHTML pattern which
// took a flat list of section titles + contents. The new shape
// renders (title, full text, scenes with clip/image/voiceover
// bindings, entities, metadata) directly from the typed inputs.
package adapters

import (
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// BuildGenerationDocumentHTML renders the Google Doc HTML body for a
// canonical typed model + aggregate entities + metadata.
//
// Sections:
//
//	<h1>title</h1>
//	<h2>Script</h2> — full-text prose (split on \n\n for paragraphs)
//	<h2>Scenes</h2> — per-scene: index, kind, text, and bindings (clip
//	  / image / voiceover) rendered as inline annotations.
//	<h2>Entities</h2> — Persons, Places, Concepts subsections
//	  (each empty list renders nothing — no spurious headers).
//	  The Raw field is shown as a <pre> block when present (legacy
//	  pre-PR-3 read-compat rows).
//	<h2>Video Metadata</h2> — per-language title, description, tags.
//
// language is used for localised chapter-label aliases (matching
// BuildSectionDocHTML's "Chapter" / "Capitolo" / "Chapitre" /
// "Capítulo" / "Kapitel" mapping). Unknown languages default to
// "Chapter".
//
// Empty inputs render a minimal HTML doc — pass an empty model to
// get an empty doc shell. nil model renders an empty string.
func BuildGenerationDocumentHTML(
	model *scriptpkg.ModelScriptOutputV1,
	title string,
	language string,
	entities *scriptpkg.EntityResult,
	metadata []scriptpkg.VideoMetadata,
) string {
	if model == nil {
		return ""
	}

	cl := chapterLabel(language)

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")

	// ── Title ─────────────────────────────────────────────────────────
	if t := strings.TrimSpace(title); t != "" {
		b.WriteString(fmt.Sprintf("<h1>%s</h1>", html.EscapeString(t)))
	}

	// ── Full text prose ───────────────────────────────────────────────
	if t := strings.TrimSpace(model.Text); t != "" {
		b.WriteString("<h2>Script</h2>")
		for _, para := range strings.Split(t, "\n\n") {
			para = strings.TrimSpace(para)
			if para == "" {
				continue
			}
			para = html.EscapeString(para)
			para = strings.ReplaceAll(para, "\n", "<br>")
			b.WriteString(fmt.Sprintf("<p>%s</p>", para))
		}
	}

	// ── Scenes ────────────────────────────────────────────────────────
	if len(model.SpecScene.Scenes) > 0 {
		b.WriteString("<h2>Scenes</h2>")
		for i := range model.SpecScene.Scenes {
			sc := &model.SpecScene.Scenes[i]
			b.WriteString(fmt.Sprintf("<section id=\"scene-%d\">", i+1))

			// Heading: <chapterLabel> N: <scene_id>
			sceneHeading := strings.TrimSpace(sc.ID)
			if sceneHeading == "" {
				sceneHeading = fmt.Sprintf("Scene %d", i+1)
			}
			b.WriteString(fmt.Sprintf("<h3>%s %d: %s</h3>",
				html.EscapeString(cl), i+1,
				html.EscapeString(sceneHeading)))

			// Metadata: index, kind
			b.WriteString("<p><em>")
			b.WriteString(fmt.Sprintf("index=%d, kind=%s",
				sc.Index, html.EscapeString(string(sc.Kind))))
			b.WriteString("</em></p>")

			// Text
			if tx := strings.TrimSpace(sc.Text); tx != "" {
				b.WriteString(fmt.Sprintf("<p>%s</p>", html.EscapeString(tx)))
			}

			// ─ Bindings ─
			if sc.Bindings.Clip != nil {
				b.WriteString("<p><strong>Clip:</strong> ")
				switch {
				case sc.Bindings.Clip.DriveLink != "" && sc.Bindings.Clip.ClipTitle != "":
					b.WriteString(fmt.Sprintf("<a href=\"%s\">%s</a>",
						html.EscapeString(sc.Bindings.Clip.DriveLink),
						html.EscapeString(sc.Bindings.Clip.ClipTitle)))
				case sc.Bindings.Clip.DriveLink != "":
					b.WriteString(fmt.Sprintf("<a href=\"%s\">%s</a>",
						html.EscapeString(sc.Bindings.Clip.DriveLink),
						html.EscapeString(sc.Bindings.Clip.DriveLink)))
				case sc.Bindings.Clip.ClipTitle != "":
					b.WriteString(html.EscapeString(sc.Bindings.Clip.ClipTitle))
				default:
					b.WriteString("(no link)")
				}
				if sc.Bindings.Clip.ClipID != "" {
					b.WriteString(fmt.Sprintf(" (id=%s)",
						html.EscapeString(sc.Bindings.Clip.ClipID)))
				}
				b.WriteString("</p>")
			}
			if sc.Bindings.Image != nil {
				b.WriteString("<p><strong>Image:</strong> ")
				if sc.Bindings.Image.URL != "" {
					b.WriteString(fmt.Sprintf("<a href=\"%s\">%s</a>",
						html.EscapeString(sc.Bindings.Image.URL),
						html.EscapeString(sc.Bindings.Image.URL)))
				} else {
					b.WriteString("(no URL)")
				}
				if sc.Bindings.Image.Status != "" {
					b.WriteString(fmt.Sprintf(" — status=%s",
						html.EscapeString(sc.Bindings.Image.Status)))
				}
				b.WriteString("</p>")
			}
			if sc.Bindings.Voiceover != nil {
				b.WriteString("<p><strong>Voiceover:</strong> ")
				if sc.Bindings.Voiceover.Link != "" {
					b.WriteString(fmt.Sprintf("<a href=\"%s\">%s</a>",
						html.EscapeString(sc.Bindings.Voiceover.Link),
						html.EscapeString(sc.Bindings.Voiceover.Link)))
				} else {
					b.WriteString("(no link)")
				}
				if sc.Bindings.Voiceover.Status != "" {
					b.WriteString(fmt.Sprintf(" — status=%s",
						html.EscapeString(sc.Bindings.Voiceover.Status)))
				}
				b.WriteString("</p>")
			}

			b.WriteString("</section>")
		}
	}

	// ── Entities ──────────────────────────────────────────────────────
	if entities != nil {
		hasAnyEntity := len(entities.Persons) > 0 ||
			len(entities.Places) > 0 ||
			len(entities.Concepts) > 0 ||
			entities.Raw != ""
		if hasAnyEntity {
			b.WriteString("<h2>Entities</h2>")
			if len(entities.Persons) > 0 {
				b.WriteString("<h3>Persons</h3><ul>")
				for _, e := range entities.Persons {
					b.WriteString(fmt.Sprintf("<li>%s</li>",
						html.EscapeString(e.Value)))
				}
				b.WriteString("</ul>")
			}
			if len(entities.Places) > 0 {
				b.WriteString("<h3>Places</h3><ul>")
				for _, e := range entities.Places {
					b.WriteString(fmt.Sprintf("<li>%s</li>",
						html.EscapeString(e.Value)))
				}
				b.WriteString("</ul>")
			}
			if len(entities.Concepts) > 0 {
				b.WriteString("<h3>Concepts</h3><ul>")
				for _, e := range entities.Concepts {
					b.WriteString(fmt.Sprintf("<li>%s</li>",
						html.EscapeString(e.Value)))
				}
				b.WriteString("</ul>")
			}
			if entities.Raw != "" {
				b.WriteString("<h3>Raw (pre-PR-3 legacy)</h3><pre>")
				b.WriteString(html.EscapeString(entities.Raw))
				b.WriteString("</pre>")
			}
		}
	}

	// ── Video metadata ────────────────────────────────────────────────
	if len(metadata) > 0 {
		b.WriteString("<h2>Video Metadata</h2>")
		for _, m := range metadata {
			b.WriteString(fmt.Sprintf("<h3>%s</h3>",
				html.EscapeString(strings.TrimSpace(m.Language))))
			if m.Title != "" {
				b.WriteString(fmt.Sprintf("<p><strong>Title:</strong> %s</p>",
					html.EscapeString(m.Title)))
			}
			if m.Description != "" {
				b.WriteString(fmt.Sprintf("<p><strong>Description:</strong> %s</p>",
					html.EscapeString(m.Description)))
			}
			if len(m.Tags) > 0 {
				b.WriteString("<p><strong>Tags:</strong> ")
				for i, tag := range m.Tags {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString(html.EscapeString(tag))
				}
				b.WriteString("</p>")
			}
		}
	}

	b.WriteString("</body></html>")
	return b.String()
}

// chapterLabel maps a BCP-47-ish language tag to the localised
// "Chapter" word. Mirrors BuildSectionDocHTML's mapping so the
// pre-PR-3 batch pipeline and the PR 3 typed renderer produce
// visually equivalent docs.
func chapterLabel(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "it":
		return "Capitolo"
	case "fr":
		return "Chapitre"
	case "es":
		return "Capítulo"
	case "de":
		return "Kapitel"
	}
	return "Chapter"
}

// BuildClipSpecSceneDocumentHTML renders the clip-aware document body
// used by generate-from-clips. It emits the compact SpecScene JSON
// structure documented by the legacy route: every scene gets a
// drive_links array derived from the resolved clip evidence.
func BuildClipSpecSceneDocumentHTML(
	model *scriptpkg.ModelScriptOutputV1,
	title string,
	evidence *scriptpkg.ClipEvidence,
) string {
	if model == nil {
		return ""
	}

	type sceneDoc struct {
		ID          string   `json:"id"`
		Index       int      `json:"index"`
		Text        string   `json:"text"`
		Kind        string   `json:"kind"`
		Description string   `json:"description,omitempty"`
		DriveLinks  []string `json:"drive_links,omitempty"`
	}
	type specDoc struct {
		Version int        `json:"version"`
		Scenes  []sceneDoc `json:"scenes"`
	}

	doc := specDoc{
		Version: model.SpecScene.Version,
		Scenes:  make([]sceneDoc, 0, len(model.SpecScene.Scenes)),
	}

	clipIDs := sortedClipIDs(evidence)
	for i := range model.SpecScene.Scenes {
		scene := model.SpecScene.Scenes[i]
		var links []string
		var desc string
		if len(clipIDs) > 0 && evidence != nil {
			clipID := clipIDs[i%len(clipIDs)]
			if link := strings.TrimSpace(evidence.DriveLinks[clipID]); link != "" {
				links = append(links, link)
			}
			if name := strings.TrimSpace(evidence.ClipNames[clipID]); name != "" {
				desc = name
			}
		}
		kind := string(scene.Kind)
		if i == 0 && (kind == "" || kind == "clip" || kind == "narration") {
			kind = "intro"
		}
		doc.Scenes = append(doc.Scenes, sceneDoc{
			ID:          scene.ID,
			Index:       scene.Index,
			Text:        scene.Text,
			Kind:        kind,
			Description: desc,
			DriveLinks:  links,
		})
	}

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")
	if t := strings.TrimSpace(title); t != "" {
		b.WriteString(fmt.Sprintf("<h1>%s</h1>", html.EscapeString(t)))
	}
	b.WriteString("<h2>SpecScene JSON</h2><pre>")
	b.WriteString(html.EscapeString(string(raw)))
	b.WriteString("</pre></body></html>")
	return b.String()
}

func sortedClipIDs(evidence *scriptpkg.ClipEvidence) []string {
	if evidence == nil || len(evidence.DriveLinks) == 0 {
		return nil
	}
	clipIDs := make([]string, 0, len(evidence.DriveLinks))
	for id := range evidence.DriveLinks {
		clipIDs = append(clipIDs, id)
	}
	sort.Strings(clipIDs)
	return clipIDs
}

// BuildSectionDocHTML renders a sectioned HTML document from a flat
// (titles, contents) list. Canonical impl lives here in adapters/ after
// PR-G mega-package split (the previous scripts/usecase/section_regen.go
// shim was hoisted into this package when the scripts/ package was
// renamed to scripts/adapters/). Returns a minimal but real HTML body
// so callers don't observe empty Google Docs in production.
//
// noChapters=true skips the <h1> chapter heading (caller controls
// whether each section title renders as h2 or h3).
func BuildSectionDocHTML(title string, sectionTitles, sectionContents []string, noChapters bool, language string) string {
	var b strings.Builder
	htmlEscape := func(s string) string {
		s = strings.ReplaceAll(s, "&", "&amp;")
		s = strings.ReplaceAll(s, "<", "&lt;")
		s = strings.ReplaceAll(s, ">", "&gt;")
		s = strings.ReplaceAll(s, "\"", "&quot;")
		return s
	}
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>")
	b.WriteString(htmlEscape(title))
	b.WriteString("</title></head><body>")
	if !noChapters {
		b.WriteString("<h1>")
		b.WriteString(htmlEscape(title))
		b.WriteString("</h1>")
	}
	if language != "" {
		b.WriteString("<p><em>")
		b.WriteString(htmlEscape(language))
		b.WriteString("</em></p>")
	}
	n := len(sectionTitles)
	if n < len(sectionContents) {
		n = len(sectionContents)
	}
	for i := 0; i < n; i++ {
		var t, c string
		if i < len(sectionTitles) {
			t = sectionTitles[i]
		}
		if i < len(sectionContents) {
			c = sectionContents[i]
		}
		if strings.TrimSpace(t) != "" {
			b.WriteString("<h2>")
			b.WriteString(htmlEscape(t))
			b.WriteString("</h2>")
		}
		if strings.TrimSpace(c) != "" {
			for _, para := range strings.Split(c, "\n\n") {
				b.WriteString("<p>")
				b.WriteString(htmlEscape(strings.TrimSpace(para)))
				b.WriteString("</p>")
			}
		}
	}
	b.WriteString("</body></html>")
	return b.String()
}
