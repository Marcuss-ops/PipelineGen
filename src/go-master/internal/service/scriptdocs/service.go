// Package scriptdocs orchestrates script generation + entity extraction + clip association + Google Docs upload.
package scriptdocs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/util"

	"go.uber.org/zap"
)

// ArtlistClip represents an Artlist clip uploaded to Drive Stock.
type ArtlistClip struct {
	Name     string `json:"name"`
	Term     string `json:"term"`
	URL      string `json:"url"`
	Folder   string `json:"folder"`
	FolderID string `json:"folder_id"`
}

// ArtlistIndex holds all Artlist clips available for association.
type ArtlistIndex struct {
	FolderID  string               `json:"folder_id"`
	Clips     []ArtlistClip        `json:"clips"`
	CreatedAt string               `json:"created_at,omitempty"`
	ByTerm    map[string][]ArtlistClip `json:"-"`
}

// LoadArtlistIndex loads the Artlist clip index from JSON file.
func LoadArtlistIndex(path string) (*ArtlistIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read Artlist index: %w", err)
	}

	var idx ArtlistIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("failed to parse Artlist index: %w", err)
	}

	// Build ByTerm map
	idx.ByTerm = make(map[string][]ArtlistClip)
	for _, clip := range idx.Clips {
		idx.ByTerm[clip.Term] = append(idx.ByTerm[clip.Term], clip)
	}

	return &idx, nil
}

// Search searches the ArtlistIndex for clips matching the given terms.
func (idx *ArtlistIndex) Search(terms []string, maxResults int) []ArtlistClip {
	var results []ArtlistClip
	used := make(map[string]bool)

	for _, clip := range idx.Clips {
		if len(results) >= maxResults {
			break
		}
		if used[clip.URL] {
			continue
		}

		matched := false
		for _, term := range terms {
			t := strings.ToLower(term)
			if strings.Contains(strings.ToLower(clip.Name), t) || strings.Contains(strings.ToLower(clip.Term), t) {
				matched = true
				break
			}
		}

		if matched {
			results = append(results, clip)
			used[clip.URL] = true
		}
	}

	return results
}

// ScriptDocRequest represents the input for script document generation.
type ScriptDocRequest struct {
	Topic            string   `json:"topic" binding:"required"`
	Duration         int      `json:"duration"`
	Languages        []string `json:"languages"` // e.g. ["it", "es"] — default ["it"]
	Template         string   `json:"template"`  // "documentary", "storytelling", "top10", "biography"
	BoostKeywords    []string `json:"boost_keywords"`
	SuppressKeywords []string `json:"suppress_keywords"`
}

const (
	MinDuration      = 30
	MaxDuration      = 180
	DefaultDuration  = 80
	MaxLanguages     = 5

	TemplateDocumentary   = "documentary"
	TemplateStorytelling  = "storytelling"
	TemplateTop10         = "top10"
	TemplateBiography     = "biography"
)

// Validate checks request validity.
func (r *ScriptDocRequest) Validate() error {
	if strings.TrimSpace(r.Topic) == "" {
		return fmt.Errorf("topic is required")
	}

	if r.Duration == 0 {
		r.Duration = DefaultDuration
	}
	if r.Duration < MinDuration || r.Duration > MaxDuration {
		return fmt.Errorf("duration must be between %d and %d seconds", MinDuration, MaxDuration)
	}

	if len(r.Languages) == 0 {
		r.Languages = []string{"it"}
	}
	if len(r.Languages) > MaxLanguages {
		return fmt.Errorf("maximum %d languages allowed", MaxLanguages)
	}

	for _, lang := range r.Languages {
		if _, ok := LanguageInfo[lang]; !ok {
			return fmt.Errorf("unsupported language: %s (supported: it, en, es, fr, de, pt, ro)", lang)
		}
	}

	if r.Template == "" {
		r.Template = TemplateDocumentary
	}
	validTemplates := map[string]bool{
		TemplateDocumentary:  true,
		TemplateStorytelling: true,
		TemplateTop10:        true,
		TemplateBiography:    true,
	}
	if !validTemplates[r.Template] {
		return fmt.Errorf("invalid template: %s (valid: documentary, storytelling, top10, biography)", r.Template)
	}

	return nil
}

// LanguageInfo maps language code to display name and prompt language.
var LanguageInfo = map[string]struct {
	Name       string
	PromptLang string // how to tell Ollama to write
}{
	"it": {"Italiano", "italiano"},
	"en": {"English", "English"},
	"es": {"Español", "español"},
	"fr": {"Français", "français"},
	"de": {"Deutsch", "Deutsch"},
	"pt": {"Português", "português"},
	"ro": {"Română", "română"},
}

