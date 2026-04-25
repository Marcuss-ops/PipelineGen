package script

import "strings"

func renderScriptDocument(title string, sections []ScriptSection) string {
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(strings.Repeat("=", len(title)))
	b.WriteString("\n\n")

	for _, section := range sections {
		b.WriteString(section.Title)
		b.WriteString("\n")
		b.WriteString(strings.Repeat("-", len(section.Title)))
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(section.Body))
		b.WriteString("\n\n")
	}

	return strings.TrimSpace(b.String())
}
