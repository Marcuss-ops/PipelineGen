package scriptdocs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// ScanStockFolders dynamically scans the Drive Stock root folder and builds
// the keyword-to-folder mapping by discovering all subfolders recursively.
func ScanStockFolders(ctx context.Context, driveClient *drive.Client, stockRootFolderID string) (map[string]StockFolder, error) {
	folders, err := driveClient.ListFolders(ctx, drive.ListFoldersOptions{
		ParentID: stockRootFolderID,
		MaxDepth: 2, // Root → Category → Subfolder
		MaxItems: 200,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan Stock folders: %w", err)
	}

	result := make(map[string]StockFolder)

	// Build path tree from scanned folders
	for _, cat := range folders {
		// Skip system folders
		if strings.HasPrefix(cat.Name, ".") {
			continue
		}
		// Add category-level mapping
		catKey := strings.ToLower(cat.Name)
		result[catKey] = StockFolder{
			ID:   cat.ID,
			Name: fmt.Sprintf("Stock/%s", cat.Name),
			URL:  cat.Link,
		}

		// Scan subfolders
		for _, sub := range cat.Subfolders {
			subKey := strings.ToLower(sub.Name)
			pathName := fmt.Sprintf("Stock/%s/%s", cat.Name, sub.Name)
			result[subKey] = StockFolder{
				ID:   sub.ID,
				Name: pathName,
				URL:  sub.Link,
			}

			// Also add keyword variants (remove spaces, lowercase)
			cleanKey := strings.ReplaceAll(strings.ToLower(sub.Name), " ", "")
			if cleanKey != subKey {
				result[cleanKey] = StockFolder{
					ID:   sub.ID,
					Name: pathName,
					URL:  sub.Link,
				}
			}
		}
	}

	logger.Info("Stock folders scanned dynamically",
		zap.Int("total_mappings", len(result)),
	)

	return result, nil
}

// NewScriptDocService creates a new service with pre-loaded stock folders.
func NewScriptDocService(
	gen *ollama.Generator,
	dc *drive.DocClient,
	ai *ArtlistIndex,
	sdb *stockdb.StockDB,
	stockFolders map[string]StockFolder,
	cs *clipsearch.Service,
	alSrc *clip.ArtlistSource,
	alDB *artlistdb.ArtlistDB,
) *ScriptDocService {
	return &ScriptDocService{
		generator:            gen,
		docClient:            dc,
		artlistIndex:         ai,
		artlistSrc:           alSrc,
		artlistDB:            alDB,
		stockDB:              sdb,
		stockFolders:         stockFolders,
		stockFoldersCacheTTL: 24 * time.Hour,
		clipSearch:           cs,
	}
}

// NewScriptDocServiceWithDynamicFolders creates a service that dynamically scans Drive folders.
func NewScriptDocServiceWithDynamicFolders(
	gen *ollama.Generator,
	dc *drive.DocClient,
	driveClient *drive.Client,
	stockRootFolderID string,
	ai *ArtlistIndex,
	sdb *stockdb.StockDB,
	cs *clipsearch.Service,
	alSrc *clip.ArtlistSource,
	alDB *artlistdb.ArtlistDB,
) *ScriptDocService {
	svc := &ScriptDocService{
		generator:            gen,
		docClient:            dc,
		artlistIndex:         ai,
		artlistSrc:           alSrc,
		artlistDB:            alDB,
		stockDB:              sdb,
		driveClient:          driveClient,
		stockRootFolderID:    stockRootFolderID,
		stockFoldersCacheTTL: 24 * time.Hour,
		clipSearch:           cs,
	}

	// Try to scan folders on startup, but don't fail if it doesn't work
	if driveClient != nil && stockRootFolderID != "" {
		folders, err := ScanStockFolders(context.Background(), driveClient, stockRootFolderID)
		if err != nil {
			logger.Warn("Failed to scan Stock folders on startup, will retry on first use",
				zap.Error(err),
			)
		} else {
			svc.stockFolders = folders
			svc.stockFoldersCacheTime = time.Now()
		}
	}

	return svc
}

// resolveStockFolder finds the best matching Stock folder for a topic.
// Uses StockDB keyword search on full_path — no hardcoded IDs.
func (s *ScriptDocService) resolveStockFolder(topic string) StockFolder {
	// 1. Try StockDB keyword search on full_path
	if s.stockDB != nil {
		folder, err := s.stockDB.FindFolderByTopic(topic)
		if err == nil && folder != nil {
			logger.Info("Resolved Stock folder from StockDB",
				zap.String("topic", topic),
				zap.String("folder", folder.FullPath),
			)
			return StockFolder{
				ID:   folder.DriveID,
				Name: folder.FullPath,
				URL:  fmt.Sprintf("https://drive.google.com/drive/folders/%s", folder.DriveID),
			}
		}
	}

	// 2. Try in-memory cache
	s.stockFoldersMu.RLock()
	needRefresh := s.driveClient != nil && s.stockRootFolderID != "" &&
		(s.stockFolders == nil || time.Since(s.stockFoldersCacheTime) > s.stockFoldersCacheTTL)
	s.stockFoldersMu.RUnlock()

	if needRefresh {
		s.stockFoldersMu.Lock()
		folders, err := ScanStockFolders(context.Background(), s.driveClient, s.stockRootFolderID)
		if err != nil {
			logger.Warn("Failed to refresh Stock folders cache, using stale data",
				zap.Error(err),
			)
		} else {
			s.stockFolders = folders
			s.stockFoldersCacheTime = time.Now()

			// Also update DB if available
			if s.stockDB != nil {
				var dbFolders []stockdb.StockFolderEntry
				for keyword, folder := range folders {
					dbFolders = append(dbFolders, stockdb.StockFolderEntry{
						TopicSlug: keyword,
						DriveID:   folder.ID,
						ParentID:  "",
						FullPath:  folder.Name,
						Section:   "stock",
					})
				}
				s.stockDB.BulkUpsertFolders(dbFolders)
			}
		}
		s.stockFoldersMu.Unlock()
	}

	s.stockFoldersMu.RLock()
	defer s.stockFoldersMu.RUnlock()

	topicLower := strings.ToLower(topic)

	// 3. Try keyword match in cache (longest match first)
	type keywordFolder struct {
		keyword string
		folder  StockFolder
	}
	var sorted []keywordFolder
	for keyword, folder := range s.stockFolders {
		sorted = append(sorted, keywordFolder{keyword, folder})
	}
	// Sort by keyword length descending (longest match first)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if len(sorted[j].keyword) > len(sorted[i].keyword) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	for _, kf := range sorted {
		if strings.Contains(topicLower, kf.keyword) {
			// Register in DB for future instant lookup
			if s.stockDB != nil {
				s.stockDB.UpsertFolder(stockdb.StockFolderEntry{
					TopicSlug: stockdb.NormalizeSlug(kf.keyword),
					DriveID:   kf.folder.ID,
					ParentID:  "",
					FullPath:  kf.folder.Name,
					Section:   "stock",
				})
			}
			return kf.folder
		}
	}

	// 4. Fallback: Auto-create folder on Drive if client available
	if s.driveClient != nil && s.stockRootFolderID != "" {
		slug := stockdb.NormalizeSlug(topic)
		folderName := strings.Title(strings.ReplaceAll(slug, "-", " "))
		if folderName == "" {
			folderName = "Unknown"
		}

		// Try to create on Drive
		folderID, err := s.driveClient.CreateFolder(context.Background(), folderName, s.stockRootFolderID)
		if err == nil && folderID != "" {
			folderLink := fmt.Sprintf("https://drive.google.com/drive/folders/%s", folderID)
			newFolder := StockFolder{
				ID:   folderID,
				Name: fmt.Sprintf("Stock/%s", folderName),
				URL:  folderLink,
			}

			// Register in DB
			if s.stockDB != nil {
				s.stockDB.UpsertFolder(stockdb.StockFolderEntry{
					TopicSlug: slug,
					DriveID:   folderID,
					ParentID:  s.stockRootFolderID,
					FullPath:  newFolder.Name,
					Section:   "stock",
				})
			}

			// Add to cache
			s.stockFolders[slug] = newFolder

			logger.Info("Auto-created Stock folder for topic",
				zap.String("topic", topic),
				zap.String("folder", newFolder.Name),
			)

			return newFolder
		}
	}

	// 5. Ultimate fallback
	return StockFolder{
		ID:   "root",
		Name: "Stock",
		URL:  "https://drive.google.com/drive/u/0/my-drive",
	}
}
