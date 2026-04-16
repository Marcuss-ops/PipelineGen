// Package clip provides clip suggestion functionality using NLP.
package clip

import (
	"context"
	"sort"
	"strings"
	"time"

	"velox/go-master/internal/nlp"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)


// Suggester provides clip suggestions based on title/script matching.
// It now uses the in-memory clip index for instant suggestions (sub-ms),
// falling back to live Drive scan only if the index is unavailable.
type Suggester struct {
	driveClient *drive.Client
	rootFolderID string
	indexer     *Indexer           // Optional: indexer reference (always gets latest index)
}

// NewSuggester creates a new clip suggester
func NewSuggester(driveClient *drive.Client, rootFolderID string) *Suggester {
	return &Suggester{
		driveClient:  driveClient,
		rootFolderID: rootFolderID,
	}
}

// SetIndexer sets the clip indexer for fast in-memory suggestions.
// The indexer is used as a reference to always get the latest index.
func (s *Suggester) SetIndexer(indexer *Indexer) {
	s.indexer = indexer
}

// SetIndex sets a snapshot of the clip index for fast suggestions.
// Deprecated: Use SetIndexer instead to always get the latest index.
func (s *Suggester) SetIndex(index *ClipIndex) {
	// Create a dummy indexer with the provided index
	// This is kept for backward compatibility
	s.indexer = &Indexer{}
	s.indexer.SetIndex(index)
}

// SuggestClips suggests clips based on title and optional script
// mediaType filters results to "clip" or "stock" (empty = no filter)
func (s *Suggester) SuggestClips(ctx context.Context, title, script, group string, maxResults int, minScore float64, mediaType string) ([]Suggestion, error) {
	startTime := time.Now()

	// Extract keywords from title
	titleKeywords := nlp.ExtractKeywords(title, 10)
	logger.Debug("Extracted keywords from title",
		zap.String("title", title),
		zap.Int("keyword_count", len(titleKeywords)))

	// Also extract from script if provided
	var scriptKeywords []nlp.Keyword
	if script != "" {
		scriptKeywords = nlp.ExtractKeywords(script, 15)
	}

	var suggestions []Suggestion
	usedIndex := false

	// Try to use in-memory index first (instant, no Drive API calls)
	if s.indexer != nil {
		index := s.indexer.GetIndex()
		if index != nil && len(index.Clips) > 0 {
			usedIndex = true
			suggestions = s.suggestFromIndex(index, titleKeywords, scriptKeywords, group, minScore, mediaType)
			logger.Debug("Suggestions from index",
				zap.Int("indexed_clips", len(index.Clips)),
				zap.Int("suggestions", len(suggestions)))
		}
	}

	// Fallback: live Drive scan (only if index is not available)
	if !usedIndex && s.driveClient != nil {
		var err error
		suggestions, err = s.suggestFromDrive(ctx, titleKeywords, scriptKeywords, group, minScore)
		if err != nil {
			return nil, err
		}
	}

	// Sort by score descending
	sortSuggestions(suggestions)

	// Limit results
	if maxResults > 0 && len(suggestions) > maxResults {
		suggestions = suggestions[:maxResults]
	}

	logger.Info("Clip suggestions generated",
		zap.String("title", title),
		zap.Int("total_suggestions", len(suggestions)),
		zap.Duration("duration", time.Since(startTime)))

	return suggestions, nil
}

