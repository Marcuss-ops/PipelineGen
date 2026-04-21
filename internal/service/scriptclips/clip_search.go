package scriptclips

import (
	"context"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/clipcache"
	"velox/go-master/internal/stock"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// findOrDownloadClip searches for a clip and downloads/uploads if not found
// PRIORITY 1: Check Google Drive Stock folders for existing clips
// PRIORITY 2: Check clip cache
// PRIORITY 3: Download from YouTube and upload to Drive
func (s *ScriptClipsService) findOrDownloadClip(ctx context.Context, entityName string) ClipMapping {
	// Skip generic Italian words
	if s.isGenericWord(entityName) {
		logger.Info("Skipping generic entity", zap.String("entity", entityName))
		return ClipMapping{
			Entity:        entityName,
			SearchQueryEN: entityName,
			ClipFound:     false,
			ClipStatus:    "skipped_generic",
			ErrorMessage:  "Skipped - generic word not suitable for YouTube search",
		}
	}

	// PRIORITY 1: Search Drive Stock folders for existing clips
	if existingClip := s.searchDriveForExistingClip(ctx, entityName); existingClip != nil {
		logger.Info("Found existing clip on Drive, skipping download",
			zap.String("entity", entityName),
			zap.String("clip_name", existingClip.Name),
			zap.String("drive_url", existingClip.Link),
		)
		return ClipMapping{
			Entity:        entityName,
			SearchQueryEN: entityName,
			ClipFound:     true,
			ClipStatus:    "found_on_drive",
			DriveURL:      existingClip.Link,
			DriveFileID:   existingClip.ID,
		}
	}

	// PRIORITY 2: Check clip cache
	if s.clipCache != nil {
		searchQueryEN := s.clipTranslator.TranslateQuery(entityName)
		cachedClip := s.clipCache.Search(searchQueryEN)
		if cachedClip != nil {
			logger.Info("Using cached clip",
				zap.String("entity", entityName),
				zap.String("cached_drive_url", cachedClip.DriveURL),
			)
			s.clipCache.UpdateUseCount(searchQueryEN)
			return ClipMapping{
				Entity:        entityName,
				SearchQueryEN: searchQueryEN,
				ClipFound:     true,
				ClipStatus:    "cache_hit",
				YouTubeURL:    cachedClip.URL,
				DriveURL:      cachedClip.DriveURL,
				DriveFileID:   cachedClip.DriveFileID,
			}
		}
	}

	// PRIORITY 3: No existing clip found, proceed with YouTube search + download
	logger.Info("No existing clip found on Drive, searching YouTube",
		zap.String("entity", entityName),
	)

	searchQueryEN := s.clipTranslator.TranslateQuery(entityName)

	mapping := ClipMapping{
		Entity:        entityName,
		SearchQueryEN: searchQueryEN,
		ClipFound:     false,
		ClipStatus:    "not_found",
	}

	// Search YouTube
	ytResults, err := s.stockManager.SearchYouTube(ctx, searchQueryEN, 10)
	if err != nil {
		mapping.ErrorMessage = fmt.Sprintf("YouTube search failed: %v", err)
		logger.Warn("YouTube search failed", zap.String("entity", entityName), zap.Error(err))
		return mapping
	}

	if len(ytResults) == 0 {
		logger.Info("No YouTube results for entity", zap.String("entity", entityName))
		return mapping
	}

	// Validate links with Ollama AI (if enabled)
	var approvedResults []stock.VideoResult
	if s.validateLinks && s.ollamaClient != nil {
		approvedResults, err = s.validateYouTubeLinks(ctx, entityName, ytResults)
		if err != nil {
			logger.Warn("Link validation failed, proceeding with all results",
				zap.String("entity", entityName), zap.Error(err))
			approvedResults = ytResults
		}
		if len(approvedResults) == 0 {
			logger.Info("No YouTube clips approved by AI", zap.String("entity", entityName))
			mapping.ErrorMessage = "No YouTube clips approved by AI validation"
			return mapping
		}
	} else {
		approvedResults = ytResults
	}

	// Download the best result
	bestResult := approvedResults[0]
	downloadPath, err := s.downloadVideo(ctx, bestResult.URL)
	if err != nil {
		mapping.ErrorMessage = fmt.Sprintf("Download failed: %v", err)
		logger.Warn("Clip download failed", zap.String("entity", entityName), zap.Error(err))
		return mapping
	}

	// Upload to Google Drive
	driveFileID, driveURL, err := s.uploadToDriveWithTopic(ctx, downloadPath, entityName, s.topic)
	if err != nil {
		mapping.ErrorMessage = fmt.Sprintf("Drive upload failed: %v", err)
		logger.Warn("Drive upload failed", zap.String("entity", entityName), zap.Error(err))
		return mapping
	}

	// Success
	mapping.ClipFound = true
	mapping.ClipStatus = "downloaded_and_uploaded"
	mapping.YouTubeURL = bestResult.URL
	mapping.DriveURL = driveURL
	mapping.DriveFileID = driveFileID

	// Cache the clip
	if s.clipCache != nil {
		s.clipCache.Store(&clipcache.CachedClip{
			SearchQuery:  searchQueryEN,
			VideoID:      bestResult.ID,
			Title:        bestResult.Title,
			URL:          bestResult.URL,
			DriveURL:     driveURL,
			DriveFileID:  driveFileID,
			Duration:     bestResult.Duration,
			ViewCount:    bestResult.ViewCount,
			DownloadedAt: time.Now().Unix(),
			LastUsedAt:   time.Now().Unix(),
			UseCount:     1,
		})
	}

	logger.Info("Clip downloaded and uploaded to Drive",
		zap.String("entity", entityName),
		zap.String("drive_url", mapping.DriveURL),
	)

	return mapping
}

// searchDriveForExistingClip cerca clip esistenti nelle cartelle Stock su Drive
func (s *ScriptClipsService) searchDriveForExistingClip(ctx context.Context, entityName string) *drive.File {
	if s.driveClient == nil || s.topic == "" {
		return nil
	}

	group := drive.DetectGroupFromTopic(s.topic)

	stockRootID, err := s.driveClient.GetOrCreateFolder(ctx, "Stock", "root")
	if err != nil {
		logger.Warn("Failed to resolve Stock root folder", zap.Error(err))
		return nil
	}

	groupFolderID, err := s.driveClient.GetOrCreateFolder(ctx, group, stockRootID)
	if err != nil {
		logger.Warn("Failed to resolve group folder", zap.String("group", group), zap.Error(err))
		return nil
	}

	topicFolder, err := s.driveClient.GetFolderByName(ctx, s.topic, groupFolderID)
	if err != nil {
		logger.Warn("Failed to resolve topic folder", zap.String("topic", s.topic), zap.Error(err))
		return nil
	}

	files, err := s.driveClient.SearchFiles(ctx, entityName, topicFolder.ID, true)
	if err != nil {
		logger.Warn("Failed to search Drive for existing clip",
			zap.String("entity", entityName), zap.String("folder", s.topic), zap.Error(err))
		return nil
	}

	if len(files) == 0 {
		return nil
	}

	logger.Info("Found existing clip on Drive",
		zap.String("entity", entityName),
		zap.String("clip_name", files[0].Name),
		zap.String("folder", s.topic),
	)

	return &files[0]
}

// isGenericWord checks if a word is too generic for YouTube search
func (s *ScriptClipsService) isGenericWord(word string) bool {
	lower := strings.ToLower(strings.TrimSpace(word))

	genericWords := map[string]bool{
		"il": true, "lo": true, "la": true, "i": true, "gli": true, "le": true,
		"un": true, "uno": true, "una": true, "di": true, "da": true, "con": true,
		"su": true, "per": true, "tra": true, "fra": true, "che": true, "chi": true,
		"cui": true, "quale": true, "è": true, "sono": true, "ha": true, "fare": true,
		"può": true, "essere": true, "avere": true, "dire": true, "questo": true,
		"questa": true, "quello": true, "quella": true, "e": true, "o": true,
		"ma": true, "non": true, "si": true, "no": true, "come": true, "dove": true,
		"quando": true, "molto": true, "più": true, "meno": true, "anche": true,
		"ancora": true, "già": true, "mai": true, "sempre": true, "tutto": true,
		"tutti": true, "tutte": true, "parte": true, "tipo": true, "cosa": true,
		"modo": true, "volta": true, "tempo": true,
		"the": true, "an": true, "is": true, "are": true, "was": true, "were": true,
		"be": true, "been": true, "and": true, "or": true, "but": true, "not": true,
		"this": true, "that": true, "these": true, "those": true, "with": true,
		"from": true, "for": true, "to": true, "very": true, "more": true,
		"most": true, "some": true, "part": true, "type": true, "way": true,
		"thing": true, "concept": true, "idea": true, "concetto": true,
	}

	if genericWords[lower] {
		return true
	}
	if len(lower) <= 2 {
		return true
	}

	return false
}
