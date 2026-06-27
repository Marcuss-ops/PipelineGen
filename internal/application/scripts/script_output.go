package scripts

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
)

type scriptOutputEnvelope struct {
	Text      string `json:"text"`
	SpecScene struct {
		Clip struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"clip"`
	} `json:"specscene"`
	Sections []map[string]any `json:"sections,omitempty"`
}

// buildScriptOutputEnvelope normalizes any generated script into the
// standard response shape used by the API and Google Docs:
//
//	{
//	  "text": "...",
//	  "specscene": {
//	    "clip": {
//	      "id": "clip_01",
//	      "title": "..."
//	    }
//	  }
//	}
//
// The clip entry is intentionally synthetic so downstream systems have
// a stable anchor even when the generation is text-first.
func buildScriptOutputEnvelope(title, text string, sections []map[string]any) map[string]any {
	envelope := scriptOutputEnvelope{}
	envelope.Text = strings.TrimSpace(text)
	envelope.SpecScene.Clip.ID = "clip_01"
	envelope.SpecScene.Clip.Title = strings.TrimSpace(title)
	if envelope.SpecScene.Clip.Title == "" {
		envelope.SpecScene.Clip.Title = "Script"
	}
	if len(sections) > 0 {
		envelope.Sections = sections
	}

	out := map[string]any{
		"text": envelope.Text,
		"specscene": map[string]any{
			"clip": map[string]any{
				"id":    envelope.SpecScene.Clip.ID,
				"title": envelope.SpecScene.Clip.Title,
			},
		},
	}
	if len(envelope.Sections) > 0 {
		out["sections"] = envelope.Sections
	}
	return out
}

func buildClipOutputSection(sc ClipScene, fallbackID string) map[string]any {
	clipID := strings.TrimSpace(sc.ClipID)
	if clipID == "" {
		clipID = fallbackID
	}
	if clipID == "" {
		clipID = "clip_01"
	}
	section := map[string]any{
		"text": strings.TrimSpace(sc.Text),
		"specscene": map[string]any{
			"clip": map[string]any{
				"id":    clipID,
				"title": strings.TrimSpace(sc.ClipTitle),
			},
		},
	}
	if sc.DriveLink != "" {
		section["drive_link"] = sc.DriveLink
	}
	return section
}

func buildScriptOutputJSON(title, text string, sections []map[string]any) string {
	envelope := buildScriptOutputEnvelope(title, text, sections)
	raw, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return fmt.Sprintf("{\n  \"text\": %q,\n  \"specscene\": {\"clip\": {\"id\": %q, \"title\": %q}}\n}",
			strings.TrimSpace(text), "clip_01", fallbackScriptTitle(title))
	}
	return string(raw)
}

func buildStructuredScriptDocHTML(title, text string, sections []map[string]any) string {
	return appendStructuredJSONToHTML(
		"<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body><h1>"+html.EscapeString(strings.TrimSpace(title))+"</h1>",
		title,
		text,
		sections,
	)
}

func buildStructuredBatchDocHTML(title string, sectionTitles, sectionContents []string, noChapters bool, language string, sections []map[string]any) string {
	base := BuildSectionDocHTML(title, sectionTitles, sectionContents, noChapters, language)
	return appendStructuredJSONToHTML(base, title, strings.Join(sectionContents, "\n\n"), sections)
}

func buildStructuredCurateDocHTML(title string, clipScenes []ClipScene) string {
	base := BuildCurateDocContent(title, clipScenes)
	sections := make([]map[string]any, 0, len(clipScenes))
	texts := make([]string, 0, len(clipScenes))
	for i, sc := range clipScenes {
		sections = append(sections, buildClipOutputSection(sc, fmt.Sprintf("clip_%02d", i+1)))
		if txt := strings.TrimSpace(sc.Text); txt != "" {
			texts = append(texts, txt)
		}
	}
	text := strings.Join(texts, "\n\n")
	if text == "" {
		text = strings.TrimSpace(title)
	}
	return appendStructuredJSONToHTML(base, title, text, sections)
}

func appendStructuredJSONToHTML(baseHTML, title, text string, sections []map[string]any) string {
	jsonText := html.EscapeString(buildScriptOutputJSON(title, text, sections))
	var b strings.Builder
	b.WriteString(strings.TrimSuffix(baseHTML, "</body></html>"))
	if sections != nil {
		b.WriteString("<hr><h2>Structured Output</h2>")
	} else {
		b.WriteString("<hr><h2>Structured Output</h2>")
	}
	b.WriteString("<pre style=\"white-space: pre-wrap; font-family: monospace;\">")
	b.WriteString(jsonText)
	b.WriteString("</pre>")
	b.WriteString("</body></html>")
	return b.String()
}

func fallbackScriptTitle(title string) string {
	t := strings.TrimSpace(title)
	if t == "" {
		return "Script"
	}
	return t
}
