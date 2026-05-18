package association

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/storage"

	"go.uber.org/zap"
)

func (s *Service) buildStockFolderCandidates(ctx context.Context) ([]FolderCandidate, error) {
	if s.stockRepo != nil {
		if records, err := s.loadClipsFromDB(ctx, s.stockRepo, "stock"); err == nil && len(records) > 0 {
			return s.buildCandidatesFromRecords(records, "stock"), nil
		}
	}
	return s.loadCandidatesFromCatalog()
}

func (s *Service) buildClipFolderCandidates(ctx context.Context) ([]FolderCandidate, error) {
	if s.clipsRepo == nil {
		return nil, nil
	}
	records, err := s.loadClipsFromDB(ctx, s.clipsRepo, "")
	if err != nil {
		return nil, err
	}
	return s.buildCandidatesFromRecords(records, ""), nil
}

func (s *Service) buildArtlistFolderCandidates(ctx context.Context) ([]FolderCandidate, error) {
	candidates := make([]FolderCandidate, 0)
	seenFolders := make(map[string]bool)

	if s.nodeScraperDir != "" {
		candidates = s.loadFromScraperDB(candidates, seenFolders)
	}

	if s.artlistRepo != nil {
		if records, err := s.loadClipsFromDB(ctx, s.artlistRepo, "artlist"); err == nil {
			candidates = s.appendCandidatesFromRecords(records, candidates, seenFolders)
		}
	}

	return candidates, nil
}

func (s *Service) loadClipsFromDB(ctx context.Context, repo *clips.Repository, sourceFilter string) ([]catalog.StockClipRef, error) {
	if repo == nil {
		return nil, nil
	}
	allClips, err := repo.ListClips(ctx, "")
	if err != nil {
		return nil, err
	}

	var records []catalog.StockClipRef
	for _, c := range allClips {
		if sourceFilter != "" && c.Source != sourceFilter && c.MediaType != sourceFilter {
			continue
		}
		records = append(records, catalog.StockClipRef{
			ClipID:     c.ID,
			Name:       c.Name,
			FolderID:   c.FolderID,
			FolderPath: c.FolderPath,
			DriveLink:  c.DriveLink,
			MediaType:  c.MediaType,
			Group:      c.Group,
		})
	}
	return records, nil
}

func (s *Service) buildCandidatesFromRecords(records []catalog.StockClipRef, mediaType string) []FolderCandidate {
	candidates := make([]FolderCandidate, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, rec := range records {
		if mediaType == "stock" && strings.TrimSpace(string(rec.MediaType)) != "stock" && strings.TrimSpace(rec.Group) != "stock" {
			// Also check Group as it's often used for source
			continue
		}

		path := strings.TrimSpace(rec.FolderPath)
		if path == "" {
			path = strings.TrimSpace(rec.Group)
		}
		if path == "" {
			path = strings.TrimSpace(rec.Name)
		}
		if path == "" {
			continue
		}
		name := filepath.Base(path)
		if name == "." || name == "/" || name == "" {
			name = path
		}

		link := strings.TrimSpace(rec.DriveLink)
		if link == "" && rec.FolderID != "" {
			link = "https://drive.google.com/drive/folders/" + rec.FolderID
		}

		key := strings.ToLower(name + "|" + path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		candidates = append(candidates, FolderCandidate{
			Name:     name,
			Path:     path,
			Link:     link,
			FolderID: strings.TrimSpace(rec.FolderID),
		})
	}
	return candidates
}

func (s *Service) loadCandidatesFromCatalog() ([]FolderCandidate, error) {
	folders, err := s.catalogRepo.LoadStockFolders()
	if err != nil {
		return nil, err
	}
	candidates := make([]FolderCandidate, 0, len(folders))
	for _, folder := range folders {
		path := strings.TrimSpace(folder.StockPath())
		if path == "" {
			continue
		}
		name := filepath.Base(path)
		if name == "." || name == "/" {
			name = path
		}
		candidates = append(candidates, FolderCandidate{
			Name:     name,
			Path:     path,
			Link:     strings.TrimSpace(folder.PickLink()),
			FolderID: strings.TrimSpace(folder.FolderID),
		})
	}
	return candidates, nil
}

func (s *Service) loadFromScraperDB(candidates []FolderCandidate, seenFolders map[string]bool) []FolderCandidate {
	dbPath := filepath.Join(s.nodeScraperDir, "artlist_videos.db")
	if _, err := os.Stat(dbPath); err != nil {
		return candidates
	}

	log := zap.L()
	sqliteDB, err := storage.OpenSQLiteDB(dbPath, log)
	if err != nil {
		log.Warn("Failed to open scraper DB", zap.String("path", dbPath), zap.Error(err))
		return candidates
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

	rows, err := db.Query("SELECT name, drive_link, full_path FROM artlist_folders")
	if err != nil {
		return candidates
	}
	defer rows.Close()

	for rows.Next() {
		var name, link, path string
		if err := rows.Scan(&name, &link, &path); err == nil {
			name = strings.TrimSpace(name)
			if name != "" && !seenFolders[name] {
				seenFolders[name] = true
				candidates = append(candidates, FolderCandidate{
					Name:     name,
					Path:     path,
					Link:     link,
					FolderID: s.extractDriveFolderID(link),
				})
			}
		}
	}
	return candidates
}

func (s *Service) appendCandidatesFromRecords(records []catalog.StockClipRef, candidates []FolderCandidate, seenFolders map[string]bool) []FolderCandidate {
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

			link := strings.TrimSpace(rec.DriveLink)
			if link == "" && rec.FolderID != "" {
				link = "https://drive.google.com/drive/folders/" + rec.FolderID
			}

			candidates = append(candidates, FolderCandidate{
				Name:     name,
				Path:     path,
				Link:     link,
				FolderID: strings.TrimSpace(rec.FolderID),
			})
		}
	}
	return candidates
}

func (s *Service) extractDriveFolderID(link string) string {
	if strings.Contains(link, "folders/") {
		parts := strings.Split(link, "folders/")
		if len(parts) > 1 {
			id := parts[1]
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return strings.TrimSpace(id)
		}
	}
	return ""
}
