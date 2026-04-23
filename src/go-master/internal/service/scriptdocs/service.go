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
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Initialize Open-World Semantic Registry
	if s.semanticRegistry != nil {
		if err := s.semanticRegistry.Initialize(ctx); err != nil {
			logger.Warn("Failed to initialize semantic registry", zap.Error(err))
		}
	}

	s.currentTemplate = req.Template
	s.currentAssociationMode = req.AssociationMode

	logger.Info("Starting script doc pipeline",
		zap.String("topic", req.Topic),
		zap.Strings("languages", req.Languages),
		zap.String("template", req.Template),
		zap.Int("duration", req.Duration),
	)

	stockFolder := s.resolveStockFolder(req.Topic)

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

			fullText, err := s.generateScriptForLangWithRetry(ctx, req.Topic, req.Duration, info.PromptLang, "", 3)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("failed to generate script (%s): %w", lang, err)
				}
				mu.Unlock()
				return
			}

			sentences := ExtractSentences(fullText)
			if len(sentences) == 0 {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("script too short for language %s: no meaningful sentences found", lang)
				}
				mu.Unlock()
				return
			}

			chapters := s.planSemanticChapters(ctx, req.Topic, fullText, req.Duration, 4, info.PromptLang)

			frasiImportanti := make([]string, 0, util.Min(5, len(sentences)))
			for _, chapter := range chapters {
				phrases := SelectImportantPhrases(chapter.SourceText, req.Topic, 1)
				if len(phrases) > 0 {
					frasiImportanti = append(frasiImportanti, phrases[0])
				}
			}
			if len(frasiImportanti) == 0 {
				frasiImportanti = SelectImportantPhrases(fullText, req.Topic, util.Min(5, len(sentences)))
			}
			if len(frasiImportanti) == 0 {
				frasiImportanti = sentences[:util.Min(4, len(sentences))]
			}
			nomiSpeciali := extractProperNouns(sentences)
			paroleImportant := extractKeywords(fullText)
			entitaConImmagine := extractEntitiesWithImages(sentences)

			// Keep outputs compact and stable for downstream matching and docs readability.
			frasiImportanti = limitStringList(frasiImportanti, 5)
			nomiSpeciali = limitStringList(nomiSpeciali, 5)
			paroleImportant = limitStringList(paroleImportant, 5)
			entitaConImmagine = limitEntityImageMap(entitaConImmagine, 5)

			keywords := s.extractClipKeywords(frasiImportanti, nomiSpeciali, paroleImportant)
			if normalizeAssociationMode(req.AssociationMode) != AssociationModeFullArtlist &&
				normalizeAssociationMode(req.AssociationMode) != AssociationModeImagesFull &&
				normalizeAssociationMode(req.AssociationMode) != AssociationModeImagesOnly &&
				len(keywords) > 0 &&
				s.clipSearch != nil {
				logger.Info("Starting dynamic clip search", zap.Strings("keywords", keywords))
				dynamicClips, err := s.clipSearch.SearchClips(ctx, keywords)
				if err != nil {
					logger.Warn("Dynamic clip search failed", zap.Error(err))
				} else if len(dynamicClips) > 0 {
					s.dynamicClipsMu.Lock()
					s.dynamicClips = append(s.dynamicClips, dynamicClips...)
					s.dynamicClipsMu.Unlock()
					logger.Info("Dynamic clips found", zap.Int("count", len(dynamicClips)))
				}
			}

			associations := []ClipAssociation(nil)
			var stockAssociations []ClipAssociation
			var artlistAssociations []ClipAssociation
			var timeline []ArtlistTimeline
			var imageAssociations []ImageAssociation
			var mixedSegments []MixedSegment

			switch normalizeAssociationMode(req.AssociationMode) {
			case AssociationModeImagesFull:
				associations = s.associateClipsForDocs(frasiImportanti, stockFolder, req.Topic)
				associations = filterAssociationsByMode(associations, req.AssociationMode)
				stockAssociations, artlistAssociations = s.splitAssociationsBySource(associations)
				timeline = s.buildArtlistTimeline(associations, req.Duration)
				imageAssociations = s.buildImagesFullAssociations(ctx, req.Topic, chapters, entitaConImmagine)
			case AssociationModeImagesOnly:
				imageAssociations = s.buildImagesFullAssociations(ctx, req.Topic, chapters, entitaConImmagine)
			case AssociationModeMixed:
				mixedSegments = s.buildMixedSegments(ctx, req.Topic, chapters, stockFolder, entitaConImmagine)
			default:
				associations = s.associateClips(frasiImportanti, stockFolder, req.Topic)
				associations = filterAssociationsByMode(associations, req.AssociationMode)
				stockAssociations, artlistAssociations = s.splitAssociationsBySource(associations)
				timeline = s.buildArtlistTimeline(associations, req.Duration)
			}

			result := LanguageResult{
				Language:            lang,
				FullText:            fullText,
				Chapters:            chapters,
				FrasiImportanti:     frasiImportanti,
				NomiSpeciali:        nomiSpeciali,
				ParoleImportant:     paroleImportant,
				EntitaConImmagine:   entitaConImmagine,
				Associations:        associations,
				StockAssociations:   stockAssociations,
				ArtlistAssociations: artlistAssociations,
				ArtlistTimeline:     timeline,
				ImageAssociations:   imageAssociations,
				MixedSegments:       mixedSegments,
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

	content := s.buildMultilingualDocument(req.Topic, req.Duration, stockFolder, langResults)
	title := fmt.Sprintf("Script: %s (%s)", req.Topic, langNames(langResults))
	docID, docURL := "", ""
	previewPath := ""
	var err error
	if req.PreviewOnly {
		docID, docURL, err = s.saveToLocalFile(title, content)
		if err != nil {
			return nil, fmt.Errorf("failed to create preview document: %w", err)
		}
		previewPath = docURL
	} else {
		docID, docURL, err = s.createDocWithFallback(ctx, title, content)
		if err != nil {
			return nil, fmt.Errorf("failed to create document: %w", err)
		}
	}

	var imagePlan *ImagePlan
	var imagePlanPath string
	if normalizeAssociationMode(req.AssociationMode) == AssociationModeImagesFull {
		imagePlan = s.buildImagePlan(req.Topic, req.Duration, req.AssociationMode, langResults)
		if imagePlan != nil {
			if path, err := saveImagePlanJSON(req.Topic, imagePlan); err == nil {
				imagePlanPath = path
			}
		}
	}
	if normalizeAssociationMode(req.AssociationMode) == AssociationModeImagesOnly {
		imagePlan = s.buildImagePlan(req.Topic, req.Duration, req.AssociationMode, langResults)
		if imagePlan != nil {
			if path, err := saveImagePlanJSON(req.Topic, imagePlan); err == nil {
				imagePlanPath = path
			}
		}
	}

	auditPath, _ := saveScriptDocAuditJSON(req, &ScriptDocResult{
		DocID:          docID,
		DocURL:         docURL,
		Title:          title,
		Languages:      langResults,
		StockFolder:    stockFolder.Name,
		StockFolderURL: stockFolder.URL,
		ImagePlan:      imagePlan,
		ImagePlanPath:  imagePlanPath,
	})

	result := &ScriptDocResult{
		DocID:          docID,
		DocURL:         docURL,
		Title:          title,
		Languages:      langResults,
		StockFolder:    stockFolder.Name,
		StockFolderURL: stockFolder.URL,
		ImagePlan:      imagePlan,
		ImagePlanPath:  imagePlanPath,
		AuditPath:      auditPath,
		PreviewPath:    previewPath,
	}

	logger.Info("Script doc pipeline completed",
		zap.String("topic", req.Topic),
		zap.String("doc_id", docID),
		zap.Int("languages", len(langResults)),
	)

	return result, nil
}
