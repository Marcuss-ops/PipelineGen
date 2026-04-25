package script

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func buildPrompt(topic string, duration int, language, template string) string {
	wordCount := duration * 3
	style := "documentary"
	switch strings.ToLower(strings.TrimSpace(template)) {
	case "storytelling":
		style = "storytelling"
	case "top10":
		style = "top 10"
	case "biography":
		style = "biography"
	}

	return fmt.Sprintf(
		"Genera un testo %s su %s in lingua %s. Lunghezza target %d parole, con un minimo di %d e un massimo di %d parole. Scrivi almeno 3 paragrafi completi. Scrivi solo il testo finale, senza introduzioni, titoli, note tecniche, meta-commenti o frasi tipo 'okay, here's'. Se il contenuto rischia di essere troppo corto, espandi con dettagli, transizioni e contesto coerente fino a raggiungere il target.",
		style, topic, language, wordCount, wordCount-25, wordCount+25,
	)
}

func buildPreviewPath(dir, title string) string {
	return filepath.Join(dir, sanitizeFilename(title)+".txt")
}

func sanitizeFilename(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		if r == ' ' || r == '-' || r == '_' {
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "script_preview"
	}
	return out
}

func writePreview(path, title, content string) error {
	data := []byte(title + "\n\n" + content)
	return os.WriteFile(path, data, 0644)
}
