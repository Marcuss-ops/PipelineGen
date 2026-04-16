package channelmonitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// resolveFolder determines the Drive folder where clips for a video should be uploaded.
// It extracts the protagonist from the title, classifies the entity via Ollama,
// finds or creates the matching category folder, then creates/finds the subfolder.
// Returns: folderPath (e.g. "HipHop/ArtistName"), folderID, folderExisted, error.
func (m *Monitor) resolveFolder(ctx context.Context, ch ChannelConfig, videoTitle string) (string, string, bool, error) {
	// Step 1: Extract protagonist name from title
	protagonist := extractProtagonist(videoTitle)
	if protagonist == "" {
		protagonist = "Unknown"
	}

	// Step 2: Classify entity via Ollama to determine category
	category := ch.Category
	if category == "" {
		classified, err := m.classifyEntity(ctx, videoTitle)
		if err != nil {
			logger.Warn("Ollama classification failed, using default category",
				zap.String("title", videoTitle),
				zap.Error(err),
			)
			category = "HipHop"
		} else {
			category = classified
		}
	}

	// Step 3: Fuzzy match category to known categories
	canonicalCategory := fuzzyMatchFolder(category)
	if canonicalCategory == "" {
		canonicalCategory = category // use as-is if no match
	}

	// Step 4: Find or create the category folder under Stock root
	categoryFolderID, existed, err := m.getOrCreateCategoryFolder(ctx, canonicalCategory)
	if err != nil {
		return "", "", false, fmt.Errorf("failed to get/create category folder: %w", err)
	}

	// Step 5: Sanitize and create/find the protagonist subfolder
	sanitizedName := sanitizeFolderName(protagonist)
	subfolderPath := canonicalCategory + "/" + sanitizedName

	subfolderID, err := m.driveClient.GetOrCreateFolder(ctx, sanitizedName, categoryFolderID)
	if err != nil {
		return "", "", false, fmt.Errorf("failed to get/create subfolder %s: %w", subfolderPath, err)
	}

	logger.Info("Folder resolved",
		zap.String("path", subfolderPath),
		zap.String("folder_id", subfolderID),
		zap.Bool("category_existed", existed),
	)

	return subfolderPath, subfolderID, existed, nil
}

