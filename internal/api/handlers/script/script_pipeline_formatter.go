package script

import (
	"fmt"
	"strings"
	"time"
)

func buildMinimalDocumentContent(title, topic string, duration int, lang, script string) string {
	var content strings.Builder
	today := time.Now().Format("02/01/2006")
	langUpper := strings.ToUpper(strings.TrimSpace(lang))
	if langUpper == "" {
		langUpper = "EN"
	}
	content.WriteString(fmt.Sprintf("# %s\n\n", title))
	content.WriteString(fmt.Sprintf("**Topic:** %s | **Durata:** %d:%02d | %s\n", topic, duration/60, duration%60, today))
	content.WriteString("====================================================================================================\n\n")
	content.WriteString(fmt.Sprintf("🌍 %s\n\n", langUpper))
	content.WriteString("--------------------------------------------------------------------------------\n\n")
	content.WriteString(strings.TrimSpace(script))
	content.WriteString("\n")
	return content.String()
}

// BuildDocumentContent generates the standardized Markdown content for the script document
func (h *ScriptPipelineHandler) BuildDocumentContent(
	title string,
	topic string,
	duration int,
	lang string,
	script string,
	segments []Segment,
	artlistAssocs []ArtlistAssoc,
	stockFolderID string,
	stockFolderName string,
	stockDriveAssocs []DriveFolderAssoc,
	clipDriveAssocs []DriveFolderAssoc,
	frasi []string,
	nomi []string,
	parole []string,
	entitaImmagini []EntityImage,
	imageAssociations []ImageAssociation,
	mixedSegments []MixedSegment,
	translations []Translation,
) string {
	var content strings.Builder
	today := time.Now().Format("02/01/2006")
	langUpper := strings.ToUpper(lang)
	if langUpper == "" {
		langUpper = "EN"
	}

	// Header
	content.WriteString(fmt.Sprintf("# %s\n\n", title))
	content.WriteString(fmt.Sprintf("**Topic:** %s | **Durata:** %d:%02d | %s\n", topic, duration/60, duration%60, today))
	content.WriteString("====================================================================================================\n\n")

	// 1. STOCK DRIVE SECTION
	if len(stockDriveAssocs) > 0 {
		content.WriteString("📦 STOCK DRIVE (CHAPTERS)\n\n")
		for i, assoc := range stockDriveAssocs {
			content.WriteString(fmt.Sprintf("   %d. 💬 %s\n", i+1, assoc.Phrase))
			if strings.TrimSpace(assoc.InitialPhrase) != "" {
				content.WriteString(fmt.Sprintf("      • Inizio: %s\n", assoc.InitialPhrase))
			}
			if strings.TrimSpace(assoc.FinalPhrase) != "" && assoc.FinalPhrase != assoc.InitialPhrase {
				content.WriteString(fmt.Sprintf("      • Fine: %s\n", assoc.FinalPhrase))
			}
			if strings.TrimSpace(assoc.FolderName) != "" {
				content.WriteString(fmt.Sprintf("      📁 %s\n", assoc.FolderName))
			}
			if strings.TrimSpace(assoc.FolderURL) != "" {
				content.WriteString(fmt.Sprintf("      🔗 %s\n", assoc.FolderURL))
			}
			content.WriteString("\n")
		}
	} else if stockFolderID != "" {
		folderName := stockFolderName
		if folderName == "" {
			folderName = topic
		}
		content.WriteString("📦 STOCK DRIVE\n\n")
		content.WriteString(fmt.Sprintf("   📁 %s\n", folderName))
		content.WriteString(fmt.Sprintf("   🔗 https://drive.google.com/drive/folders/%s\n\n", stockFolderID))
	} else {
		content.WriteString("📦 STOCK DRIVE\n\n")
		content.WriteString("   - None\n\n")
	}
	content.WriteString("====================================================================================================\n\n")

	// 2. DRIVE CLIPS SECTION
	content.WriteString("📂 DRIVE CLIPS\n\n")
	if len(clipDriveAssocs) > 0 {
		seenFolders := make(map[string]bool)
		for _, assoc := range clipDriveAssocs {
			if seenFolders[assoc.FolderURL] {
				continue
			}
			displayPhrase := strings.TrimSpace(assoc.Phrase)
			if displayPhrase == "" {
				displayPhrase = assoc.FolderName
			}
			content.WriteString(fmt.Sprintf("   💬 %s\n", displayPhrase))
			content.WriteString(fmt.Sprintf("   📁 %s\n", assoc.FolderName))
			content.WriteString(fmt.Sprintf("   🔗 %s\n\n", assoc.FolderURL))
			seenFolders[assoc.FolderURL] = true
		}
	} else {
		content.WriteString("   - None\n\n")
	}
	content.WriteString("====================================================================================================\n\n")

	// 3. ARTLIST CLIPS SECTION
	content.WriteString("🎞️ ARTLIST CLIPS\n\n")
	artlistCount := 0
	for _, assoc := range artlistAssocs {
		for _, c := range assoc.Clips {
			if strings.TrimSpace(c.URL) == "" {
				continue
			}
			artlistCount++
			content.WriteString(fmt.Sprintf("   %d. %s\n", artlistCount, c.Name))
			content.WriteString(fmt.Sprintf("      🔗 %s\n", c.URL))
			content.WriteString("\n")
			if artlistCount >= 2 {
				break
			}
		}
		if artlistCount >= 2 {
			break
		}
	}
	if artlistCount == 0 {
		content.WriteString("   - None\n\n")
	}
	content.WriteString("====================================================================================================\n\n")

	// 4. SCRIPT SECTION
	content.WriteString(fmt.Sprintf("🌍 %s\n\n", langUpper))
	content.WriteString("--------------------------------------------------------------------------------\n\n")
	if script != "" {
		content.WriteString(script)
		content.WriteString("\n\n--------------------------------------------------------------------------------\n\n")
	} else if len(segments) > 0 {
		for _, seg := range segments {
			content.WriteString(seg.Text)
			content.WriteString("\n\n")
		}
		content.WriteString("--------------------------------------------------------------------------------\n\n")
	}

	// 5. SUGGESTED ASSETS & ENTITIES
	if len(entitaImmagini) > 0 || len(frasi) > 0 || len(nomi) > 0 || len(parole) > 0 {
		content.WriteString("🖼️ SUGGESTED IMAGES & ENTITIES\n\n")

		// 5a. Visual entities (Immagini)
		if len(entitaImmagini) > 0 {
			for _, ent := range entitaImmagini {
				content.WriteString(fmt.Sprintf("   • 👤 %s", ent.Entity))
				if ent.ImageURL != "" {
					content.WriteString(fmt.Sprintf(" -> %s", ent.ImageURL))
				}
				content.WriteString("\n")
			}
			content.WriteString("\n")
		}

		// 5b. Frasi Importanti (Numerato)
		if len(frasi) > 0 {
			content.WriteString(fmt.Sprintf("📌 FRASI IMPORTANTI (%d)\n", len(frasi)))
			for i, fr := range frasi {
				content.WriteString(fmt.Sprintf("   %d. %s\n", i+1, fr))
			}
			content.WriteString("\n")
		}

		// 5c. Nomi Speciali (Separati da virgola)
		if len(nomi) > 0 {
			content.WriteString(fmt.Sprintf("👤 NOMI SPECIALI (%d)\n", len(nomi)))
			content.WriteString("   ")
			for i, n := range nomi {
				content.WriteString(n)
				if i < len(nomi)-1 {
					content.WriteString(", ")
				}
			}
			content.WriteString("\n\n")
		}

		// 5d. Parole Importanti (Separati da virgola)
		if len(parole) > 0 {
			content.WriteString(fmt.Sprintf("🔑 PAROLE IMPORTANTI (%d)\n", len(parole)))
			content.WriteString("   ")
			for i, p := range parole {
				content.WriteString(p)
				if i < len(parole)-1 {
					content.WriteString(", ")
				}
			}
			content.WriteString("\n\n")
		}
		content.WriteString("====================================================================================================\n\n")
	}

	// 6. IMAGES FULL (Se presenti)
	if len(imageAssociations) > 0 {
		groups := groupImageAssocsByWindow(imageAssociations)
		content.WriteString(fmt.Sprintf("🖼️ IMAGES FULL (%d)\n", len(imageAssociations)))
		content.WriteString(strings.Repeat("-", 30) + "\n")
		for i, group := range groups {
			content.WriteString(fmt.Sprintf("%d. ⏱ %s\n", i+1, formatTimestampWindow(group.StartTime, group.EndTime)))
			for _, img := range group.Images {
				title := strings.TrimSpace(img.Title)
				if title == "" {
					title = img.Entity
				}
				content.WriteString(fmt.Sprintf("   - %s -> %s\n", title, img.ImageURL))
			}
			content.WriteString("\n")
		}
		content.WriteString(strings.Repeat("=", 100) + "\n\n")
	}

	// 7. MIXED SEGMENTS (Se presenti)
	if len(mixedSegments) > 0 {
		content.WriteString(fmt.Sprintf("🧩 MIXED SEGMENTS (%d)\n", len(mixedSegments)))
		content.WriteString(strings.Repeat("-", 30) + "\n")
		for i, seg := range mixedSegments {
			content.WriteString(fmt.Sprintf("%d. ⏱ %s\n", i+1, formatTimestampWindow(seg.StartTime, seg.EndTime)))
			if seg.Image != nil && strings.TrimSpace(seg.Image.ImageURL) != "" {
				title := strings.TrimSpace(seg.Image.Title)
				if title == "" {
					title = seg.Image.Entity
				}
				content.WriteString(fmt.Sprintf("   Immagine: %s -> %s\n", title, seg.Image.ImageURL))
			}
			if seg.Clip != nil && strings.TrimSpace(seg.Clip.URL) != "" {
				content.WriteString(fmt.Sprintf("   Clip: %s -> %s\n", seg.Clip.Title, seg.Clip.URL))
			}
			content.WriteString("\n")
		}
		content.WriteString(strings.Repeat("=", 100) + "\n\n")
	}

	return content.String()
}

