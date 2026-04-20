package scriptdocs

import (
	"context"
	"fmt"
	"strings"
)

const (
	AssociationModeDefault     = "default"
	AssociationModeFullArtlist = "fullartlist"
)

func normalizeAssociationMode(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", AssociationModeDefault:
		return AssociationModeDefault
	case "full_artlist", "full-artlist", AssociationModeFullArtlist:
		return AssociationModeFullArtlist
	default:
		return mode
	}
}

func filterAssociationsByMode(associations []ClipAssociation, mode string) []ClipAssociation {
	mode = normalizeAssociationMode(mode)
	if mode != AssociationModeFullArtlist {
		return associations
	}
	filtered := make([]ClipAssociation, 0, len(associations))
	for _, assoc := range associations {
		if assoc.Type == "ARTLIST" {
			filtered = append(filtered, assoc)
		}
	}
	return filtered
}

func formatTimestampWindow(startSec, endSec int) string {
	if startSec < 0 {
		startSec = 0
	}
	if endSec < startSec {
		endSec = startSec
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", startSec/60, startSec%60, endSec/60, endSec%60)
}

func (s *ScriptDocService) buildArtlistTimeline(associations []ClipAssociation, duration int) []ArtlistTimeline {
	if len(associations) == 0 {
		return nil
	}
	type item struct {
		term     string
		folderID string
	}
	seen := make(map[string]bool)
	seenFolder := make(map[string]bool)
	ordered := make([]item, 0, len(associations))
	hasDownloadedForTerm := func(term string) bool {
		if s.artlistDB == nil {
			return true
		}
		clips, ok := s.artlistDB.GetDownloadedClipsForTerm(term)
		return ok && len(clips) > 0
	}
	folderHasFilesCache := make(map[string]bool)
	folderHasFiles := func(folderID string) bool {
		folderID = strings.TrimSpace(folderID)
		if folderID == "" {
			return false
		}
		if ok, cached := folderHasFilesCache[folderID]; cached {
			return ok
		}
		// If Drive client is unavailable, trust DB-level filter.
		if s.driveClient == nil {
			folderHasFilesCache[folderID] = true
			return true
		}
		content, err := s.driveClient.GetFolderContent(context.Background(), folderID)
		if err != nil || content == nil {
			folderHasFilesCache[folderID] = false
			return false
		}
		ok := content.TotalFiles > 0
		folderHasFilesCache[folderID] = ok
		return ok
	}
	for _, assoc := range associations {
		if assoc.Type != "ARTLIST" || assoc.Clip == nil {
			continue
		}
		term := normalizeKeyword(assoc.MatchedKeyword)
		if term == "" {
			term = normalizeKeyword(assoc.Clip.Term)
		}
		if term == "" || seen[term] || !hasDownloadedForTerm(term) {
			continue
		}
		folderID := strings.TrimSpace(assoc.Clip.FolderID)
		if folderID == "" {
			if clips := s.artlistClipsForTerm(term); len(clips) > 0 {
				for _, c := range clips {
					if strings.TrimSpace(c.FolderID) != "" {
						folderID = strings.TrimSpace(c.FolderID)
						break
					}
				}
			}
		}
		if folderID != "" {
			if seenFolder[folderID] {
				continue
			}
			if !folderHasFiles(folderID) {
				continue
			}
			seenFolder[folderID] = true
		}
		seen[term] = true
		ordered = append(ordered, item{term: term, folderID: folderID})
		if len(ordered) >= 8 {
			break
		}
	}
	if len(ordered) == 0 {
		return nil
	}

	resolveFolderID := func(term string, existing string) string {
		if existing != "" {
			return existing
		}
		if s.artlistIndex != nil {
			if clips := s.artlistClipsForTerm(term); len(clips) > 0 {
				for _, c := range clips {
					if strings.TrimSpace(c.FolderID) != "" {
						return strings.TrimSpace(c.FolderID)
					}
				}
			}
			if strings.TrimSpace(s.artlistIndex.FolderID) != "" {
				return strings.TrimSpace(s.artlistIndex.FolderID)
			}
		}
		return ""
	}

	n := len(ordered)
	if duration <= 0 {
		duration = DefaultDuration
	}
	out := make([]ArtlistTimeline, 0, n)
	for i, it := range ordered {
		start := duration * i / n
		end := duration * (i + 1) / n
		folderID := resolveFolderID(it.term, it.folderID)
		label := it.term
		if len(label) > 0 {
			label = strings.ToUpper(label[:1]) + label[1:]
		}
		folderName := "Stock/Artlist/" + label
		folderURL := ""
		if folderID != "" {
			folderURL = "https://drive.google.com/drive/folders/" + folderID
		}
		out = append(out, ArtlistTimeline{
			Timestamp:  formatTimestampWindow(start, end),
			Keyword:    it.term,
			FolderName: folderName,
			FolderURL:  folderURL,
		})
	}
	return out
}
