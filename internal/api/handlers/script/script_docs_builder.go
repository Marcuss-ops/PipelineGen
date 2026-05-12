package script

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/prompts"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/clips"
	artlistSvc "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/association"
	clipresolver "velox/go-master/internal/service/clipresolver"
	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/internal/service/visualplanner"
	"velox/go-master/pkg/textutil"
	"go.uber.org/zap"
)

// BuildScriptDocument generates the modular script document using Ollama and the local catalogs.
func BuildScriptDocument(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, dataDir, pythonScriptsDir, nodeScraperDir string, StockDriveRepo, ArtlistRepo, ClipsRepo *clips.Repository, artlistService *artlistSvc.Service, imgService *imgservice.Service, assocService *association.Service, clipResolver *clipresolver.Service) (*ScriptDocument, error) {
	req.Normalize()

	if gen == nil || gen.GetClient() == nil {
		return nil, fmt.Errorf("ollama generator not initialized")
	}

	sourceText := strings.TrimSpace(req.SourceText)
	if sourceText == "" {
		sourceText = req.Topic
	}

	generated, err := gen.GenerateScript(ctx, types.TextGenerationRequest{
		Title:      req.Topic,
		SourceText: sourceText,
		Language:   req.Language,
		Duration:   req.Duration,
		Tone:       req.Template,
		Model:      "",
		Options:    map[string]interface{}{},
	})
	if err != nil {
		return nil, fmt.Errorf("ollama script generation failed: %w", err)
	}

	narrative := strings.TrimSpace(generated.Script)
	if narrative == "" {
		narrative = strings.TrimSpace(sourceText)
	}
	if narrative == "" {
		narrative = req.Topic
	}
	narrative = types.CleanScript(narrative)

	// 1. Build Timeline
	timeline, _ := BuildTimelinePlan(ctx, gen, req, dataDir, nodeScraperDir, sourceText, narrative, StockDriveRepo, ArtlistRepo, ClipsRepo, artlistService, assocService, clipResolver)

	// 2. Extract Document Analysis
	analysis := extractDocumentAnalysis(ctx, gen, narrative)

	// 5. Extract metadata for final sections
	var phrases []string
	var specialNames []string
	var importantWords []string

	if analysis != nil && len(analysis.SegmentEntities) > 0 {
		entities := analysis.SegmentEntities[0]
		phrases = entities.FrasiImportanti
		specialNames = entities.NomiSpeciali
		importantWords = entities.ParoleImportanti
		
		zap.L().Info("LLM document analysis successful",
			zap.Int("phrases", len(phrases)),
			zap.Int("special_names", len(specialNames)),
			zap.Int("important_words", len(importantWords)),
		)
	}

	// FALLBACK: If LLM analysis failed or returned empty results, use heuristics
	if len(phrases) == 0 {
		zap.L().Info("falling back to heuristic phrase extraction")
		phrases = extractImportantPhrases(narrative)
	}
	if len(specialNames) == 0 {
		zap.L().Info("falling back to heuristic name extraction")
		specialNames = extractSpecialNames(narrative)
	}
	if len(importantWords) == 0 {
		zap.L().Info("falling back to heuristic word extraction")
		importantWords = extractImportantWords(narrative, 10)
	}

	// 3. Build Unified Visual Plan
	var vpSegments []visualplanner.TimelineSegment
	if timeline != nil {
		for _, seg := range timeline.Segments {
			vpSegments = append(vpSegments, visualplanner.TimelineSegment{
				Index:             seg.Index,
				VisualSubject:     seg.VisualSubject,
				VisualCaption:     seg.VisualCaption,
				SearchSuggestions: seg.SearchSuggestions,
			})
		}
	}
	vPlan := visualplanner.Build(req.Topic, narrative, analysis, vpSegments)

	// 4. Resolve Images through section builder
	var imageSection ScriptSection
	if imgService != nil {
		imageSection = buildImagePlanningSection(req, vPlan, imgService)
	}

	importantPhrasesSection := ScriptSection{
		Title: "📢 IMPORTANT PHRASES",
		Body:  renderImportantPhrases(phrases),
	}
	specialNamesSection := ScriptSection{
		Title: "⭐ SPECIAL NAMES",
		Body:  renderSpecialNamesWithImages(specialNames, nil),
	}
	importantWordsSection := ScriptSection{
		Title: "🗝️ IMPORTANT WORDS",
		Body:  renderImportantWords(importantWords),
	}

	sections := []ScriptSection{
		{Title: "🧾 Metadata", Body: renderMetadata(req)},
		{Title: types.MarkerNarrator, Body: narrative},
		{Title: types.MarkerTimeline, Body: RenderTimeline(timeline)},
	}

	// Only add image section if it has content
	if strings.TrimSpace(imageSection.Body) != "" && !strings.Contains(imageSection.Body, "nessuna immagine trovata") && !strings.Contains(imageSection.Body, "Nessun soggetto") {
		sections = append(sections, imageSection)
	}

	sections = append(sections, importantPhrasesSection, specialNamesSection, importantWordsSection)

	content := renderScriptDocument(req.Topic, sections)
	return &ScriptDocument{
		Title:    req.Topic,
		Content:  content,
		Sections: sections,
		Timeline: timeline,
	}, nil
}

func extractDocumentAnalysis(ctx context.Context, gen *ollama.Generator, narrative string) *types.FullEntityAnalysis {
	if gen == nil {
		return nil
	}

	extractionPrompt := prompts.BuildEntityExtractionPrompt(narrative, 10)
	
	type localExtracted struct {
		FrasiImportanti  []string    `json:"frasi_importanti"`
		EntitaSenzaTesto interface{} `json:"entity_senza_testo"`
		NomiSpeciali     []string    `json:"nomi_speciali"`
		ParoleImportanti []string    `json:"parole_importanti"`
	}
	
	var extracted localExtracted
	messages := []types.Message{
		{Role: "user", Content: extractionPrompt},
	}
	
	resp, err := gen.GetClient().Chat(ctx, messages, nil)
	if err != nil {
		zap.L().Error("LLM metadata extraction failed", zap.Error(err))
		return nil
	}

	jsonStr := textutil.ExtractJSONObject(resp)
	zap.L().Info("LLM metadata extraction successful", zap.String("json", jsonStr))
	
	if err := json.Unmarshal([]byte(jsonStr), &extracted); err != nil {
		zap.L().Error("LLM metadata unmarshal failed", zap.Error(err), zap.String("json", jsonStr))
		return nil
	}

	formal := types.SegmentEntities{
		FrasiImportanti:  extracted.FrasiImportanti,
		NomiSpeciali:     extracted.NomiSpeciali,
		ParoleImportanti: extracted.ParoleImportanti,
		EntitaSenzaTesto: make(map[string]string),
	}
	
	if extracted.EntitaSenzaTesto != nil {
		switch v := extracted.EntitaSenzaTesto.(type) {
		case string:
			formal.EntitaSenzaTesto[v] = "Search term"
		case map[string]interface{}:
			for k := range v {
				formal.EntitaSenzaTesto[k] = "Search term"
			}
		}
	}

	return &types.FullEntityAnalysis{
		SegmentEntities: []types.SegmentEntities{formal},
	}
}
