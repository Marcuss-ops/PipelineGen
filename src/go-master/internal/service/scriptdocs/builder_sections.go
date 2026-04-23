package scriptdocs

import (
	"fmt"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

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
