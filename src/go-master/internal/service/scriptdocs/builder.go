package scriptdocs

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/util"
)

func (s *ScriptDocService) createDocWithFallback(ctx context.Context, title string, content string) (docID string, docURL string, err error) {
	if s.docClient == nil {
		return s.saveToLocalFile(title, content)
	}
	doc, err := s.docClient.CreateDoc(ctx, title, content, "")
	if err != nil {
		logger.Warn("Google Docs creation failed, falling back to local file", zap.Error(err))
		return s.saveToLocalFile(title, content)
	}
	return doc.ID, doc.URL, nil
}

func (s *ScriptDocService) saveToLocalFile(title string, content string) (string, string, error) {
	filename := strings.ReplaceAll(title, " ", "_")
	filename = strings.ReplaceAll(filename, ":", "")
	filename = fmt.Sprintf("/tmp/%s_%d.txt", filename[:util.Min(50, len(filename))], time.Now().Unix())
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		return "", "", fmt.Errorf("failed to save local file: %w", err)
	}
	return "local_file", fmt.Sprintf("file://%s", filename), nil
}

func (s *ScriptDocService) buildMultilingualDocument(topic string, duration int, stockFolder StockFolder, langResults []LanguageResult) string {
	var b strings.Builder
	caser := cases.Title(language.Und)

	mins := duration / 60
	secs := duration % 60
	b.WriteString(fmt.Sprintf("📝 %s\n", topic))
	b.WriteString(fmt.Sprintf("Topic: %s | Durata: %d:%02d | %s\n", topic, mins, secs, time.Now().Format("02/01/2006")))
	b.WriteString(strings.Repeat("=", 100) + "\n\n")

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

		if normalizeAssociationMode(s.currentAssociationMode) == AssociationModeImagesFull {
			b.WriteString(fmt.Sprintf("🖼️ IMAGES FULL (%d)\n", len(lr.ImageAssociations)))
			b.WriteString(strings.Repeat("-", 30) + "\n")
			if len(lr.ImageAssociations) == 0 {
				b.WriteString("   - Nessuna immagine rilevante trovata\n\n")
			} else {
				for i, img := range lr.ImageAssociations {
					b.WriteString(fmt.Sprintf("%d. 💬 \"%s\"\n", i+1, truncate(associationLabel(ClipAssociation{Phrase: img.Phrase, MatchedKeyword: img.Entity}), 160)))
					if img.StartTime > 0 || img.EndTime > 0 {
						b.WriteString(fmt.Sprintf("   ⏱ %s\n", formatTimestampWindow(img.StartTime, img.EndTime)))
					}
					if img.Entity != "" {
						b.WriteString(fmt.Sprintf("   🏷 Entity: %s\n", img.Entity))
					}
					if img.Title != "" {
						b.WriteString(fmt.Sprintf("   🖼 Titolo: %s\n", img.Title))
					}
					if img.ImageURL != "" {
						b.WriteString(fmt.Sprintf("   🔗 %s\n", img.ImageURL))
					}
					if img.PageURL != "" {
						b.WriteString(fmt.Sprintf("   🌐 %s\n", img.PageURL))
					}
					if img.Source != "" {
						b.WriteString(fmt.Sprintf("   ✅ Fonte: %s\n", img.Source))
					}
					b.WriteString(fmt.Sprintf("   📊 Score: %.2f\n\n", img.Score))
				}
			}
			b.WriteString(strings.Repeat("=", 100) + "\n\n")
			continue
		}

		if len(lr.Associations) >= 0 {
			// Group 1: Drive Clips (real clips only: Dynamic + Stock DB)
			driveCount := 0
			var driveBuffer strings.Builder
			for _, assoc := range lr.Associations {
				sourceKind := s.associationSourceKind(assoc)
				if sourceKind != "stock" && sourceKind != "dynamic" {
					continue
				}

				hasValidClip := false
				if assoc.DynamicClip != nil {
					folderCheck := assoc.DynamicClip.Folder
					if folderCheck == "" {
						folderCheck = assoc.DynamicClip.FolderID
					}
					if folderCheck != "" && s.stockDB != nil {
						if folder, err := s.stockDB.FindFolderByDriveID(folderCheck); err == nil && folder != nil {
							if strings.Contains(strings.ToLower(folder.FullPath), "artlist") {
								continue
							}
						}
					}
					if assoc.DynamicClip.Filename != "" &&
						!strings.HasPrefix(assoc.DynamicClip.Filename, assoc.MatchedKeyword) {
						hasValidClip = true
					}
				}
				if assoc.ClipDB != nil {
					folderCheck := assoc.ClipDB.FolderID
					if folderCheck != "" && s.stockDB != nil {
						if folder, err := s.stockDB.FindFolderByDriveID(folderCheck); err == nil && folder != nil {
							if strings.Contains(strings.ToLower(folder.FullPath), "artlist") {
								continue
							}
						}
					}
					if assoc.ClipDB.Filename != "" {
						hasValidClip = true
					}
				}

				if !hasValidClip {
					continue
				}
				if assoc.Type == "DYNAMIC" || assoc.Type == "STOCK_DB" || assoc.Type == "STOCK" {
					driveCount++
					driveBuffer.WriteString(fmt.Sprintf("%d. 💬 \"%s\"\n", driveCount, truncate(associationLabel(assoc), 160)))
					switch assoc.Type {
					case "DYNAMIC":
						if assoc.DynamicClip != nil {
							driveBuffer.WriteString("   ✅ Fonte primaria: DRIVE FOLDER (da DYNAMIC SEARCH)\n")
							driveBuffer.WriteString(fmt.Sprintf("   📛 Clip: %s\n", assoc.DynamicClip.Filename))
							driveBuffer.WriteString(fmt.Sprintf("   📁 %s\n", assoc.DynamicClip.Folder))
							if assoc.DynamicClip.Folder != "" && !strings.Contains(assoc.DynamicClip.Folder, "/") {
								driveBuffer.WriteString(fmt.Sprintf("   🔗 https://drive.google.com/drive/folders/%s\n", assoc.DynamicClip.Folder))
							}
						}
					case "STOCK_DB", "STOCK":
						if assoc.ClipDB != nil {
							driveBuffer.WriteString("   ✅ Fonte primaria: DRIVE FOLDER (da STOCK DB)\n")
							driveBuffer.WriteString(fmt.Sprintf("   📛 Clip: %s\n", assoc.ClipDB.Filename))
							if assoc.ClipDB.FolderID != "" {
								folderName := assoc.ClipDB.FolderID
								folderURL := fmt.Sprintf("https://drive.google.com/drive/folders/%s", assoc.ClipDB.FolderID)
								if s.stockDB != nil {
									if folder, err := s.stockDB.FindFolderByDriveID(assoc.ClipDB.FolderID); err == nil && folder != nil {
										if strings.TrimSpace(folder.FullPath) != "" {
											folderName = folder.FullPath
										}
									}
								}
								driveBuffer.WriteString(fmt.Sprintf("   📁 %s\n", folderName))
								driveBuffer.WriteString(fmt.Sprintf("   🔗 %s\n", folderURL))
							}
							if desc := buildClipDescriptionFromTags(assoc.ClipDB.Tags); desc != "" {
								driveBuffer.WriteString(fmt.Sprintf("   📝 Descrizione: %s\n", desc))
							}
						}
					}
					if assoc.MatchedKeyword != "" {
						driveBuffer.WriteString(fmt.Sprintf("   🔍 Match: %s\n", assoc.MatchedKeyword))
					}
					driveBuffer.WriteString(fmt.Sprintf("   📊 Confidenza: %.2f\n\n", assoc.Confidence))
				}
			}

			b.WriteString(fmt.Sprintf("🔴 CLIP DRIVE (%d)\n", driveCount))
			b.WriteString(strings.Repeat("-", 30) + "\n")
			if driveCount > 0 {
				b.WriteString(driveBuffer.String())
			} else {
				b.WriteString("   - None\n\n")
			}
			b.WriteString(strings.Repeat("=", 100) + "\n\n")

			// Group 2: Artlist (clip-level in default mode, timeline-folder in fullartlist mode)
			if s.currentAssociationMode == AssociationModeFullArtlist && len(lr.ArtlistTimeline) > 0 {
				b.WriteString(fmt.Sprintf("🟢 ARTLIST TIMELINE (%d)\n", len(lr.ArtlistTimeline)))
				b.WriteString(strings.Repeat("-", 30) + "\n")
				for i, tl := range lr.ArtlistTimeline {
					b.WriteString(fmt.Sprintf("%d. ⏱ %s\n", i+1, tl.Timestamp))
					b.WriteString(fmt.Sprintf("   🏷 Keyword: %s\n", tl.Keyword))
					if strings.TrimSpace(tl.FolderName) != "" {
						b.WriteString(fmt.Sprintf("   📁 %s\n", tl.FolderName))
					}
					if strings.TrimSpace(tl.FolderURL) != "" {
						b.WriteString(fmt.Sprintf("   🔗 %s\n", tl.FolderURL))
					}
					b.WriteString("\n")
				}
			} else {
				artlistCount := 0
				var artlistBuffer strings.Builder
				for _, assoc := range lr.ArtlistAssociations {
					if assoc.Type == "ARTLIST" {
						artlistCount++
						artlistBuffer.WriteString(fmt.Sprintf("%d. 💬 \"%s\"\n", artlistCount, truncate(associationLabel(assoc), 160)))
						if assoc.Clip != nil {
							artlistBuffer.WriteString("   ✅ Fonte primaria: ARTLIST\n")
							artlistBuffer.WriteString(fmt.Sprintf("   🟢 Artlist: %s\n", assoc.Clip.Name))
							artlistBuffer.WriteString(fmt.Sprintf("   📁 Stock/Artlist/%s\n", caser.String(strings.ToLower(assoc.Clip.Term))))
							artlistBuffer.WriteString(fmt.Sprintf("   🔗 %s\n", assoc.Clip.URL))
						}
						if assoc.MatchedKeyword != "" {
							artlistBuffer.WriteString(fmt.Sprintf("   🔍 Match: %s\n", assoc.MatchedKeyword))
						}
						artlistBuffer.WriteString(fmt.Sprintf("   📊 Confidenza: %.2f\n\n", assoc.Confidence))
					}
				}

				b.WriteString(fmt.Sprintf("🟢 CLIP ARTLIST (%d)\n", artlistCount))
				b.WriteString(strings.Repeat("-", 30) + "\n")
				if artlistCount > 0 {
					b.WriteString(artlistBuffer.String())
				} else {
					b.WriteString("   - None\n\n")
				}
			}
			b.WriteString(strings.Repeat("=", 100) + "\n\n")
		}

		b.WriteString(strings.Repeat("=", 100) + "\n\n")
	}

	return b.String()
}

