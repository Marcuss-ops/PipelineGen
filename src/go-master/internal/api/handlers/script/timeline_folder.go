package script

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
)

// clipDriveRecord is a legacy struct for catalog compatibility
type clipDriveRecord struct {
	ID         string
	Name       string
	FolderID   string
	FolderPath string
	DriveLink  string
	Source     string
	MediaType  string
	Group      string
	Category   string
}

func loadClipsFromDB(ctx context.Context, repo *clips.Repository, sourceFilter string) ([]clipDriveRecord, error) {
	if repo == nil {
		return nil, nil
	}
	allClips, err := repo.ListClips(ctx, "")
	if err != nil {
		return nil, err
	}

	var records []clipDriveRecord
	for _, c := range allClips {
		if sourceFilter != "" && c.Source != sourceFilter && c.MediaType != sourceFilter {
			continue
		}
		records = append(records, clipDriveRecord{
			ID:         c.ID,
			Name:       c.Name,
			FolderID:   c.FolderID,
			FolderPath: c.FolderPath,
			DriveLink:  c.DriveLink,
			Source:     c.Source,
			MediaType:  c.MediaType,
			Group:      c.Group,
			Category:   c.Category,
		})
	}
	return records, nil
}

func pickClipDriveRecordLink(rec clipDriveRecord) string {
	return normalizeDriveFolderLink(rec.DriveLink, rec.FolderID)
}

func buildTimelineStockFolderCandidates(ctx context.Context, repo *clips.Repository, dataDir string) ([]timelineFolderCandidate, error) {
	if repo != nil {
		if records, err := loadClipsFromDB(ctx, repo, "stock"); err == nil && len(records) > 0 {
			return buildCandidatesFromRecords(records, "stock"), nil
		}
	}
	return loadCandidatesFromCatalog(dataDir)
}

func findDirectStockFolderCandidate(ctx context.Context, repo *clips.Repository, dataDir, topic, subject string) (*timelineFolderCandidate, bool, error) {
	folders, err := buildTimelineStockFolderCandidates(ctx, repo, dataDir)
	if err != nil {
		return nil, false, err
	}
	best, ok := directFolderMatch(folders, topic, subject)
	if !ok {
		return nil, false, nil
	}
	return &best, true, nil
}

func directFolderMatch(folders []timelineFolderCandidate, topic, subject string) (timelineFolderCandidate, bool) {
	focuses := []string{}
	subject = strings.TrimSpace(subject)
	topic = strings.TrimSpace(topic)
	if subject != "" {
		focuses = append(focuses, subject)
	} else if topic != "" {
		focuses = append(focuses, topic)
	}
	bestScore := 0
	bestDepth := -1
	var best timelineFolderCandidate

	for _, folder := range folders {
		name := normalizeAssociationKey(folder.Name)
		path := normalizeAssociationKey(folder.Path)
		link := normalizeDriveFolderLink(folder.Link, folder.FolderID)
		if name == "" && path == "" {
			continue
		}
		folderDepth := strings.Count(path, "/")
		for _, focus := range focuses {
			focus = normalizeAssociationKey(focus)
			if focus == "" {
				continue
			}
			score := 0
			switch {
			case name == focus:
				score = 300
			case path == focus:
				score = 280
			case strings.HasSuffix(path, "/"+focus):
				score = 260
			case strings.Contains(name, focus) && len(focus) >= 3:
				score = 220
			case strings.Contains(path, focus) && len(focus) >= 3:
				score = 200
			default:
				continue
			}
			if score > bestScore || (score == bestScore && folderDepth > bestDepth) {
				bestScore = score
				bestDepth = folderDepth
				best = folder
				best.Link = link
			}
		}
	}

	if bestScore == 0 {
		return timelineFolderCandidate{}, false
	}
	return best, true
}

