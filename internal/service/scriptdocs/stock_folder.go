package scriptdocs

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/entityimages"
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
		imageFinder:          entityimages.New(),
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
		imageFinder:          entityimages.New(),
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

func normalizeLoose(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *ScriptDocService) isValidDriveFolder(ctx context.Context, folder StockFolder) bool {
	if strings.TrimSpace(folder.ID) == "" || folder.ID == "root" {
		return false
	}
	if s.driveClient == nil {
		return true
	}
	f, err := s.driveClient.GetFile(ctx, folder.ID)
	if err != nil || f == nil {
		return false
	}
	return f.MimeType == "application/vnd.google-apps.folder"
}

// resolveStockFolder finds the best matching Stock folder for a topic.
func (s *ScriptDocService) resolveStockFolder(topic string) StockFolder {
	ctx := context.Background()

	// 1. Try StockDB keyword search on full_path (source of truth for STOCK section)
	if s.stockDB != nil {
		folder, err := s.stockDB.FindFolderByTopicInSection(topic, "stock")
		if err == nil && folder != nil {
			logger.Info("Resolved Stock folder from StockDB section",
				zap.String("topic", topic),
				zap.String("folder", folder.FullPath),
			)
			candidate := StockFolder{
				ID:   folder.DriveID,
				Name: folder.FullPath,
				URL:  fmt.Sprintf("https://drive.google.com/drive/folders/%s", folder.DriveID),
			}
			if s.isValidDriveFolder(ctx, candidate) && !isGenericStockFolderName(candidate.Name) {
				return candidate
			}
			logger.Warn("StockDB(section) resolved stale/generic/non-folder Drive ID, skipping",
				zap.String("topic", topic),
				zap.String("folder_id", folder.DriveID),
				zap.String("folder_path", folder.FullPath),
			)
		}

		folder, err = s.stockDB.FindFolderByTopic(topic)
		if err == nil && folder != nil {
			logger.Info("Resolved Stock folder from StockDB",
				zap.String("topic", topic),
				zap.String("folder", folder.FullPath),
			)
			candidate := StockFolder{
				ID:   folder.DriveID,
				Name: folder.FullPath,
				URL:  fmt.Sprintf("https://drive.google.com/drive/folders/%s", folder.DriveID),
			}
			if s.isValidDriveFolder(ctx, candidate) && !isGenericStockFolderName(candidate.Name) {
				return candidate
			}
			logger.Warn("StockDB resolved stale/generic/non-folder Drive ID, skipping",
				zap.String("topic", topic),
				zap.String("folder_id", folder.DriveID),
				zap.String("folder_path", folder.FullPath),
			)
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
	topicLoose := normalizeLoose(topic)

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
		keywordLower := strings.ToLower(kf.keyword)
		keywordLoose := normalizeLoose(kf.keyword)
		folderLoose := normalizeLoose(kf.folder.Name)
		if strings.Contains(topicLower, keywordLower) ||
			(keywordLoose != "" && strings.Contains(topicLoose, keywordLoose)) ||
			(keywordLoose != "" && strings.Contains(keywordLoose, topicLoose)) ||
			(topicLoose != "" && folderLoose != "" && strings.Contains(folderLoose, topicLoose)) {
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
			if s.isValidDriveFolder(ctx, kf.folder) {
				return kf.folder
			}
			logger.Warn("Cache resolved stale/non-folder Drive ID, skipping",
				zap.String("topic", topic),
				zap.String("folder_id", kf.folder.ID),
				zap.String("folder_name", kf.folder.Name),
			)
		}
	}

	// 4. Ultimate fallback
	return StockFolder{
		ID:   "",
		Name: "None",
		URL:  "None",
	}
}

func isGenericStockFolderName(name string) bool {
	v := strings.ToLower(strings.TrimSpace(name))
	if v == "" {
		return true
	}
	generics := []string{
		"stock root",
		"stock",
		"clips",
		"artlist",
		"stock/artlist",
		"root",
	}
	for _, g := range generics {
		if v == g {
			return true
		}
	}
	return false
}