type stockDriveLink struct {
	StartPhrase string
	EndPhrase   string
	ClipName    string
}

type stockDriveSection struct {
	ChapterIndex int
	FolderName   string
	FolderURL    string
	StartTime    int
	EndTime      int
	Links        []stockDriveLink
}

func (s *ScriptDocService) buildStockDriveSections(stockFolder StockFolder, langResults []LanguageResult) []stockDriveSection {
	sections := make([]stockDriveSection, 0, 8)
	for _, lr := range langResults {
		for idx, chapter := range lr.Chapters {
			assoc := pickStockAssociationForChapter(lr.StockAssociations, idx)
			sourceKind := s.associationSourceKind(assoc)

			isStockDrive := sourceKind == "stock" || sourceKind == "dynamic"
			if !isStockDrive {
				continue
			}

			folderName, folderURL, clipName := s.resolveStockAssociationFolder(assoc, StockFolder{})

			if strings.Contains(strings.ToLower(folderName), "artlist") {
				continue
			}

			hasValidClip := false
			if assoc.DynamicClip != nil && assoc.DynamicClip.Filename != "" {
				if !strings.HasPrefix(assoc.DynamicClip.Filename, assoc.MatchedKeyword) {
					hasValidClip = true
				}
			}
			if assoc.ClipDB != nil && assoc.ClipDB.Filename != "" {
				hasValidClip = true
			}
			if !hasValidClip {
				continue
			}

			startPhrase, endPhrase := chapterBoundaries(chapter.SourceText)

			if strings.TrimSpace(folderName) == "" && strings.TrimSpace(folderURL) == "" {
				continue
			}

			sections = append(sections, stockDriveSection{
				ChapterIndex: idx + 1,
				FolderName:   folderName,
				FolderURL:    folderURL,
				StartTime:    chapter.StartTime,
				EndTime:      chapter.EndTime,
				Links: []stockDriveLink{{
					StartPhrase: startPhrase,
					EndPhrase:   endPhrase,
					ClipName:    clipName,
				}},
			})
		}
	}

	return sections
}

