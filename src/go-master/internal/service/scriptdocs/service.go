// Package scriptdocs orchestrates script generation + entity extraction + clip association + Google Docs upload.
package scriptdocs

import (
	"context"
	"fmt"
	"sync"

	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/util"

	"go.uber.org/zap"
)

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