// classifyEntity uses Ollama to classify a video title into a category
func (m *Monitor) classifyEntity(ctx context.Context, title string) (string, error) {
	prompt := fmt.Sprintf(`Classify the following YouTube video title into one of these categories: Boxe, Crime, Discovery, HipHop, Music, Wwe.
Reply with ONLY the category name.

Title: "%s"

Category:`, title)

	reqBody := map[string]interface{}{
		"model":  "gemma3:4b",
		"prompt": prompt,
		"stream": false,
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", m.ollamaURL+"/api/generate", bytes.NewReader(reqJSON))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	var ollamaResp struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	category := strings.TrimSpace(ollamaResp.Response)
	// Clean up the response - remove quotes, periods, etc.
	category = strings.Trim(category, `"'.,;: `)
	
	logger.Debug("Ollama classification result",
		zap.String("title", title),
		zap.String("category", category),
	)

	return category, nil
}

// fuzzyMatchFolder maps a category string to a known canonical category
func fuzzyMatchFolder(category string) string {
	normalized := strings.ToLower(strings.TrimSpace(category))
	
	// Direct lookup in knownCategories map
	if canonical, ok := knownCategories[normalized]; ok {
		return canonical
	}

	// Partial matching
	for key, canonical := range knownCategories {
		if strings.Contains(normalized, key) || strings.Contains(key, normalized) {
			return canonical
		}
	}

	// Check if it already matches a canonical category name
	for _, canonical := range knownCategories {
		if strings.EqualFold(category, canonical) {
			return canonical
		}
	}

	return ""
}

// getOrCreateCategoryFolder finds or creates a category folder under the Stock root
func (m *Monitor) getOrCreateCategoryFolder(ctx context.Context, category string) (string, bool, error) {
	// Check cache first
	cacheKey := "Stock/" + category
	if folderID, ok := m.folderCache[cacheKey]; ok {
		return folderID, true, nil
	}

	// Get or create Stock root
	stockRootID := m.config.StockRootID
	if stockRootID == "" {
		// Try to find Stock root by name
		var err error
		stockRootID, err = m.findStockRoot(ctx)
		if err != nil {
			return "", false, fmt.Errorf("Stock root folder not found: %w", err)
		}
	}

	// Search for existing category folder
	query := fmt.Sprintf("name='%s' and '%s' in parents and trashed=false and mimeType='application/vnd.google-apps.folder'",
		category, stockRootID)

	result, err := m.driveClient.ListFolders(ctx, drive.ListFoldersOptions{
		ParentID: stockRootID,
		MaxDepth: 1,
		MaxItems: 100,
	})
	if err != nil {
		// Fallback: create the folder
		folderID, err := m.driveClient.CreateFolder(ctx, category, stockRootID)
		if err != nil {
			return "", false, fmt.Errorf("failed to create category folder: %w", err)
		}
		m.folderCache[cacheKey] = folderID
		return folderID, false, nil
	}

	// Look for matching folder
	for _, f := range result {
		if strings.EqualFold(f.Name, category) {
			m.folderCache[cacheKey] = f.ID
			return f.ID, true, nil
		}
	}

	// Create the folder
	folderID, err := m.driveClient.CreateFolder(ctx, category, stockRootID)
	if err != nil {
		return "", false, fmt.Errorf("failed to create category folder: %w", err)
	}
	m.folderCache[cacheKey] = folderID

	return folderID, false, nil
}

// findStockRoot searches for the Stock root folder by name
func (m *Monitor) findStockRoot(ctx context.Context) (string, error) {
	// Search root level for "Stock" folder
	result, err := m.driveClient.ListFoldersNoRecursion(ctx, drive.ListFoldersOptions{
		MaxItems: 100,
	})
	if err != nil {
		return "", err
	}

	for _, f := range result {
		if strings.EqualFold(f.Name, "Stock") {
			m.config.StockRootID = f.ID
			return f.ID, nil
		}
	}

	// Try to create it
	folderID, err := m.driveClient.CreateFolder(ctx, "Stock", "root")
	if err != nil {
		return "", fmt.Errorf("failed to create Stock root: %w", err)
	}
	m.config.StockRootID = folderID
	return folderID, nil
}

// extractProtagonist extracts the main subject/person name from a video title
func extractProtagonist(title string) string {
	// Remove common prefixes and patterns
	cleaned := title

	// Remove stuff in parentheses and brackets
	re := regexp.MustCompile(`[\(\[\{][^\)\]\}]*[\)\]\}]`)
	cleaned = re.ReplaceAllString(cleaned, "")

	// Remove common patterns
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(official\s+(music\s+)?video|lyrics?\s*video|audio|ft\.?\s+\w+|feat\.?\s+.+?\.?)\b`),
		regexp.MustCompile(`(?i)\|(?:\s*official|\s*audio|\s*video|\s*lyrics?)?\s*$`),
		regexp.MustCompile(`(?i)\b(video|film|clip|episode|interview)\b`),
	}

	for _, p := range patterns {
		cleaned = p.ReplaceAllString(cleaned, "")
	}

	// Remove special characters but keep spaces
	cleaned = regexp.MustCompile(`[^a-zA-Z0-9\s'&]`).ReplaceAllString(cleaned, "")

	// Collapse whitespace
	cleaned = regexp.MustCompile(`\s+`).ReplaceAllString(cleaned, " ")
	cleaned = strings.TrimSpace(cleaned)

	// Try to extract name patterns
	// Pattern: "Name - Something" or "Name: Something"
	if idx := strings.Index(cleaned, " - "); idx > 0 {
		name := strings.TrimSpace(cleaned[:idx])
		if isValidName(name) {
			return name
		}
	}
	if idx := strings.Index(cleaned, ":"); idx > 0 {
		name := strings.TrimSpace(cleaned[:idx])
		if isValidName(name) {
			return name
		}
	}

	// Pattern: "Name vs Name" or "Name and Name"
	vsPatterns := regexp.MustCompile(`(?i)\b(?:vs\.?|and|&)\b`)
	if vsPatterns.MatchString(cleaned) {
		parts := vsPatterns.Split(cleaned, 2)
		if len(parts) == 2 {
			name := strings.TrimSpace(parts[0])
			if isValidName(name) {
				return name
			}
		}
	}

	// If title is short enough, use it as-is
	words := strings.Fields(cleaned)
	if len(words) <= 4 {
		return cleaned
	}

	// Try to extract a proper noun (capitalized word or phrase)
	// Look for the first capitalized phrase
	re2 := regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)*)`)
	matches := re2.FindAllString(cleaned, -1)
	if len(matches) > 0 {
		// Return the longest match (likely the full name)
		longest := ""
		for _, m := range matches {
			if len(m) > len(longest) {
				longest = m
			}
		}
		if isValidName(longest) {
			return longest
		}
	}

	// Fallback: first few words
	if len(words) >= 2 {
		return strings.Join(words[:2], " ")
	}
	if len(words) == 1 {
		return words[0]
	}

	return cleaned
}

// isValidName checks if a string looks like a valid name
func isValidName(name string) bool {
	if len(name) < 2 || len(name) > 50 {
		return false
	}
	// Should have at least one capitalized word
	words := strings.Fields(name)
	for _, w := range words {
		if len(w) > 0 && w[0] >= 'A' && w[0] <= 'Z' {
			return true
		}
	}
	return false
}

// sanitizeFolderName removes invalid characters from folder names for Google Drive
func sanitizeFolderName(name string) string {
	// Google Drive forbidden characters: < > : " / \ | ? *
	re := regexp.MustCompile(`[<>:"/\\|?*]`)
	cleaned := re.ReplaceAllString(name, "")

	// Also remove control characters
	cleaned = regexp.MustCompile(`[\x00-\x1f]`).ReplaceAllString(cleaned, "")

	// Trim whitespace
	cleaned = strings.TrimSpace(cleaned)

	// Replace multiple spaces with single space
	cleaned = regexp.MustCompile(`\s+`).ReplaceAllString(cleaned, " ")

	// Limit length (Drive has 255 char limit for names)
	if len(cleaned) > 100 {
		cleaned = cleaned[:100]
	}

	if cleaned == "" {
		return "Unnamed"
	}

	return cleaned
}
