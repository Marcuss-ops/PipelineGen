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

	timeline, _ := BuildTimelinePlan(ctx, gen, req, dataDir, nodeScraperDir, sourceText, narrative, StockDriveRepo, ArtlistRepo, ClipsRepo, artlistService, assocService, clipResolver)

	// Extract entities and metadata using LLM
	var analysis *types.FullEntityAnalysis
	if gen != nil {
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
		if err == nil {
			jsonStr := textutil.ExtractJSONObject(resp)
			zap.L().Info("LLM metadata extraction successful", zap.String("json", jsonStr))
			
			if err := json.Unmarshal([]byte(jsonStr), &extracted); err == nil {
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

				analysis = &types.FullEntityAnalysis{
					SegmentEntities: []types.SegmentEntities{formal},
				}
			} else {
				zap.L().Error("LLM metadata unmarshal failed", zap.Error(err), zap.String("json", jsonStr))
			}
		} else {
			zap.L().Error("LLM metadata extraction failed", zap.Error(err))
		}
	}

	// Build image section
	var imageSection ScriptSection
	if imgService != nil {
		imageSection = buildImagePlanningSection(req, narrative, analysis, pythonScriptsDir, imgService)
	} else {
		imageSection = ScriptSection{
			Title: "📸 Entità con Immagine",
			Body:  "Servizio immagini non disponibile.",
		}
	}

	// Extract end sections from LLM analysis
	var phrases []string
	var specialNames []string
	var importantWords []string

	if analysis != nil && len(analysis.SegmentEntities) > 0 {
		entities := analysis.SegmentEntities[0]
		phrases = entities.FrasiImportanti
		specialNames = entities.NomiSpeciali
		importantWords = entities.ParoleImportanti
	}

	importantPhrasesSection := ScriptSection{
		Title: "📢 IMPORTANT PHRASES",
		Body:  renderImportantPhrases(phrases),
	}
	specialNamesSection := ScriptSection{
		Title: "⭐ SPECIAL NAMES",
		Body:  renderSpecialNames(specialNames),
	}
	importantWordsSection := ScriptSection{
		Title: "🗝️ IMPORTANT WORDS",
		Body:  renderImportantWords(importantWords),
	}

	sections := []ScriptSection{
		{Title: "🧾 Metadata", Body: renderMetadata(req)},
		{Title: types.MarkerNarrator, Body: narrative},
		{Title: types.MarkerTimeline, Body: RenderTimeline(timeline)},
		imageSection,
		importantPhrasesSection,
		specialNamesSection,
		importantWordsSection,
	}

	content := renderScriptDocument(req.Topic, sections)
	return &ScriptDocument{
		Title:    req.Topic,
		Content:  content,
		Sections: sections,
		Timeline: timeline,
	}, nil
}
