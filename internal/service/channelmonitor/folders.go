package channelmonitor

import (
	"context"
	"fmt"
	"strings"

	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

const protagonistMergeThreshold = 0.88

var monitorDefaultCategories = []string{"Boxe", "Crime", "Discovery", "HipHop", "Music", "Various", "Wwe"}

var protagonistNoiseWords = map[string]struct{}{
	"official": {}, "video": {}, "audio": {}, "lyrics": {}, "lyric": {},
	"interview": {}, "interviews": {}, "highlights": {}, "highlight": {},
	"training": {}, "best": {}, "moment": {}, "moments": {}, "full": {},
	"fight": {}, "fights": {}, "compilation": {}, "analysis": {}, "reaction": {},
	"documentary": {}, "episode": {}, "podcast": {}, "news": {},
	"press": {}, "conference": {}, "weighin": {}, "weigh-in": {}, "faceoff": {},
	"vs": {}, "v": {}, "feat": {}, "ft": {},
}

// resolveFolder determines the Drive folder where clips for a video should be uploaded.
func (m *Monitor) resolveFolder(ctx context.Context, ch ChannelConfig, videoTitle string) (string, string, bool, CategoryDecision, error) {
	protagonist := strings.TrimSpace(ch.FolderName)
	if protagonist == "" {
		protagonist = extractProtagonist(videoTitle)
	}
	if protagonist == "" {
		protagonist = "Unknown"
	}

	category := ch.Category
	decision := CategoryDecision{
		Category:   category,
		Source:     "override",
		Confidence: 1.0,
	}
	if category == "" {
		classified, reason, err := m.classifyEntity(ctx, videoTitle, protagonist)
		if err != nil {
			logger.Warn("Ollama classification failed, using fallback category",
				zap.String("title", videoTitle),
				zap.Error(err),
			)
			category = fallbackCategory(videoTitle, protagonist)
			decision = CategoryDecision{
				Category:   category,
				Source:     "fallback",
				Reason:     reason,
				Confidence: classificationConfidence(category, "fallback"),
			}
		} else {
			category = classified
			decision = CategoryDecision{
				Category:   category,
				Source:     "gemma",
				Reason:     reason,
				Confidence: classificationConfidence(category, "gemma"),
			}
		}
	}
	if category == "" {
		category = "Various"
	}
	decision.Category = category
	decision.NeedsReview = decision.Confidence < 0.60 || category == "Various"
	if decision.Source == "override" && decision.Category != "" {
		decision.Confidence = 1.0
		decision.NeedsReview = false
	}

	canonicalCategory := fuzzyMatchFolder(category)
	if canonicalCategory == "" {
		canonicalCategory = category
	}
	decision.Category = canonicalCategory
	decision.NeedsReview = decision.NeedsReview || canonicalCategory == "Various"

	categoryFolderID, existed, err := m.getOrCreateCategoryFolder(ctx, canonicalCategory)
	if err != nil {
		return "", "", false, decision, fmt.Errorf("failed to get/create category folder: %w", err)
	}

	sanitizedName := sanitizeFolderName(protagonist)
	if sanitizedName == "" {
		sanitizedName = "Unknown"
	}

	chosenName := sanitizedName
	chosenID := ""

	if existingName, existingID, score, ok := m.findBestProtagonistFolder(ctx, categoryFolderID, sanitizedName); ok && score >= protagonistMergeThreshold {
		chosenName = existingName
		chosenID = existingID
		logger.Info("Reusing existing protagonist folder by fuzzy match",
			zap.String("requested", sanitizedName),
			zap.String("matched", existingName),
			zap.Float64("score", score),
			zap.String("folder_id", existingID),
		)
	}

	subfolderPath := canonicalCategory + "/" + chosenName

	if chosenID == "" {
		chosenID, err = m.driveClient.GetOrCreateFolder(ctx, chosenName, categoryFolderID)
		if err != nil {
			return "", "", false, decision, fmt.Errorf("failed to get/create subfolder %s: %w", subfolderPath, err)
		}
	}

	logger.Info("Folder resolved",
		zap.String("path", subfolderPath),
		zap.String("folder_id", chosenID),
		zap.Bool("category_existed", existed),
	)

	return subfolderPath, chosenID, existed, decision, nil
}

// getOrCreateCategoryFolder finds or creates a category folder under the root
func (m *Monitor) getOrCreateCategoryFolder(ctx context.Context, category string) (string, bool, error) {
	cacheKey := "Clips/" + category
	if folderID, ok := m.getCachedFolder(cacheKey); ok {
		return folderID, true, nil
	}

	clipRootID := m.config.ClipRootID
	if clipRootID == "" {
		var err error
		clipRootID, err = m.findClipRoot(ctx)
		if err != nil {
			return "", false, fmt.Errorf("Clips root folder not found: %w", err)
		}
	}

	result, err := m.driveClient.ListFolders(ctx, drive.ListFoldersOptions{
		ParentID: clipRootID,
		MaxDepth: 1,
		MaxItems: 100,
	})
	if err != nil {
		folderID, err := m.driveClient.CreateFolder(ctx, category, clipRootID)
		if err != nil {
			return "", false, fmt.Errorf("failed to create category folder: %w", err)
		}
		m.setCachedFolder(cacheKey, folderID)
		return folderID, false, nil
	}

	for _, f := range result {
		if strings.EqualFold(f.Name, category) {
			m.setCachedFolder(cacheKey, f.ID)
			return f.ID, true, nil
		}
	}

	folderID, err := m.driveClient.CreateFolder(ctx, category, clipRootID)
	if err != nil {
		return "", false, fmt.Errorf("failed to create category folder: %w", err)
	}
	m.setCachedFolder(cacheKey, folderID)

	return folderID, false, nil
}

// findClipRoot searches for the Clips root folder by name
func (m *Monitor) findClipRoot(ctx context.Context) (string, error) {
	result, err := m.driveClient.ListFoldersNoRecursion(ctx, drive.ListFoldersOptions{MaxItems: 100})
	if err != nil {
		return "", err
	}

	for _, f := range result {
		if strings.EqualFold(f.Name, "Clips") {
			m.config.ClipRootID = f.ID
			return f.ID, nil
		}
	}

	folderID, err := m.driveClient.CreateFolder(ctx, "Clips", "root")
	if err != nil {
		return "", fmt.Errorf("failed to create Clips root: %w", err)
	}
	m.config.ClipRootID = folderID
	return folderID, nil
}

func (m *Monitor) findClipRootNoCreate(ctx context.Context) (string, error) {
	result, err := m.driveClient.ListFoldersNoRecursion(ctx, drive.ListFoldersOptions{MaxItems: 100})
	if err != nil {
		return "", err
	}
	for _, f := range result {
		if strings.EqualFold(f.Name, "Clips") {
			return f.ID, nil
		}
	}
	return "", nil
}
