// Package stocksync provides Drive-to-DB synchronization.
package stocksync

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/clipdb"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// DriveSync synchronizes Drive folders and clips to local DB
type DriveSync struct {
	driveClient *drive.Client
	stockDB     *stockdb.StockDB
	clipDB      *clipdb.ClipDB
	stockRootID string
	lastSync    time.Time
	mu          sync.Mutex
	running     bool
}

// NewDriveSync creates a new Drive sync service (backward compatible)
func NewDriveSync(driveClient *drive.Client, stockDB *stockdb.StockDB, stockRootID string) *DriveSync {
	return &DriveSync{
		driveClient: driveClient,
		stockDB:     stockDB,
		stockRootID: stockRootID,
	}
}

// NewDriveSyncWithClips creates a new Drive sync service with separate Stock and Clip DBs
func NewDriveSyncWithClips(driveClient *drive.Client, stockDB *stockdb.StockDB, clipDB *clipdb.ClipDB, stockRootID string) *DriveSync {
	return &DriveSync{
		driveClient: driveClient,
		stockDB:     stockDB,
		clipDB:      clipDB,
		stockRootID: stockRootID,
	}
}

// Sync performs a full Drive-to-DB synchronization
func (s *DriveSync) Sync(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("sync already running")
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	start := time.Now()
	logger.Info("Starting Drive-to-DB sync",
		zap.String("stock_root_id", s.stockRootID),
	)

	// Scan all folders from Drive recursively
	driveFolders, err := s.driveClient.ListFolders(ctx, drive.ListFoldersOptions{
		ParentID: s.stockRootID,
		MaxDepth: 3,
		MaxItems: 500,
	})
	if err != nil {
		return fmt.Errorf("failed to scan Drive folders: %w", err)
	}

	// Separate storage for stock and clips
	var stockFolders []stockdb.StockFolderEntry
	var clipFolders []clipdb.ClipFolder
	var stockClips []stockdb.StockClipEntry
	var clipClips []clipdb.ClipEntry

	for _, cat := range driveFolders {
		if strings.HasPrefix(cat.Name, ".") {
			continue
		}

		section := "stock"
		if s.isClipsCategory(cat.Name) {
			section = "clips"
		}

		// Category-level folder
		if section == "stock" {
			stockFolders = append(stockFolders, stockdb.StockFolderEntry{
				TopicSlug: normalizeSlug(fmt.Sprintf("%s-%s", section, cat.Name)),
				DriveID:   cat.ID,
				ParentID:  s.stockRootID,
				FullPath:  fmt.Sprintf("stock/%s", cat.Name),
				Section:   section,
			})
		} else {
			clipFolders = append(clipFolders, clipdb.ClipFolder{
				TopicSlug: normalizeSlug(fmt.Sprintf("%s-%s", section, cat.Name)),
				DriveID:   cat.ID,
				ParentID:  s.stockRootID,
				FullPath:  fmt.Sprintf("clips/%s", cat.Name),
			})
		}

		// Subfolders and clips
		for _, sub := range cat.Subfolders {
			if section == "stock" {
				stockFolders = append(stockFolders, stockdb.StockFolderEntry{
					TopicSlug: normalizeSlug(fmt.Sprintf("%s-%s-%s", section, cat.Name, sub.Name)),
					DriveID:   sub.ID,
					ParentID:  cat.ID,
					FullPath:  fmt.Sprintf("stock/%s/%s", cat.Name, sub.Name),
					Section:   section,
				})
			} else {
				clipFolders = append(clipFolders, clipdb.ClipFolder{
					TopicSlug: normalizeSlug(fmt.Sprintf("%s-%s-%s", section, cat.Name, sub.Name)),
					DriveID:   sub.ID,
					ParentID:  cat.ID,
					FullPath:  fmt.Sprintf("clips/%s/%s", cat.Name, sub.Name),
				})
			}

			// Get clips from this subfolder
			content, err := s.driveClient.GetFolderContent(ctx, sub.ID)
			if err != nil {
				logger.Warn("Failed to get folder content", zap.String("folder", sub.Name), zap.Error(err))
				continue
			}

			for _, file := range content.Files {
				if !isVideoFile(file.Name) {
					continue
				}

				clipEntry := clipdb.ClipEntry{
					ClipID:   file.ID,
					FolderID: sub.ID,
					Filename: file.Name,
					Source:   section,
					Tags:     strings.Join(generateTags(file.Name), " "),
					Duration: int(file.DurationMs / 1000),
					DriveURL: fmt.Sprintf("https://drive.google.com/file/d/%s/view?usp=drivesdk", file.ID),
				}

				if section == "stock" {
					stockClips = append(stockClips, stockdb.StockClipEntry{
						ClipID:   file.ID,
						FolderID: sub.ID,
						Filename: file.Name,
						Source:   section,
						Tags:     generateTags(file.Name),
						Duration: int(file.DurationMs / 1000),
					})
				} else {
					clipClips = append(clipClips, clipEntry)
				}
			}

			// Nested subfolders
			for _, sub2 := range sub.Subfolders {
				if section == "stock" {
					stockFolders = append(stockFolders, stockdb.StockFolderEntry{
						TopicSlug: normalizeSlug(fmt.Sprintf("%s-%s-%s-%s", section, cat.Name, sub.Name, sub2.Name)),
						DriveID:   sub2.ID,
						ParentID:  sub.ID,
						FullPath:  fmt.Sprintf("stock/%s/%s/%s", cat.Name, sub.Name, sub2.Name),
						Section:   section,
					})
				} else {
					clipFolders = append(clipFolders, clipdb.ClipFolder{
						TopicSlug: normalizeSlug(fmt.Sprintf("%s-%s-%s-%s", section, cat.Name, sub.Name, sub2.Name)),
						DriveID:   sub2.ID,
						ParentID:  sub.ID,
						FullPath:  fmt.Sprintf("clips/%s/%s/%s", cat.Name, sub.Name, sub2.Name),
					})
				}

				content2, err := s.driveClient.GetFolderContent(ctx, sub2.ID)
				if err != nil {
					continue
				}

				for _, file := range content2.Files {
					if !isVideoFile(file.Name) {
						continue
					}

					if section == "stock" {
						stockClips = append(stockClips, stockdb.StockClipEntry{
							ClipID:   file.ID,
							FolderID: sub2.ID,
							Filename: file.Name,
							Source:   section,
							Tags:     generateTags(file.Name),
							Duration: int(file.DurationMs / 1000),
						})
				} else {
					clipClips = append(clipClips, clipdb.ClipEntry{
						ClipID:   file.ID,
						FolderID: sub2.ID,
						Filename: file.Name,
						Source:   section,
						Tags:     strings.Join(generateTags(file.Name), " "),
						Duration: int(file.DurationMs / 1000),
						DriveURL: fmt.Sprintf("https://drive.google.com/file/d/%s/view?usp=drivesdk", file.ID),
					})
					}
				}
			}
		}
	}

	// Bulk upsert to StockDB
	if s.stockDB != nil && len(stockFolders) > 0 {
		if err := s.stockDB.BulkUpsertFolders(stockFolders); err != nil {
			logger.Warn("Failed to upsert stock folders", zap.Error(err))
		}
		if err := s.stockDB.BulkUpsertClips(stockClips); err != nil {
			logger.Warn("Failed to upsert stock clips", zap.Error(err))
		}
	}

	// Bulk upsert to ClipDB
	if s.clipDB != nil && len(clipFolders) > 0 {
		if err := s.clipDB.BulkUpsertFolders(clipFolders); err != nil {
			logger.Warn("Failed to upsert clip folders", zap.Error(err))
		}
		if err := s.clipDB.BulkUpsertClips(clipClips); err != nil {
			logger.Warn("Failed to upsert clips", zap.Error(err))
		}
	}

	s.lastSync = time.Now()

	logger.Info("Drive-to-DB sync completed",
		zap.Int("stock_folders", len(stockFolders)),
		zap.Int("stock_clips", len(stockClips)),
		zap.Int("clip_folders", len(clipFolders)),
		zap.Int("clips", len(clipClips)),
		zap.Duration("duration", time.Since(start)),
	)

	return nil
}

// GetLastSyncTime returns the last sync time
func (s *DriveSync) GetLastSyncTime() time.Time {
	return s.lastSync
}

// IsRunning returns true if sync is currently running
func (s *DriveSync) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Helper functions

func (s *DriveSync) isClipsCategory(name string) bool {
	clipsCategories := []string{"clips", "clip"}
	nameLower := strings.ToLower(name)
	for _, cat := range clipsCategories {
		if nameLower == cat {
			return true
		}
	}
	return false
}

func isVideoFile(filename string) bool {
	ext := strings.ToLower(filename)
	return strings.HasSuffix(ext, ".mp4") ||
		strings.HasSuffix(ext, ".mov") ||
		strings.HasSuffix(ext, ".avi") ||
		strings.HasSuffix(ext, ".mkv") ||
		strings.HasSuffix(ext, ".webm")
}

// generateTags extracts keyword tags from a filename, returning them as a slice.
func generateTags(filename string) []string {
	name := strings.ToLower(filename)
	name = strings.TrimSuffix(name, ".mp4")
	name = strings.TrimSuffix(name, ".mov")
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	return strings.Fields(name)
}

func normalizeSlug(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	var result string
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			result += string(c)
		}
	}
	return result
}