func buildCandidatesFromRecords(records []clipDriveRecord, mediaType string) []timelineFolderCandidate {
	candidates := make([]timelineFolderCandidate, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, rec := range records {
		if mediaType == "stock" && strings.TrimSpace(rec.MediaType) != "stock" && strings.TrimSpace(rec.Source) != "stock" {
			continue
		}
		if mediaType == "stock" && strings.ToLower(strings.TrimSpace(rec.Category)) != "folder" {
			continue
		}
		path := getValidPath(rec)
		if path == "" {
			continue
		}
		name := filepath.Base(path)
		if name == "." || name == "/" || name == "" {
			name = path
		}
		link := getLink(rec)
		key := strings.ToLower(name + "|" + path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		candidates = append(candidates, timelineFolderCandidate{
			Name:     name,
			Path:     path,
			Link:     link,
			FolderID: strings.TrimSpace(rec.FolderID),
		})
	}
	return candidates
}

func getValidPath(rec clipDriveRecord) string {
	path := strings.TrimSpace(rec.FolderPath)
	if path == "" {
		path = strings.TrimSpace(rec.Group)
	}
	if path == "" {
		path = strings.TrimSpace(rec.Name)
	}
	return path
}

func getLink(rec clipDriveRecord) string {
	return normalizeDriveFolderLink(rec.DriveLink, rec.FolderID)
}

func loadCandidatesFromCatalog(dataDir string) ([]timelineFolderCandidate, error) {
	folders, err := loadStockFolderCatalog(dataDir)
	if err != nil {
		return nil, err
	}
	candidates := make([]timelineFolderCandidate, 0, len(folders))
	for _, folder := range folders {
		path := strings.TrimSpace(folder.StockPath())
		if path == "" {
			continue
		}
		name := filepath.Base(path)
		if name == "." || name == "/" {
			name = path
		}
		candidates = append(candidates, timelineFolderCandidate{
			Name:     name,
			Path:     path,
			Link:     strings.TrimSpace(folder.PickLink()),
			FolderID: strings.TrimSpace(folder.FolderID),
		})
	}
	return candidates, nil
}

func buildTimelineArtlistFolderCandidates(ctx context.Context, repo *clips.Repository, nodeScraperDir string) ([]timelineFolderCandidate, error) {
	candidates := make([]timelineFolderCandidate, 0)
	seenFolders := make(map[string]bool)

	if nodeScraperDir != "" {
		candidates = loadFromScraperDB(nodeScraperDir, candidates, seenFolders)
	}

	if repo != nil {
		if records, err := loadClipsFromDB(ctx, repo, "artlist"); err == nil {
			candidates = appendCandidatesFromRecords(records, candidates, seenFolders)
		}
	}

	return candidates, nil
}

func loadFromScraperDB(nodeScraperDir string, candidates []timelineFolderCandidate, seenFolders map[string]bool) []timelineFolderCandidate {
	dbPath := filepath.Join(nodeScraperDir, "artlist_videos.db")
	if _, err := os.Stat(dbPath); err != nil {
		return candidates
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		zap.L().Warn("Failed to open artlist_videos.db", zap.Error(err))
		return candidates
	}
	defer db.Close()

	rows, err := db.Query("SELECT name, drive_link, full_path FROM artlist_folders")
	if err != nil {
		zap.L().Warn("Failed to query artlist_videos.db", zap.Error(err))
		return candidates
	}
	defer rows.Close()

	for rows.Next() {
		var name, link, path string
		if err := rows.Scan(&name, &link, &path); err == nil {
			name = strings.TrimSpace(name)
			if name != "" && !seenFolders[name] {
				seenFolders[name] = true
				candidates = append(candidates, timelineFolderCandidate{
					Name:     name,
					Path:     path,
					Link:     link,
					FolderID: extractDriveFolderID(link),
				})
			}
		}
	}
	return candidates
}

func appendCandidatesFromRecords(records []clipDriveRecord, candidates []timelineFolderCandidate, seenFolders map[string]bool) []timelineFolderCandidate {
	for _, rec := range records {
		path := strings.TrimSpace(rec.FolderPath)
		if path == "" {
			continue
		}
		name := filepath.Base(path)
		if name == "." || name == "/" || name == "" {
			name = path
		}
		if !seenFolders[name] {
			seenFolders[name] = true
			candidates = append(candidates, timelineFolderCandidate{
				Name:     name,
				Path:     path,
				Link:     strings.TrimSpace(pickClipDriveRecordLink(rec)),
				FolderID: strings.TrimSpace(rec.FolderID),
			})
		}
	}
	return candidates
}