// LanguageResult holds the result for a single language.
type LanguageResult struct {
	Language         string            `json:"language"`
	FullText         string            `json:"full_text"`
	FrasiImportanti  []string          `json:"frasi_importanti"`
	NomiSpeciali     []string          `json:"nomi_speciali"`
	ParoleImportant  []string          `json:"parole_importanti"`
	EntitaConImmagine map[string]string `json:"entita_con_immagine,omitempty"`
	Associations     []ClipAssociation `json:"associations"`
}

// ScriptDocResult represents the output of the pipeline.
type ScriptDocResult struct {
	DocID          string            `json:"doc_id"`
	DocURL         string            `json:"doc_url"`
	Title          string            `json:"title"`
	Languages      []LanguageResult  `json:"languages"`
	StockFolder    string            `json:"stock_folder"`
	StockFolderURL string            `json:"stock_folder_url"`
}

// ClipAssociation represents a phrase-to-clip association.
type ClipAssociation struct {
	Phrase         string                  `json:"phrase"`
	Type           string                  `json:"type"` // "DYNAMIC", "STOCK_DB", "ARTLIST", or "STOCK"
	DynamicClip    *clipsearch.SearchResult `json:"dynamic_clip,omitempty"`
	Clip           *ArtlistClip            `json:"clip,omitempty"`
	ClipDB         *stockdb.StockClipEntry `json:"clip_db,omitempty"`
	Confidence     float64                 `json:"confidence"`
	MatchedKeyword string                  `json:"matched_keyword,omitempty"`
}

// ScriptDocService orchestrates the full pipeline.
type ScriptDocService struct {
	generator             *ollama.Generator
	docClient             *drive.DocClient
	artlistIndex          *ArtlistIndex
	artlistSrc            *clip.ArtlistSource
	artlistDB             *artlistdb.ArtlistDB
	stockDB               *stockdb.StockDB
	stockFolders          map[string]StockFolder
	stockFoldersMu        sync.RWMutex
	stockFoldersCacheTime time.Time
	stockFoldersCacheTTL  time.Duration
	driveClient           *drive.Client
	stockRootFolderID     string
	currentTemplate       string
	clipSearch            *clipsearch.Service
	dynamicClips          []clipsearch.SearchResult
	dynamicClipsMu        sync.Mutex
}

// StockFolder represents a Drive Stock folder.
type StockFolder struct {
	ID   string
	Name string // e.g., "Stock/Boxe/Andrewtate"
	URL  string
}

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

// GenerateScriptDoc runs the full pipeline (single or multi-language).
func (s *ScriptDocService) GenerateScriptDoc(ctx context.Context, req ScriptDocRequest) (*ScriptDocResult, error) {
	// Validate request
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Set current template for use during processing
	s.currentTemplate = req.Template

	logger.Info("Starting script doc pipeline",
		zap.String("topic", req.Topic),
		zap.Strings("languages", req.Languages),
		zap.String("template", req.Template),
		zap.Int("duration", req.Duration),
	)

	// Resolve Stock folder for this topic
	stockFolder := s.resolveStockFolder(req.Topic)

	// Generate script + extract entities + associate clips for each language
	// For multi-language, generate in parallel
	var langResults []LanguageResult
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error

	for _, lang := range req.Languages {
		info, ok := LanguageInfo[lang]
		if !ok {
			logger.Warn("Unsupported language, skipping", zap.String("lang", lang))
			continue
		}

		wg.Add(1)
		go func(lang string, info struct{ Name, PromptLang string }) {
			defer wg.Done()

			logger.Info("Generating script", zap.String("lang", lang), zap.String("topic", req.Topic))

			// 1. Generate script via Ollama in target language with retry
			fullText, err := s.generateScriptForLangWithRetry(ctx, req.Topic, req.Duration, info.PromptLang, 3)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("failed to generate script (%s): %w", lang, err)
				}
				mu.Unlock()
				return
			}

			// 2. Extract entities
			sentences := ExtractSentences(fullText)
			if len(sentences) == 0 {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("script too short for language %s: no meaningful sentences found", lang)
				}
				mu.Unlock()
				return
			}
			frasiImportanti := sentences[:util.Min(4, len(sentences))]
			nomiSpeciali := extractProperNouns(sentences)
			paroleImportant := extractKeywords(fullText)
			entitaConImmagine := extractEntitiesWithImages(sentences)

			// 2.5. Dynamic clip search for keywords extracted from phrases
			keywords := s.extractClipKeywords(frasiImportanti, nomiSpeciali, paroleImportant)
			if len(keywords) > 0 && s.clipSearch != nil {
				logger.Info("Starting dynamic clip search",
					zap.Strings("keywords", keywords),
				)
				dynamicClips, err := s.clipSearch.SearchClips(ctx, keywords)
				if err != nil {
					logger.Warn("Dynamic clip search failed", zap.Error(err))
				} else if len(dynamicClips) > 0 {
					s.dynamicClipsMu.Lock()
					s.dynamicClips = append(s.dynamicClips, dynamicClips...)
					s.dynamicClipsMu.Unlock()
					logger.Info("Dynamic clips found",
						zap.Int("count", len(dynamicClips)),
					)
				}
			}

			// 3. Associate clips to phrases (multilingual matching)
			associations := s.associateClips(frasiImportanti)

			result := LanguageResult{
				Language:          lang,
				FullText:          fullText,
				FrasiImportanti:   frasiImportanti,
				NomiSpeciali:      nomiSpeciali,
				ParoleImportant:   paroleImportant,
				EntitaConImmagine: entitaConImmagine,
				Associations:      associations,
			}

			mu.Lock()
			langResults = append(langResults, result)
			mu.Unlock()
		}(lang, info)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	if len(langResults) == 0 {
		return nil, fmt.Errorf("no languages were successfully generated")
	}

	// 4. Build document content with all languages
	content := s.buildMultilingualDocument(req.Topic, req.Duration, stockFolder, langResults)

	// 5. Create Google Doc with graceful degradation
	title := fmt.Sprintf("Script: %s (%s)", req.Topic, langNames(langResults))
	docID, docURL, err := s.createDocWithFallback(ctx, title, content)
	if err != nil {
		return nil, fmt.Errorf("failed to create document: %w", err)
	}

	result := &ScriptDocResult{
		DocID:          docID,
		DocURL:         docURL,
		Title:          title,
		Languages:      langResults,
		StockFolder:    stockFolder.Name,
		StockFolderURL: stockFolder.URL,
	}

	logger.Info("Script doc pipeline completed",
		zap.String("topic", req.Topic),
		zap.String("doc_id", docID),
		zap.Int("languages", len(langResults)),
	)

	return result, nil
}