// suggestFromIndex uses the in-memory clip index for fast suggestions (sub-ms)
func (s *Suggester) suggestFromIndex(index *ClipIndex, titleKeywords, scriptKeywords []nlp.Keyword, group string, minScore float64, mediaType string) []Suggestion {
	var suggestions []Suggestion

	// Build folder map from index for folder name matching
	folderMap := make(map[string]Folder)
	for _, folder := range index.Folders {
		folderMap[folder.ID] = Folder{
			ID:   folder.ID,
			Name: folder.Name,
			Path: folder.Path,
		}
	}

	// Filter clips by group and media type if specified
	var clipsToScore []IndexedClip
	for _, clip := range index.Clips {
		if group != "" && !strings.EqualFold(clip.Group, group) {
			continue
		}
		if mediaType != "" && !strings.EqualFold(clip.MediaType, mediaType) {
			continue
		}
		clipsToScore = append(clipsToScore, clip)
	}

	// Score each clip
	for _, indexedClip := range clipsToScore {
		// Convert IndexedClip to Clip for compatibility with calculateRelevance
		clip := Clip{
			ID:         indexedClip.ID,
			Name:       indexedClip.Name,
			Filename:   indexedClip.Filename,
			Duration:   indexedClip.Duration,
			Resolution: indexedClip.Resolution,
			Width:      indexedClip.Width,
			Height:     indexedClip.Height,
			DriveLink:  indexedClip.DriveLink,
			Size:       indexedClip.Size,
			MimeType:   indexedClip.MimeType,
			FolderID:   indexedClip.FolderID,
			FolderName: indexedClip.FolderPath,
		}

		// Get folder for folder name matching
		folder := folderMap[indexedClip.FolderID]
		if folder.ID == "" {
			// Derive folder name from path
			parts := strings.Split(indexedClip.FolderPath, "/")
			if len(parts) > 0 {
				folder.Name = parts[len(parts)-1]
			}
		}

		score, matchType, matchTerms := s.calculateRelevance(clip, folder, titleKeywords, scriptKeywords)
		if score >= minScore {
			suggestions = append(suggestions, Suggestion{
				Clip:       clip,
				Score:      score,
				MatchType:  matchType,
				MatchTerms: matchTerms,
			})
		}
	}

	return suggestions
}

// suggestFromDrive uses live Drive scan for suggestions (slow — fallback)
func (s *Suggester) suggestFromDrive(ctx context.Context, titleKeywords, scriptKeywords []nlp.Keyword, group string, minScore float64) ([]Suggestion, error) {
	// Determine which folder to search in based on group
	searchFolderID := s.rootFolderID
	if group != "" {
		// Try to find group folder
		groupFolder, err := s.findGroupFolder(ctx, group)
		if err == nil && groupFolder != "" {
			searchFolderID = groupFolder
		}
	}

	// Get all folders and their clips
	folders, err := s.getAllFoldersAndClips(ctx, searchFolderID)
	if err != nil {
		return nil, err
	}

	// Calculate relevance for each clip
	var suggestions []Suggestion
	for _, folder := range folders {
		for _, clip := range folder.Clips {
			score, matchType, matchTerms := s.calculateRelevance(clip, folder, titleKeywords, scriptKeywords)

			if score >= minScore {
				suggestions = append(suggestions, Suggestion{
					Clip:       clip,
					Score:      score,
					MatchType:  matchType,
					MatchTerms: matchTerms,
				})
			}
		}
	}

	return suggestions, nil
}

// findGroupFolder tries to find a folder matching the group name
func (s *Suggester) findGroupFolder(ctx context.Context, group string) (string, error) {
	// Map group IDs to folder names
	groupName := ""
	for _, g := range ClipGroups {
		if g.ID == group {
			groupName = g.Name
			break
		}
	}

	if groupName == "" {
		groupName = group // Use as-is if not found
	}

	// Search for folder
	folder, err := s.driveClient.GetFolderByName(ctx, groupName, s.rootFolderID)
	if err != nil {
		return "", err
	}

	return folder.ID, nil
}

