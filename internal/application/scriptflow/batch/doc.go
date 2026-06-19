package batch

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/platform"

	"go.uber.org/zap"
)

// chapterLabelForLang returns the localised "Chapter" label for a language code.
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

// ── Phase: Google Doc Creation ───────────────────────────────────────────────

// createBatchDoc creates a Google Doc for the batch and returns its URL and ID.
func (s *BatchService) createBatchDoc(ctx context.Context, docTitle string, generatedParts []generatedPart, noChapters bool, language string, folderID string) (string, string) {
	if s.docClient == nil {
		return "", ""
	}

	htmlContent := buildBatchGoogleDocHTML(docTitle, generatedParts, noChapters, language)
	doc, docErr := s.docClient.CreateDoc(ctx, docTitle, htmlContent, folderID)
	if docErr != nil {
		s.log.Warn("failed to create batch Google Doc", zap.Error(docErr), zap.String("folder_id", folderID))
		return "", ""
	}
	return doc.URL, doc.ID
}

// ── Phase: Async Voiceover ───────────────────────────────────────────────────

// spawnBatchVoiceover spawns a fire-and-forget goroutine for async voiceover generation.
// Uses context.WithoutCancel(ctx) to survive the handler's return (intentional).
func (s *BatchService) spawnBatchVoiceover(ctx context.Context, script, lang, docTitle, folderID, filename string) {
	destReq := &voiceover.DestinationRequest{
		FolderID:        folderID,
		Group:           "explainatory",
		SubfolderName:   docTitle,
		CreateSubfolder: true,
	}
	destCopy := *destReq
	go func(ts, lc, fn string, d *voiceover.DestinationRequest) {
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("panic in async voiceover goroutine", zap.Any("recover", r), zap.String("lang", lc))
			}
		}()
		// Background voiceover generation: derive a fresh context from the
		// caller-supplied ctx (without cancellation, so it survives the HTTP
		// response) plus a 30-minute hard deadline to prevent unbounded
		// goroutines if the voiceover backend hangs.
		voCtx, voCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
		defer voCancel()
		voRes, voErr := s.voService.GenerateWithDestination(voCtx, textutil.CleanForVoiceover(ts), lc, fn, d)
		if voErr != nil {
			s.log.Error("batch generation: async voiceover failed for language", zap.String("lang", lc), zap.Error(voErr))
		} else if voRes != nil {
			s.log.Info("batch generation: async voiceover completed for language", zap.String("lang", lc), zap.String("drive_link", voRes.DriveLink))
		}
	}(script, lang, filename, &destCopy)
}
