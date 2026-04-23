package scriptdocs

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/util"
)

func (s *ScriptDocService) createDocWithFallback(ctx context.Context, title string, content string) (docID string, docURL string, err error) {
	if s.docClient == nil {
		logger.Warn("Docs client unavailable, saving local preview instead", zap.String("title", title))
		return s.saveToLocalFile(title, content)
	}
	// Use a background context for the Docs upload so a cancelled HTTP request
	// does not downgrade a completed generation into a local-only file.
	docCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	logger.Info("Creating Google Doc", zap.String("title", title))
	doc, err := s.docClient.CreateDoc(docCtx, title, content, "")
	if err != nil {
		logger.Warn("Google Docs creation failed, falling back to local file", zap.Error(err))
		return s.saveToLocalFile(title, content)
	}
	logger.Info("Google Doc created", zap.String("title", title), zap.String("doc_id", doc.ID), zap.String("doc_url", doc.URL))
	return doc.ID, doc.URL, nil
}

func (s *ScriptDocService) saveToLocalFile(title string, content string) (string, string, error) {
	logger.Info("Saving local preview document", zap.String("title", title))
	filename := strings.ReplaceAll(title, " ", "_")
	filename = strings.ReplaceAll(filename, ":", "")
	filename = fmt.Sprintf("/tmp/%s_%d.txt", filename[:util.Min(50, len(filename))], time.Now().Unix())
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		return "", "", fmt.Errorf("failed to save local file: %w", err)
	}
	logger.Info("Local preview saved", zap.String("path", filename))
	return "local_file", fmt.Sprintf("file://%s", filename), nil
}

