package handlers

import (
	"fmt"
	"html"
	"strings"

	"velox/go-master/pkg/textutil"
)

func chapterLabelForLang(lang string) string {
	switch lang {
	case "it":
		return "Capitolo"
	case "fr":
		return "Chapitre"
	case "es":
		return "Capítulo"
	case "de":
		return "Kapitel"
	default:
		return "Chapter"
	}
}

func buildBatchGoogleDocHTML(title string, parts []generatedPart, noChapters bool, language string) string {
	return buildBatchGoogleDocHTMLWithTranslations(title, parts, noChapters, language, nil)
}

// buildBatchGoogleDocHTMLWithTranslations generates the Google Doc HTML, optionally using translated topics
// when translatedTopics is provided (one per part).
func buildBatchGoogleDocHTMLWithTranslations(title string, parts []generatedPart, noChapters bool, language string, translatedTopics []string) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")
	b.WriteString(fmt.Sprintf("<h1>%s</h1>", html.EscapeString(strings.TrimSpace(title))))
	cl := chapterLabelForLang(language)
	getTopic := func(idx int) string {
		if translatedTopics != nil && idx < len(translatedTopics) && strings.TrimSpace(translatedTopics[idx]) != "" {
			return translatedTopics[idx]
		}
		return parts[idx].topic
	}
	if !noChapters {
		b.WriteString(fmt.Sprintf("<h2>%s</h2>", html.EscapeString("Table of Contents")))
		b.WriteString("<ol>")
		for idx := range parts {
			b.WriteString(fmt.Sprintf("<li><a href=\"#ch-%d\">%s %d: %s</a></li>", idx+1, cl, idx+1, html.EscapeString(strings.TrimSpace(getTopic(idx)))))
		}
		b.WriteString("</ol>")
		b.WriteString("<hr>")
	}
	for idx := range parts {
		part := parts[idx]
		topic := getTopic(idx)
		if !noChapters {
			b.WriteString(fmt.Sprintf("<section id=\"ch-%d\">", idx+1))
			b.WriteString(fmt.Sprintf("<h2>%s %d: %s</h2>", cl, idx+1, html.EscapeString(strings.TrimSpace(topic))))
		}
		for _, para := range strings.Split(part.content, "\n\n") {
			para = strings.TrimSpace(para)
			if para == "" {
				continue
			}
			// Strip markdown artifacts before rendering in Doc
			para = textutil.CleanForVoiceover(para)
			para = html.EscapeString(para)
			para = strings.ReplaceAll(para, "\n", "<br>")
			b.WriteString(fmt.Sprintf("<p>%s</p>", para))
		}
		if !noChapters {
			b.WriteString("</section>")
			if idx < len(parts)-1 {
				b.WriteString("<hr>")
			}
		} else {
			b.WriteString("<br>")
		}
	}
	b.WriteString("</body></html>")
	return b.String()
}
