package catalogsync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/artlistsync"
	"velox/go-master/internal/catalogdb"
	"velox/go-master/internal/clipdb"
	"velox/go-master/internal/stockdb"
)

// Sync bridges legacy JSON-backed sources into the unified SQLite catalog.
type Sync struct {
	catalog          *catalogdb.CatalogDB
	stockDB          *stockdb.StockDB
	clipDB           *clipdb.ClipDB
	artlistIndexPath string

	mu       sync.Mutex
	running  bool
	lastSync time.Time
}

func New(catalog *catalogdb.CatalogDB, stockDB *stockdb.StockDB, clipDB *clipdb.ClipDB, artlistIndexPath string) *Sync {
	return &Sync{
		catalog:          catalog,
		stockDB:          stockDB,
		clipDB:           clipDB,
		artlistIndexPath: artlistIndexPath,
	}
}

func (s *Sync) Sync(ctx context.Context) error {
	_ = ctx
	if s == nil || s.catalog == nil {
		return nil
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("catalog sync already running")
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.lastSync = time.Now().UTC()
		s.mu.Unlock()
	}()

	var all []catalogdb.Clip
	var stockIDs []string
	var clipIDs []string
	var artlistIDs []string

	if s.stockDB != nil {
		entries, err := s.stockDB.GetAllClips()
		if err == nil {
			folders, _ := s.stockDB.GetAllFolders()
			folderMap := make(map[string]stockdb.StockFolderEntry, len(folders))
			for _, folder := range folders {
				folderMap[folder.DriveID] = folder
			}
			for _, entry := range entries {
				folder := folderMap[entry.FolderID]
				clip := catalogdb.Clip{
					ID:           fmt.Sprintf("%s:%s", catalogdb.SourceStockDrive, entry.ClipID),
					Source:       catalogdb.SourceStockDrive,
					SourceID:     entry.ClipID,
					Provider:     "google_drive_stock",
					Title:        entry.Filename,
					Filename:     entry.Filename,
					Category:     folder.Section,
					FolderID:     entry.FolderID,
					FolderPath:   folder.FullPath,
					DriveFileID:  entry.ClipID,
					ExternalPath: joinPath(folder.FullPath, entry.Filename),
					Tags:         entry.Tags,
					DurationSec:  entry.Duration,
					LastSyncedAt: time.Now().UTC(),
					IsActive:     true,
				}
				all = append(all, clip)
				stockIDs = append(stockIDs, entry.ClipID)
			}
		}
	}

	if s.clipDB != nil {
		entries := s.clipDB.GetAllClips()
		for _, entry := range entries {
			clip := catalogdb.Clip{
				ID:           fmt.Sprintf("%s:%s", catalogdb.SourceClipDrive, entry.ClipID),
				Source:       catalogdb.SourceClipDrive,
				SourceID:     entry.ClipID,
				Provider:     "google_drive_clips",
				Title:        entry.Filename,
				Filename:     entry.Filename,
				Category:     entry.Source,
				FolderID:     entry.FolderID,
				DriveFileID:  entry.ClipID,
				DriveURL:     entry.DriveURL,
				LocalPath:    entry.LocalPath,
				ExternalPath: entry.Filename,
				Tags:         entry.Tags,
				DurationSec:  entry.Duration,
				LastSyncedAt: time.Now().UTC(),
				IsActive:     true,
			}
			all = append(all, clip)
			clipIDs = append(clipIDs, entry.ClipID)
		}
	}

	if s.artlistIndexPath != "" {
		data, err := os.ReadFile(s.artlistIndexPath)
		if err == nil {
			var index artlistsync.ArtlistIndex
			if err := json.Unmarshal(data, &index); err == nil {
				for _, entry := range index.Clips {
					meta, _ := json.Marshal(map[string]interface{}{
						"term":      entry.Term,
						"folder":    entry.Folder,
						"folder_id": entry.FolderID,
					})
					clip := catalogdb.Clip{
						ID:           fmt.Sprintf("%s:%s", catalogdb.SourceArtlist, entry.FileID),
						Source:       catalogdb.SourceArtlist,
						SourceID:     entry.FileID,
						Provider:     "artlist_drive",
						Title:        entry.Name,
						Filename:     entry.Name,
						Category:     entry.Term,
						FolderID:     entry.FolderID,
						FolderPath:   entry.Folder,
						DriveFileID:  entry.FileID,
						DriveURL:     entry.URL,
						ExternalPath: joinPath(entry.Folder, entry.Name),
						Tags:         inferTags(entry.Term, entry.Name, entry.Folder),
						LastSyncedAt: time.Now().UTC(),
						IsActive:     true,
						MetadataJSON: string(meta),
					}
					all = append(all, clip)
					artlistIDs = append(artlistIDs, entry.FileID)
				}
			}
		}
	}

	if err := s.catalog.BulkUpsertClips(all); err != nil {
		return err
	}
	_ = s.catalog.MarkSourceMissing(catalogdb.SourceStockDrive, stockIDs)
	_ = s.catalog.MarkSourceMissing(catalogdb.SourceClipDrive, clipIDs)
	_ = s.catalog.MarkSourceMissing(catalogdb.SourceArtlist, artlistIDs)
	_ = s.catalog.UpsertSyncState(catalogdb.SyncState{
		Source:         "catalog_bridge_sync",
		LastFullScanAt: time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	})
	return nil
}

func joinPath(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	return filepath.ToSlash(filepath.Join(clean...))
}

func inferTags(parts ...string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, part := range parts {
		part = strings.ToLower(part)
		part = strings.ReplaceAll(part, "/", " ")
		part = strings.ReplaceAll(part, "_", " ")
		part = strings.ReplaceAll(part, "-", " ")
		for _, token := range strings.Fields(part) {
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			out = append(out, token)
		}
	}
	return out
}