func (s *ScriptDocService) buildMultilingualDocument(topic string, duration int, stockFolder StockFolder, langResults []LanguageResult) string {
	var b strings.Builder
	mode := normalizeAssociationMode(s.currentAssociationMode)

	mins := duration / 60
	secs := duration % 60
	b.WriteString(fmt.Sprintf("📝 %s\n", topic))
	b.WriteString(fmt.Sprintf("Topic: %s | Durata: %d:%02d | %s\n", topic, mins, secs, time.Now().Format("02/01/2006")))
	b.WriteString(fmt.Sprintf("Mode: %s\n", mode))
	b.WriteString(strings.Repeat("=", 100) + "\n\n")

	if mode == AssociationModeImagesFull || mode == AssociationModeImagesOnly {
		b.WriteString("🖼️ IMAGE MODE\n\n")
		if strings.TrimSpace(stockFolder.Name) != "" || strings.TrimSpace(stockFolder.URL) != "" {
			b.WriteString(fmt.Sprintf("📁 %s\n", stockFolder.Name))
			if strings.TrimSpace(stockFolder.URL) != "" {
				b.WriteString(fmt.Sprintf("🔗 %s\n", stockFolder.URL))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("📦 STOCK DRIVE\n\n")
	stockSections := s.buildStockDriveSections(stockFolder, langResults)
	if len(stockSections) == 0 {
		b.WriteString(fmt.Sprintf("📁 %s\n", stockFolder.Name))
		b.WriteString(fmt.Sprintf("🏷 Nome: %s\n", stockFolder.Name))
		b.WriteString(fmt.Sprintf("🔗 %s\n\n", stockFolder.URL))
	} else {
		b.WriteString(fmt.Sprintf("📎 STOCK COLLEGATI (%d)\n", len(stockSections)))
		b.WriteString(strings.Repeat("-", 30) + "\n")
		for i, section := range stockSections {
			b.WriteString(fmt.Sprintf("%d. 📁 %s\n", i+1, section.FolderName))
			if section.StartTime > 0 || section.EndTime > 0 {
				mins := section.StartTime / 60
				secs := section.StartTime % 60
				endMins := section.EndTime / 60
				endSecs := section.EndTime % 60
				b.WriteString(fmt.Sprintf("   ⏱ %d:%02d - %d:%02d\n", mins, secs, endMins, endSecs))
			}
			if strings.TrimSpace(section.FolderURL) != "" {
				b.WriteString(fmt.Sprintf("   🔗 %s\n", section.FolderURL))
			}
			if len(section.Links) == 0 {
				b.WriteString("   - Nessun collegamento drive rilevato\n\n")
				continue
			}
			for _, link := range section.Links {
				endPhrase := link.EndPhrase
				if strings.TrimSpace(endPhrase) == "" {
					endPhrase = link.StartPhrase
				}
				if strings.TrimSpace(link.StartPhrase) != "" {
					b.WriteString(fmt.Sprintf("   • Inizio: %s\n", link.StartPhrase))
				}
				if strings.TrimSpace(endPhrase) != "" {
					b.WriteString(fmt.Sprintf("     Fine: %s\n", endPhrase))
				}
				if strings.TrimSpace(link.ClipName) != "" {
					b.WriteString(fmt.Sprintf("     Clip: %s\n", link.ClipName))
				}
				b.WriteString("\n")
			}
		}
		b.WriteString(strings.Repeat("=", 100) + "\n\n")
	}
	b.WriteString(strings.Repeat("=", 100) + "\n\n")

	for _, lr := range langResults {
		info, ok := LanguageInfo[lr.Language]
		if !ok {
			info.Name = lr.Language
		}

		b.WriteString(fmt.Sprintf("🌍 %s\n\n", info.Name))
		b.WriteString(strings.Repeat("-", 80) + "\n\n")
		b.WriteString(lr.FullText + "\n\n")
		b.WriteString(strings.Repeat("-", 80) + "\n\n")

		b.WriteString(fmt.Sprintf("🔍 ENTITÀ ESTRATTE (%s)\n\n", info.Name))
		b.WriteString(fmt.Sprintf("📌 FRASI IMPORTANTI (%d)\n", len(lr.FrasiImportanti)))
		for i, f := range lr.FrasiImportanti {
			b.WriteString(fmt.Sprintf("   %d. %s\n", i+1, f))
		}
		b.WriteString("\n")

		b.WriteString(fmt.Sprintf("👤 NOMI SPECIALI (%d)\n", len(lr.NomiSpeciali)))
		b.WriteString("   " + strings.Join(lr.NomiSpeciali, ", ") + "\n\n")

		b.WriteString(fmt.Sprintf("🔑 PAROLE IMPORTANTI (%d)\n", len(lr.ParoleImportant)))
		b.WriteString("   " + strings.Join(lr.ParoleImportant, ", ") + "\n\n")

		b.WriteString(fmt.Sprintf("🖼️ ENTITÀ CON IMMAGINE (%d)\n", len(lr.EntitaConImmagine)))
		for entity, imageURL := range lr.EntitaConImmagine {
			b.WriteString(fmt.Sprintf("   🖼 %s → %s\n", entity, imageURL))
		}
		b.WriteString("\n")
		b.WriteString(strings.Repeat("-", 80) + "\n\n")

		switch mode {
		case AssociationModeImagesFull, AssociationModeImagesOnly:
			groups := groupImageAssociationsByWindow(lr.ImageAssociations)
			header := "🖼️ IMAGES FULL"
			if mode == AssociationModeImagesOnly {
				header = "🖼️ IMAGES ONLY"
			}
			b.WriteString(fmt.Sprintf("%s (%d)\n", header, len(lr.ImageAssociations)))
			b.WriteString(strings.Repeat("-", 30) + "\n")
			if len(groups) == 0 {
				b.WriteString("   - Nessuna immagine rilevante trovata\n\n")
			} else {
				for i, group := range groups {
					b.WriteString(fmt.Sprintf("%d. ⏱ %s\n", i+1, formatTimestampWindow(group.StartTime, group.EndTime)))
					startPhrase, endPhrase := chapterBoundaries(group.Phrase)
					if strings.TrimSpace(startPhrase) != "" {
						b.WriteString(fmt.Sprintf("   Inizio: %s\n", truncate(startPhrase, 180)))
					}
					if strings.TrimSpace(endPhrase) != "" && endPhrase != startPhrase {
						b.WriteString(fmt.Sprintf("   Fine: %s\n", truncate(endPhrase, 180)))
					}
					b.WriteString("   Link:\n")
					for _, img := range group.Images {
						title := strings.TrimSpace(img.Title)
						if title == "" {
							title = img.Entity
						}
						line := fmt.Sprintf("   - %s", title)
						if strings.TrimSpace(img.ImageURL) != "" {
							line += fmt.Sprintf(" -> %s", img.ImageURL)
						}
						b.WriteString(line + "\n")
						if img.Resolution != nil {
							if strings.TrimSpace(img.Resolution.SelectedFrom) != "" {
								b.WriteString(fmt.Sprintf("     Origine: %s\n", img.Resolution.SelectedFrom))
							}
							if len(img.Resolution.SelectionOrder) > 0 {
								b.WriteString(fmt.Sprintf("     Fallback: %s\n", strings.Join(img.Resolution.SelectionOrder, " -> ")))
							}
						}
					}
					b.WriteString("\n")
				}
			}
			b.WriteString(strings.Repeat("=", 100) + "\n\n")
		case AssociationModeMixed:
			b.WriteString(fmt.Sprintf("🧩 MIXED (%d)\n", len(lr.MixedSegments)))
			b.WriteString(strings.Repeat("-", 30) + "\n")
			if len(lr.MixedSegments) == 0 {
				b.WriteString("   - Nessun segmento misto disponibile\n\n")
			} else {
				for i, segment := range lr.MixedSegments {
					b.WriteString(fmt.Sprintf("%d. ⏱ %s\n", i+1, formatTimestampWindow(segment.StartTime, segment.EndTime)))
					if strings.TrimSpace(segment.Phrase) != "" {
						b.WriteString(fmt.Sprintf("   Inizio: %s\n", truncate(segment.Phrase, 180)))
					}
					if strings.TrimSpace(segment.Reason) != "" {
						b.WriteString(fmt.Sprintf("   Scelta: %s\n", segment.Reason))
					}
					if segment.Resolution != nil {
						if strings.TrimSpace(segment.Resolution.SelectedFrom) != "" {
							b.WriteString(fmt.Sprintf("   Origine: %s\n", segment.Resolution.SelectedFrom))
						}
						if len(segment.Resolution.SelectionOrder) > 0 {
							b.WriteString(fmt.Sprintf("   Fallback: %s\n", strings.Join(segment.Resolution.SelectionOrder, " -> ")))
						}
						if len(segment.Resolution.Notes) > 0 {
							b.WriteString(fmt.Sprintf("   Note: %s\n", strings.Join(segment.Resolution.Notes, " | ")))
						}
					}
					switch strings.ToLower(strings.TrimSpace(segment.SourceKind)) {
					case "image":
						if segment.Image != nil {
							b.WriteString(fmt.Sprintf("   Fonte: IMAGE | %s\n", segment.Image.Title))
							if strings.TrimSpace(segment.Image.ImageURL) != "" {
								b.WriteString(fmt.Sprintf("   Link: %s\n", segment.Image.ImageURL))
							}
						}
					case "artlist":
						if segment.Clip != nil && segment.Clip.Clip != nil {
							b.WriteString(fmt.Sprintf("   Fonte: ARTLIST | %s\n", segment.Clip.Clip.Name))
							b.WriteString(fmt.Sprintf("   Link: %s\n", segment.Clip.Clip.URL))
						}
					default:
						if segment.Clip != nil {
							kind := strings.ToUpper(strings.TrimSpace(segment.Clip.Type))
							if kind == "" {
								kind = "CLIP"
							}
							b.WriteString(fmt.Sprintf("   Fonte: %s\n", kind))
							if segment.Clip.DynamicClip != nil {
								b.WriteString(fmt.Sprintf("   Clip: %s\n", segment.Clip.DynamicClip.Filename))
							} else if segment.Clip.ClipDB != nil {
								b.WriteString(fmt.Sprintf("   Clip: %s\n", segment.Clip.ClipDB.Filename))
							}
						}
					}
					b.WriteString("\n")
				}
			}
			b.WriteString(strings.Repeat("=", 100) + "\n\n")
		}

		s.writeClipAndArtlistSections(&b, lr, stockFolder)
	}

	return b.String()
}
