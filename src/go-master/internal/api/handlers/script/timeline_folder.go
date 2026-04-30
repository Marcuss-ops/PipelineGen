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

func buildTimelineStockFolderCandidates(ctx context.Context, repo *clips.Repository, dataDir string) ([]timelineFolderCandidate, error) {
	if repo != nil {
		if records, err := loadClipsFromDB(ctx, repo, "stock"); err == nil && len(records) > 0 {
			return buildCandidatesFromRecords(records, "stock"), nil
		}
	}
	return loadCandidatesFromCatalog(dataDir)
}

func buildCandidatesFromRecords(records []clipDriveRecord, mediaType string) []timelineFolderCandidate {
	candidates := make([]timelineFolderCandidate, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, rec := range records {
		if mediaType == "stock" && strings.TrimSpace(rec.MediaType) != "stock" && strings.TrimSpace(rec.Source) != "stock" {
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
		candidates = append(candidates, timelineFolderCandidate{Name: name, Path: path, Link: link})
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
	link := strings.TrimSpace(rec.DriveLink)
	if link == "" && strings.TrimSpace(rec.FolderID) != "" {
		link = "https://drive.google.com/drive/folders/" + strings.TrimSpace(rec.FolderID)
	}
	return link
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
			Name: name,
			Path: path,
			Link: strings.TrimSpace(folder.PickLink()),
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
		if records, err := loadClipsFromDB(ctx, repo, ""); err == nil {
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
				candidates = append(candidates, timelineFolderCandidate{Name: name, Path: path, Link: link})
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
				Name: name,
				Path: path,
				Link: strings.TrimSpace(pickClipDriveRecordLink(rec)),
			})
		}
	}
	return candidates
}
