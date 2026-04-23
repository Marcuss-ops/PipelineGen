package scriptdocs

import (
	"fmt"
	"strings"
)

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

			folderName := ""
			folderURL := ""
			clipName := ""

			switch sourceKind {
			case "stock", "dynamic":
				if strings.TrimSpace(assoc.Type) != "" {
					folderName, folderURL, clipName = s.resolveStockAssociationFolder(assoc, stockFolder)
					if strings.Contains(strings.ToLower(folderName), "artlist") {
						continue
					}
					if strings.TrimSpace(folderName) == "" && strings.TrimSpace(folderURL) == "" {
						folderName, folderURL = s.resolveStockFolderForChapterText(chapter.SourceText, stockFolder)
					}
				} else {
					folderName, folderURL = s.resolveStockFolderForChapterText(chapter.SourceText, stockFolder)
				}
			default:
				folderName, folderURL = s.resolveStockFolderForChapterText(chapter.SourceText, stockFolder)
			}

			startPhrase, endPhrase := chapterBoundaries(chapter.SourceText)

			if strings.TrimSpace(folderName) == "" && strings.TrimSpace(folderURL) == "" {
				folderName = stockFolder.Name
				folderURL = stockFolder.URL
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

func (s *ScriptDocService) resolveStockFolderForChapterText(text string, fallback StockFolder) (folderName, folderURL string) {
	if s == nil || s.stockDB == nil {
		return fallback.Name, fallback.URL
	}

	sentences := ExtractSentences(text)
	candidates := make([]string, 0, 8)
	seen := make(map[string]bool)

	for _, noun := range ExtractProperNouns(sentences) {
		key := strings.ToLower(strings.TrimSpace(noun))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		candidates = append(candidates, noun)
	}

	for _, token := range strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ' ', '-', '_', '/', ':', ',', '.', ';', '(', ')':
			return true
		default:
			return false
		}
	}) {
		token = strings.TrimSpace(token)
		if len(token) < 3 {
			continue
		}
		key := strings.ToLower(token)
		if seen[key] {
			continue
		}
		seen[key] = true
		candidates = append(candidates, token)
	}

	for _, candidate := range candidates {
		folder := s.resolveStockFolder(candidate)
		if strings.TrimSpace(folder.ID) == "" {
			continue
		}
		if isGenericStockFolderName(folder.Name) {
			continue
		}
		return folder.Name, folder.URL
	}

	if strings.TrimSpace(fallback.Name) != "" || strings.TrimSpace(fallback.URL) != "" {
		return fallback.Name, fallback.URL
	}
	return "", ""
}