func pickStockAssociationForChapter(associations []ClipAssociation, idx int) ClipAssociation {
	if idx >= 0 && idx < len(associations) {
		if strings.TrimSpace(associations[idx].Type) != "" {
			return associations[idx]
		}
	}
	for i := idx - 1; i >= 0; i-- {
		if i >= 0 && i < len(associations) && strings.TrimSpace(associations[i].Type) != "" {
			return associations[i]
		}
	}
	return ClipAssociation{}
}

func (s *ScriptDocService) resolveStockAssociationFolder(assoc ClipAssociation, fallback StockFolder) (folderName, folderURL, clipName string) {
	switch assoc.Type {
	case "DYNAMIC":
		if assoc.DynamicClip != nil {
			clipName = assoc.DynamicClip.Filename
			if assoc.DynamicClip.Folder != "" {
				folderURL = fmt.Sprintf("https://drive.google.com/drive/folders/%s", assoc.DynamicClip.Folder)
				folderName = assoc.DynamicClip.Folder
				if s.stockDB != nil {
					if folder, err := s.stockDB.FindFolderByDriveID(assoc.DynamicClip.Folder); err == nil && folder != nil {
						if strings.TrimSpace(folder.FullPath) != "" {
							folderName = folder.FullPath
						}
					}
				}
			}
		}
	case "STOCK_DB", "STOCK":
		if assoc.ClipDB != nil {
			clipName = assoc.ClipDB.Filename
			if assoc.ClipDB.FolderID != "" {
				folderURL = fmt.Sprintf("https://drive.google.com/drive/folders/%s", assoc.ClipDB.FolderID)
				folderName = assoc.ClipDB.FolderID
				if s.stockDB != nil {
					if folder, err := s.stockDB.FindFolderByDriveID(assoc.ClipDB.FolderID); err == nil && folder != nil {
						if strings.TrimSpace(folder.FullPath) != "" {
							folderName = folder.FullPath
						}
					}
				}
			}
		}
	case "STOCK_FOLDER":
		if assoc.StockFolder != nil {
			folderName = assoc.StockFolder.Name
			folderURL = assoc.StockFolder.URL
		}
	}

	if folderName == "" {
		folderName = fallback.Name
	}
	if folderURL == "" {
		folderURL = fallback.URL
	}
	return folderName, folderURL, clipName
}