// imageAssocGroup groups image associations by time window.
type imageAssocGroup struct {
	StartTime int
	EndTime   int
	Phrase    string
	Images    []ImageAssociation
}

// groupImageAssocsByWindow groups image associations by chapter/time window.
func groupImageAssocsByWindow(assocs []ImageAssociation) []imageAssocGroup {
	if len(assocs) == 0 {
		return nil
	}
	groups := make(map[string]*imageAssocGroup)
	var order []string
	for _, a := range assocs {
		key := fmt.Sprintf("%d-%d", a.StartTime, a.EndTime)
		if g, ok := groups[key]; ok {
			g.Images = append(g.Images, a)
		} else {
			groups[key] = &imageAssocGroup{
				StartTime: a.StartTime,
				EndTime:   a.EndTime,
				Phrase:    a.Phrase,
				Images:    []ImageAssociation{a},
			}
			order = append(order, key)
		}
	}
	result := make([]imageAssocGroup, 0, len(order))
	for _, k := range order {
		result = append(result, *groups[k])
	}
	return result
}

// formatTimestampWindow formats start and end time as MM:SS - MM:SS.
func formatTimestampWindow(start, end int) string {
	return fmt.Sprintf("%02d:%02d - %02d:%02d", start/60, start%60, end/60, end%60)
}

// chapterBoundaries splits a phrase into start and end boundaries.
func chapterBoundaries(phrase string) (start, end string) {
	parts := strings.SplitN(phrase, "\n", 2)
	start = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		end = strings.TrimSpace(parts[1])
	} else {
		end = start
	}
	return
}

// truncate cuts s to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
