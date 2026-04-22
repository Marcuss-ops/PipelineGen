package scriptdocs

import (
	"context"
	"fmt"
	"os"
	"sort"
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
	docCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	doc, err := s.docClient.CreateDoc(docCtx, title, content, "")
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

func (s *ScriptDocService) writeClipAndArtlistSections(b *strings.Builder, lr LanguageResult, stockFolder StockFolder) {
	caser := cases.Title(language.Und)

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
			if assoc.Resolution != nil {
				if strings.TrimSpace(assoc.Resolution.SelectedFrom) != "" {
					driveBuffer.WriteString(fmt.Sprintf("   🧭 Origine: %s\n", assoc.Resolution.SelectedFrom))
				}
				if len(assoc.Resolution.SelectionOrder) > 0 {
					driveBuffer.WriteString(fmt.Sprintf("   ↪ Fallback: %s\n", strings.Join(assoc.Resolution.SelectionOrder, " -> ")))
				}
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

	if normalizeAssociationMode(s.currentAssociationMode) == AssociationModeFullArtlist && len(lr.ArtlistTimeline) > 0 {
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
				if assoc.Resolution != nil {
					if strings.TrimSpace(assoc.Resolution.SelectedFrom) != "" {
						artlistBuffer.WriteString(fmt.Sprintf("   🧭 Origine: %s\n", assoc.Resolution.SelectedFrom))
					}
					if len(assoc.Resolution.SelectionOrder) > 0 {
						artlistBuffer.WriteString(fmt.Sprintf("   ↪ Fallback: %s\n", strings.Join(assoc.Resolution.SelectionOrder, " -> ")))
					}
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

type imageWindowGroup struct {
	StartTime int
	EndTime   int
	Phrase    string
	Images    []ImageAssociation
}

func groupImageAssociationsByWindow(images []ImageAssociation) []imageWindowGroup {
	if len(images) == 0 {
		return nil
	}
	ordered := append([]ImageAssociation(nil), images...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartTime == ordered[j].StartTime {
			if ordered[i].EndTime == ordered[j].EndTime {
				return ordered[i].Score > ordered[j].Score
			}
			return ordered[i].EndTime < ordered[j].EndTime
		}
		return ordered[i].StartTime < ordered[j].StartTime
	})

	groups := make([]imageWindowGroup, 0, len(ordered))
	for _, img := range ordered {
		if len(groups) == 0 {
			groups = append(groups, imageWindowGroup{
				StartTime: img.StartTime,
				EndTime:   img.EndTime,
				Phrase:    img.Phrase,
				Images:    []ImageAssociation{img},
			})
			continue
		}
		last := &groups[len(groups)-1]
		if last.StartTime == img.StartTime && last.EndTime == img.EndTime {
			last.Images = append(last.Images, img)
			if last.Phrase == "" {
				last.Phrase = img.Phrase
			}
			continue
		}
		groups = append(groups, imageWindowGroup{
			StartTime: img.StartTime,
			EndTime:   img.EndTime,
			Phrase:    img.Phrase,
			Images:    []ImageAssociation{img},
		})
	}
	return groups
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