// getAllFoldersAndClips recursively gets all folders and their clips
func (s *Suggester) getAllFoldersAndClips(ctx context.Context, parentID string) ([]Folder, error) {
	var folders []Folder

	opts := drive.ListFoldersOptions{
		ParentID: parentID,
		MaxDepth: 3,
		MaxItems: 100,
	}

	driveFolders, err := s.driveClient.ListFolders(ctx, opts)
	if err != nil {
		return nil, err
	}

	for _, df := range driveFolders {
		folder := Folder{
			ID:         df.ID,
			Name:       df.Name,
			Link:       df.Link,
			ParentID:   parentID,
			Subfolders: []Folder{},
		}

		// Get clips in this folder
		content, err := s.driveClient.GetFolderContent(ctx, df.ID)
		if err != nil {
			logger.Warn("Failed to get folder content", zap.Error(err))
			continue
		}

		for _, file := range content.Files {
			// Only include video files
			if IsVideoFile(file.MimeType, file.Name) {
				clip := Clip{
					ID:         file.ID,
					Name:       CleanClipName(file.Name),
					Filename:   file.Name,
					DriveLink:  file.Link,
					Size:       file.Size,
					MimeType:   file.MimeType,
					ModifiedAt: file.ModifiedTime,
					FolderID:   df.ID,
					FolderName: df.Name,
				}
				folder.Clips = append(folder.Clips, clip)
			}
		}

		folder.ClipCount = len(folder.Clips)
		folders = append(folders, folder)

		// Recursively get subfolders
		if len(df.Subfolders) > 0 {
			subfolders, err := s.getAllFoldersAndClips(ctx, df.ID)
			if err != nil {
				logger.Warn("Failed to scan subfolder tree",
					zap.String("folder_id", df.ID),
					zap.String("folder_name", df.Name),
					zap.Error(err))
			} else {
				folders = append(folders, subfolders...)
			}
		}
	}

	return folders, nil
}

// calculateRelevance calculates the relevance score between a clip and keywords
func (s *Suggester) calculateRelevance(clip Clip, folder Folder, titleKeywords, scriptKeywords []nlp.Keyword) (float64, string, []string) {
	var score float64
	var matchType string
	var matchTerms []string

	// Normalize strings for comparison
	clipNameLower := strings.ToLower(clip.Name)
	folderNameLower := strings.ToLower(folder.Name)
	filenameLower := strings.ToLower(clip.Filename)

	// Check folder name match (high weight)
	for _, kw := range titleKeywords {
		kwLower := strings.ToLower(kw.Word)
		if strings.Contains(folderNameLower, kwLower) {
			score += kw.Score * 2.0
			matchTerms = append(matchTerms, kw.Word)
			matchType = "folder_name"
		}
	}

	// Check clip name match (medium weight)
	for _, kw := range titleKeywords {
		kwLower := strings.ToLower(kw.Word)
		if strings.Contains(clipNameLower, kwLower) || strings.Contains(filenameLower, kwLower) {
			score += kw.Score * 1.5
			if matchType == "" {
				matchType = "file_name"
			}
			if !containsString(matchTerms, kw.Word) {
				matchTerms = append(matchTerms, kw.Word)
			}
		}
	}

	// Check script keywords (lower weight but broader)
	for _, kw := range scriptKeywords {
		kwLower := strings.ToLower(kw.Word)
		if strings.Contains(folderNameLower, kwLower) ||
			strings.Contains(clipNameLower, kwLower) ||
			strings.Contains(filenameLower, kwLower) {
			score += kw.Score * 0.8
			if matchType == "" {
				matchType = "content"
			}
			if !containsString(matchTerms, kw.Word) {
				matchTerms = append(matchTerms, kw.Word)
			}
		}
	}

	// Bonus for exact matches
	for _, kw := range titleKeywords {
		kwLower := strings.ToLower(kw.Word)
		if clipNameLower == kwLower || folderNameLower == kwLower {
			score += 1.0
		}
	}

	// Normalize score to 0-100 range
	score = score * 10
	if score > 100 {
		score = 100
	}

	if matchType == "" {
		matchType = "none"
	}

	return score, matchType, matchTerms
}


// sortSuggestions sorts suggestions by score descending
func sortSuggestions(suggestions []Suggestion) {
	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Score > suggestions[j].Score
	})
}