package handlers

import (
	"bytes"
	"encoding/json"
	"html"
	"strings"
)

// preStyle is the inline CSS style used for <pre> blocks in generated Google Docs.
const preStyle = "background:#f5f5f5;padding:12px;border-radius:4px;font-size:13px;overflow-x:auto"

// buildGeneratedTextDocContent builds the HTML content for a Google Doc,
// including script text, entities, insights, and per-language metadata.
func buildGeneratedTextDocContent(title, script string, targetWords int, videoMetadata []VideoMetadata, entitiesJSON string, insights ScriptInsights, scenes []ScriptSceneImage) string {
	var b strings.Builder

	if strings.TrimSpace(title) != "" {
		b.WriteString("<h1>")
		b.WriteString(html.EscapeString(strings.TrimSpace(title)))
		b.WriteString("</h1>\n")
	}

	if strings.TrimSpace(script) != "" {
		b.WriteString("<h2>Script</h2>\n")
		for _, p := range strings.Split(strings.TrimSpace(script), "\n\n") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			b.WriteString("<p>")
			b.WriteString(html.EscapeString(p))
			b.WriteString("</p>\n")
		}
	}

	// ── Entities and Insights ─────────────────────────────────────────────
	if entitiesJSON != "" {
		if len(insights.ImportantWords) > 0 {
			b.WriteString("<h2>Important Words</h2>\n")
			writeJSONBlock(&b, insights.ImportantWords)
		}
		if len(insights.ImportantPhrases) > 0 {
			b.WriteString("<h2>Important Phrases</h2>\n")
			writeJSONBlock(&b, insights.ImportantPhrases)
		}
		if len(insights.SpecialNames) > 0 {
			b.WriteString("<h2>Special Names</h2>\n")
			writeJSONBlock(&b, insights.SpecialNames)
		}
		if len(insights.ArtlistPhrases) > 0 {
			b.WriteString("<h2>Artlist Phrases</h2>\n")
			writeJSONBlock(&b, insights.ArtlistPhrases)
		}
		if len(insights.EntityImages) > 0 {
			b.WriteString("<h2>Entity Images</h2>\n")
			writeJSONBlock(&b, insights.EntityImages)
		}
		if len(insights.ArtlistClipSuggestions) > 0 {
			b.WriteString("<h2>Artlist Clip Suggestions</h2>\n")
			writeJSONBlock(&b, insights.ArtlistClipSuggestions)
		}
		if len(insights.PhraseClipSuggestions) > 0 {
			b.WriteString("<h2>Phrase Clip Suggestions</h2>\n")
			writeJSONBlock(&b, insights.PhraseClipSuggestions)
		}
		if len(insights.IntroClips) > 0 {
			b.WriteString("<h2>Intro Clips</h2>\n")
			writeJSONBlock(&b, insights.IntroClips)
		}
		if insights.RecommendedDriveFolder != nil {
			b.WriteString("<h2>Recommended Drive Folder</h2>\n")
			writeJSONBlock(&b, insights.RecommendedDriveFolder)
		}

		// Full raw entities JSON
		b.WriteString("<h2>Entities JSON (Full Analysis)</h2>\n")
		b.WriteString("<pre style=\"")
		b.WriteString(preStyle)
		b.WriteString("\">")
		var entitiesPretty bytes.Buffer
		if err := json.Indent(&entitiesPretty, []byte(entitiesJSON), "", "  "); err == nil {
			b.WriteString(html.EscapeString(entitiesPretty.String()))
		} else {
			b.WriteString(html.EscapeString(entitiesJSON))
		}
		b.WriteString("</pre>\n")
	}

	// ── Metadata (per language) ───────────────────────────────────────────
	for _, m := range videoMetadata {
		lang := strings.TrimSpace(m.Language)
		if lang == "" {
			continue
		}
		b.WriteString("<h2>Metadata (")
		b.WriteString(html.EscapeString(lang))
		b.WriteString(")</h2>\n")
		b.WriteString("<pre style=\"")
		b.WriteString(preStyle)
		b.WriteString("\">")

		type langMeta struct {
			Language    string   `json:"language"`
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
		}
		lm := langMeta{
			Language:    m.Language,
			Title:       m.Title,
			Description: m.Description,
			Tags:        m.Tags,
		}
		langBytes, _ := json.MarshalIndent(lm, "", "  ")
		b.WriteString(html.EscapeString(string(langBytes)))
		b.WriteString("</pre>\n")
	}

	// Common JSON block — compact entity metadata (only when entities were extracted)
	if entitiesJSON != "" {
		jsonBlock := buildInsightsJSONBlock(title, insights)
		if jsonBlock != "" {
			b.WriteString("<h2>Common Metadata</h2>\n")
			b.WriteString("<pre style=\"")
			b.WriteString(preStyle)
			b.WriteString("\">")
			b.WriteString(html.EscapeString(jsonBlock))
			b.WriteString("</pre>\n")
		}
	}

	// Scenes JSON block (only when scenes with images were generated)
	if len(scenes) > 0 {
		b.WriteString("<h2>Scenes JSON</h2>\n")
		b.WriteString("<pre style=\"")
		b.WriteString(preStyle)
		b.WriteString("\">")
		scenesBytes, _ := json.MarshalIndent(scenes, "", "  ")
		b.WriteString(html.EscapeString(string(scenesBytes)))
		b.WriteString("</pre>\n")
	}

	return b.String()
}

// writeJSONBlock marshals v to indented JSON inside a <pre> block.
func writeJSONBlock(b *strings.Builder, v any) {
	b.WriteString("<pre style=\"")
	b.WriteString(preStyle)
	b.WriteString("\">")
	data, err := json.MarshalIndent(v, "", "  ")
	if err == nil && len(data) > 0 {
		b.WriteString(html.EscapeString(string(data)))
	}
	b.WriteString("</pre>\n")
}
