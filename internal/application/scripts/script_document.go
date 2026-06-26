package scripts

import (
	"fmt"
	"html"
	"strings"
)

// BuildScriptDocumentContent renders the canonical Google Doc body for every
// single-script generation flow. The full script is always present; clip
// mapping and entity extraction are appended as audit sections when available.
// This makes it possible to verify in one document which source clips were
// attached to each generated scene.
func BuildScriptDocumentContent(title, script string, clipScenes []ClipScene, entitiesJSON string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Generated Script"
	}

	var b strings.Builder
	b.WriteString("<html><head><style>")
	b.WriteString("body{font-family:Arial,Helvetica,sans-serif;font-size:11pt;line-height:1.55;margin:24px}")
	b.WriteString("h1{font-size:18pt}h2{font-size:14pt;margin-top:24px}")
	b.WriteString("p{margin:8px 0}.clip{border-left:3px solid #777;padding:8px 12px;margin:12px 0;background:#f7f7f7}")
	b.WriteString(".meta{font-size:9pt;color:#555}.entity-json{font-family:monospace;font-size:9pt;white-space:pre-wrap}")
	b.WriteString("</style></head><body>")
	b.WriteString("<h1>")
	b.WriteString(html.EscapeString(title))
	b.WriteString("</h1><h2>Script</h2>")

	for _, paragraph := range documentParagraphs(script) {
		b.WriteString("<p>")
		b.WriteString(html.EscapeString(paragraph))
		b.WriteString("</p>")
	}

	if len(clipScenes) > 0 {
		b.WriteString("<h2>Clip map</h2>")
		for _, scene := range clipScenes {
			b.WriteString("<div class=\"clip\">")
			fmt.Fprintf(&b, "<div class=\"meta\">Scene %d", scene.SceneIndex+1)
			if scene.Kind != "" {
				b.WriteString(" · ")
				b.WriteString(html.EscapeString(scene.Kind))
			}
			if scene.ClipID != "" {
				b.WriteString(" · clip_id: ")
				b.WriteString(html.EscapeString(scene.ClipID))
			}
			b.WriteString("</div>")
			if scene.DriveLink != "" {
				b.WriteString("<div><a href=\"")
				b.WriteString(html.EscapeString(scene.DriveLink))
				b.WriteString("\">Open clip in Drive</a></div>")
			}
			if text := strings.TrimSpace(scene.Text); text != "" {
				b.WriteString("<p>")
				b.WriteString(html.EscapeString(text))
				b.WriteString("</p>")
			}
			b.WriteString("</div>")
		}
	}

	if entitiesJSON = strings.TrimSpace(entitiesJSON); entitiesJSON != "" {
		b.WriteString("<h2>Entity extraction</h2><div class=\"entity-json\">")
		b.WriteString(html.EscapeString(entitiesJSON))
		b.WriteString("</div>")
	}

	b.WriteString("</body></html>")
	return b.String()
}

func documentParagraphs(script string) []string {
	normalized := strings.ReplaceAll(script, "\r\n", "\n")
	paragraphs := nonEmptyBlocks(strings.Split(normalized, "\n\n"))
	if len(paragraphs) > 0 {
		return paragraphs
	}
	if text := strings.TrimSpace(script); text != "" {
		return []string{text}
	}
	return []string{"Script generation completed without text output."}
}
