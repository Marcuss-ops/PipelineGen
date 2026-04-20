package handlers

import (
	"fmt"
	"sort"
	"strings"
)

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
	driveAssocs []DriveFolderAssoc,
	frasi []string,
	nomi []string,
	parole []string,
	entitaImmagini []EntityImage,
	translations []Translation,
) string {
	var content strings.Builder
	today := "15/04/2026"
	langUpper := strings.ToUpper(lang)
	if langUpper == "" {
		langUpper = "EN"
	}

	// Header
	content.WriteString(fmt.Sprintf("# %s\n\n", title))
	content.WriteString(fmt.Sprintf("**Topic:** %s | **Durata:** %d:%02d | %s\n", topic, duration/60, duration%60, today))
	content.WriteString("====================================================================================================\n\n")

	// 1. ARTLIST SECTION - mostra sempre la sezione; fallback a None quando non configurata
	content.WriteString("🎬 ASSOCIAZIONI ARTLIST\n\n")
	if h.artlistDriveFolderID != "" {
		content.WriteString("   📁 Artlist Library\n")
		content.WriteString(fmt.Sprintf("   🔗 https://drive.google.com/drive/folders/%s\n\n", h.artlistDriveFolderID))
	} else {
		content.WriteString("   - None\n\n")
	}
	content.WriteString("====================================================================================================\n\n")

	// 2. STOCK DRIVE SECTION - mostra sempre la sezione; fallback a None quando non c'è alcun folder linkabile
	if stockFolderID != "" {
		folderName := stockFolderName
		if folderName == "" {
			folderName = topic
		}
		content.WriteString("📦 STOCK DRIVE\n\n")
		content.WriteString(fmt.Sprintf("   📁 %s\n", folderName))
		content.WriteString(fmt.Sprintf("   🔗 https://drive.google.com/drive/folders/%s\n\n", stockFolderID))
		content.WriteString("====================================================================================================\n\n")
	} else if h.stockRootFolder != "" {
		content.WriteString("📦 STOCK DRIVE (ROOT)\n\n")
		content.WriteString("   📁 Stock Root\n")
		content.WriteString(fmt.Sprintf("   🔗 https://drive.google.com/drive/folders/%s\n\n", h.stockRootFolder))
		content.WriteString("====================================================================================================\n\n")
	} else {
		content.WriteString("📦 STOCK DRIVE\n\n")
		content.WriteString("   - None\n\n")
		content.WriteString("====================================================================================================\n\n")
	}

	// 3. DRIVE CLIPS SECTION - mostra sempre la sezione; fallback a None quando non ci sono cartelle trovate
	content.WriteString("📂 DRIVE CLIPS\n\n")
	if len(driveAssocs) > 0 {
		seenFolders := make(map[string]bool)
		for _, assoc := range driveAssocs {
			if seenFolders[assoc.FolderURL] {
				continue
			}
			content.WriteString(fmt.Sprintf("   📁 %s\n", assoc.FolderName))
			content.WriteString(fmt.Sprintf("   🔗 %s\n\n", assoc.FolderURL))
			seenFolders[assoc.FolderURL] = true
		}
	} else {
		content.WriteString("   - None\n\n")
	}
	content.WriteString("====================================================================================================\n\n")

	// 4. ARTLIST CLIPS SECTION - render top 6 matched Artlist clips by score
	content.WriteString("🎞️ ARTLIST CLIPS\n\n")

	type scoredArtlist struct {
		Phrase string
		Clip   ArtlistClipRef
	}
	var allScored []scoredArtlist
	for _, assoc := range artlistAssocs {
		for _, c := range assoc.Clips {
			if strings.TrimSpace(c.URL) == "" {
				continue
			}
			allScored = append(allScored, scoredArtlist{Phrase: assoc.Phrase, Clip: c})
		}
	}

	// Sort globally by score descending
	sort.SliceStable(allScored, func(i, j int) bool {
		return allScored[i].Clip.Score > allScored[j].Clip.Score
	})

	artlistCount := 0
	fmt.Printf("DEBUG: Formatting ARTLIST clips. Total found: %d. Capping at 6.\n", len(allScored))
	for _, item := range allScored {
		c := item.Clip
		artlistCount++
		content.WriteString("   💬 " + item.Phrase + "\n")
		content.WriteString(fmt.Sprintf("   🎬 %s\n", c.Name))

		sourceInfo := ""
		if c.Source != "" {
			sourceInfo = fmt.Sprintf(" [%s]", c.Source)
		}
		scoreInfo := ""
		if c.Score > 0 {
			scoreInfo = fmt.Sprintf(" (Score: %.1f)", c.Score)
		}

		content.WriteString(fmt.Sprintf("   🔗 %s%s%s\n\n", c.URL, sourceInfo, scoreInfo))
		if artlistCount >= 6 {
			fmt.Printf("DEBUG: Reached cap of 6 clips. Breaking.\n")
			break
		}
	}
	if artlistCount == 0 {
		content.WriteString("   - None\n\n")
	}
	content.WriteString("====================================================================================================\n\n")

	// SCRIPT SECTION
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

	// ENTITIES SECTION
	if len(frasi) > 0 || len(nomi) > 0 || len(parole) > 0 || len(entitaImmagini) > 0 {
		content.WriteString(fmt.Sprintf("🔍 ENTITÀ ESTRATTE (%s)\n\n", langUpper))

		if len(frasi) > 0 {
			content.WriteString(fmt.Sprintf("📌 FRASI IMPORTANTI (%d)\n", len(frasi)))
			for i, fr := range frasi {
				content.WriteString(fmt.Sprintf("   %d. %s\n", i+1, fr))
			}
			content.WriteString("\n")
		}

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

		if len(entitaImmagini) > 0 {
			content.WriteString(fmt.Sprintf("🖼️ ENTITÀ CON IMMAGINE (%d)\n", len(entitaImmagini)))
			for _, ent := range entitaImmagini {
				content.WriteString(fmt.Sprintf("   🖼️ %s → %s\n", ent.Entity, ent.ImageURL))
			}
			content.WriteString("\n")
		}
		content.WriteString("====================================================================================================\n\n")
	}

	return content.String()
}