func chapterBoundaries(text string) (startPhrase, endPhrase string) {
	sentences := ExtractSentences(text)
	if len(sentences) == 0 {
		cleaned := compactSnippet(text, 72)
		return cleaned, cleaned
	}
	startPhrase = compactSnippet(sentences[0], 72)
	endPhrase = compactSnippet(sentences[len(sentences)-1], 72)
	if endPhrase == "" {
		endPhrase = startPhrase
	}
	return startPhrase, endPhrase
}

func compactSnippet(text string, maxLen int) string {
	cleaned := strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if cleaned == "" {
		return ""
	}
	if len(cleaned) <= maxLen {
		return cleaned
	}
	cut := maxLen
	if cut > len(cleaned) {
		cut = len(cleaned)
	}
	snippet := cleaned[:cut]
	if idx := strings.LastIndexAny(snippet, " ,;:-"); idx > 40 {
		snippet = snippet[:idx]
	}
	return strings.TrimSpace(snippet) + "..."
}

func langNames(results []LanguageResult) string {
	var names []string
	for _, r := range results {
		if info, ok := LanguageInfo[r.Language]; ok {
			names = append(names, info.Name)
		} else {
			names = append(names, r.Language)
		}
	}
	return strings.Join(names, ", ")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func buildClipDescriptionFromTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}

	seen := make(map[string]bool)
	parts := make([]string, 0, 4)
	for _, raw := range tags {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, tag)
		if len(parts) == 4 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

func associationLabel(assoc ClipAssociation) string {
	if phrase := strings.TrimSpace(assoc.Phrase); phrase != "" {
		return phrase
	}
	if kw := strings.TrimSpace(assoc.MatchedKeyword); kw != "" {
		return kw
	}
	return assoc.Phrase
}