// extractClipKeywords extracts meaningful keywords from phrases, names, and words
// for dynamic clip searching. Focuses on visual concepts: events, actions, objects.
func (s *ScriptDocService) extractClipKeywords(frasi []string, nomi []string, parole []string) []string {
	seen := make(map[string]bool)
	var keywords []string

	// Skip words that are too generic for clip searching
	skipWords := map[string]bool{
		"the": true, "and": true, "but": true, "his": true, "her": true,
		"with": true, "from": true, "that": true, "this": true, "was": true,
		"has": true, "have": true, "had": true, "were": true, "been": true,
		"also": true, "when": true, "than": true, "then": true, "after": true,
		"before": true, "during": true, "while": true, "where": true, "who": true,
		"which": true, "what": true, "how": true, "why": true, "very": true,
		"more": true, "most": true, "some": true, "any": true, "all": true,
		"each": true, "every": true, "both": true, "few": true, "many": true,
		"much": true, "other": true, "another": true, "such": true, "only": true,
	}

	// Skip short or generic proper nouns
	skipNames := map[string]bool{
		"he": true, "she": true, "it": true, "january": true, "february": true,
		"march": true, "april": true, "may": true, "june": true, "july": true,
		"august": true, "september": true, "october": true, "november": true,
		"december": true, "monday": true, "tuesday": true, "wednesday": true,
		"thursday": true, "friday": true, "saturday": true, "sunday": true,
	}

	// 1. Extract multi-word proper nouns first (highest priority for clips)
	for _, name := range nomi {
		lower := strings.ToLower(name)
		if skipNames[lower] || len(name) < 4 {
			continue
		}
		if !seen[lower] {
			seen[lower] = true
			keywords = append(keywords, name)
		}
	}

	// 2. Extract visual keywords from phrases (look for action/event words)
	for _, frase := range frasi {
		words := strings.Fields(frase)
		for _, word := range words {
			clean := strings.TrimFunc(word, func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r)
			})
			lower := strings.ToLower(clean)
			if len(clean) < 4 || skipWords[lower] {
				continue
			}
			// Prioritize nouns and action words (simplified heuristic)
			if unicode.IsUpper(rune(clean[0])) || strings.HasSuffix(lower, "ing") ||
				strings.HasSuffix(lower, "tion") || strings.HasSuffix(lower, "ment") {
				if !seen[lower] {
					seen[lower] = true
					keywords = append(keywords, clean)
				}
			}
		}
	}

	// 3. Add important single words as backup
	for _, parola := range parole {
		lower := strings.ToLower(parola)
		if skipWords[lower] || len(parola) < 5 {
			continue
		}
		if !seen[lower] {
			seen[lower] = true
			keywords = append(keywords, parola)
		}
	}

	// Limit to max 5 keywords for efficiency
	if len(keywords) > 5 {
		keywords = keywords[:5]
	}

	return keywords
}
