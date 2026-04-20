package scriptdocs

import "strings"

func (s *ScriptDocService) splitAssociationsBySource(associations []ClipAssociation) ([]ClipAssociation, []ClipAssociation) {
	stock := make([]ClipAssociation, 0, len(associations))
	artlist := make([]ClipAssociation, 0, len(associations))
	for _, assoc := range associations {
		switch s.associationSourceKind(assoc) {
		case "stock":
			stock = append(stock, assoc)
		case "artlist":
			artlist = append(artlist, assoc)
		}
	}
	return stock, artlist
}

func (s *ScriptDocService) associationSourceKind(assoc ClipAssociation) string {
	switch assoc.Type {
	case "ARTLIST":
		return "artlist"
	case "STOCK_DB", "STOCK", "STOCK_FOLDER":
		if assoc.ClipDB != nil {
			if kind := normalizeSourceKind(assoc.ClipDB.Source); kind != "" {
				return kind
			}
			if s != nil && s.stockDB != nil && strings.TrimSpace(assoc.ClipDB.FolderID) != "" {
				if folder, err := s.stockDB.FindFolderByDriveID(assoc.ClipDB.FolderID); err == nil && folder != nil {
					if strings.Contains(strings.ToLower(folder.FullPath), "artlist") {
						return "artlist"
					}
					switch strings.ToLower(strings.TrimSpace(folder.Section)) {
					case "clips":
						return "artlist"
					case "stock":
						return "stock"
					}
				}
			}
		}
		if assoc.StockFolder != nil && strings.Contains(strings.ToLower(assoc.StockFolder.Name), "artlist") {
			return "artlist"
		}
		return "stock"
	case "DYNAMIC":
		if assoc.DynamicClip != nil {
			if kind := normalizeSourceKind(assoc.DynamicClip.Source); kind != "" {
				return kind
			}
			if assoc.DynamicClip.Folder != "" && strings.Contains(strings.ToLower(assoc.DynamicClip.Folder), "artlist") {
				return "artlist"
			}
			if s != nil && s.stockDB != nil {
				folderID := assoc.DynamicClip.FolderID
				if folderID == "" {
					folderID = assoc.DynamicClip.Folder
				}
				if folderID != "" {
					if folder, err := s.stockDB.FindFolderByDriveID(folderID); err == nil && folder != nil {
						if strings.Contains(strings.ToLower(folder.FullPath), "artlist") {
							return "artlist"
						}
						switch strings.ToLower(strings.TrimSpace(folder.Section)) {
						case "clips":
							return "artlist"
						case "stock":
							return "stock"
						}
					}
				}
			}
		}
		return "dynamic"
	default:
		if assoc.Clip != nil {
			return "artlist"
		}
		return ""
	}
}

func normalizeSourceKind(raw string) string {
	switch raw {
	case "artlist":
		return "artlist"
	case "stock":
		return "stock"
	case "dynamic", "dynamic_job":
		return "dynamic"
	default:
		return ""
	}
}
