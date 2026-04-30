package script

import (
	"context"
	"fmt"
	"strings"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/clips"
	artlistSvc "velox/go-master/internal/service/artlist"
	imgservice "velox/go-master/internal/service/images"
)

// BuildScriptDocument generates the modular script document using Ollama and the local catalogs.
func BuildScriptDocument(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, dataDir, pythonScriptsDir, nodeScraperDir string, StockDriveRepo, ArtlistRepo *clips.Repository, artlistService *artlistSvc.Service, imgService *imgservice.Service) (*ScriptDocument, error) {
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
	// cleanNarrativeBody now relies on the general CleanScript logic but we can add document-specific cleaning here
	narrative = types.CleanScript(narrative)

	timeline, _ := BuildTimelinePlan(ctx, gen, req, sourceText, narrative, StockDriveRepo, ArtlistRepo, artlistService)

	analysis := extractNarrativeAnalysis(ctx, gen, req, narrative, timeline)

	// Build image section if service is available
	var imageSection ScriptSection
	if imgService != nil {
		imageSection = buildImagePlanningSection(req, narrative, analysis, ScriptSection{}, ScriptSection{}, ScriptSection{}, pythonScriptsDir, imgService)
	}

	sections := []ScriptSection{
		{Title: "🧾 Metadata", Body: renderMetadata(req)},
		{Title: types.MarkerNarrator, Body: narrative},
		{Title: types.MarkerTimeline, Body: RenderTimeline(timeline)},
		{Title: "🔎 Local Entities", Body: renderEntityExtractionSection(analysis)},
	}

	if imageSection.Title != "" {
		sections = append(sections, imageSection)
	}

	content := renderScriptDocument(req.Topic, sections)
	return &ScriptDocument{
		Title:    req.Topic,
		Content:  content,
		Sections: sections,
		Timeline: timeline,
	}, nil
}

func extractNarrativeAnalysis(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, narrative string, timeline *TimelinePlan) *types.FullEntityAnalysis {
	client := gen.GetClient()
	if client == nil {
		return nil
	}

	chunks := timelinePlanSegmentTextsFromPlan(timeline)
	if len(chunks) == 0 {
		if trimmed := strings.TrimSpace(narrative); trimmed != "" {
			chunks = []string{trimmed}
		}
	}
	if len(chunks) == 0 {
		return nil
	}

	analysis, err := client.ExtractEntitiesFromScript(ctx, chunks, types.DefaultEntityCount)
	if err != nil {
		return nil
	}
	return analysis
}

func timelineSegmentCount(plan *TimelinePlan) int {
	if plan == nil || len(plan.Segments) == 0 {
		return 0
	}
	return len(plan.Segments)
}

func timelinePlanSegmentTextsFromPlan(plan *TimelinePlan) []string {
	if plan == nil || len(plan.Segments) == 0 {
		return nil
	}

	chunks := make([]string, 0, len(plan.Segments))
	for _, seg := range plan.Segments {
		text := strings.TrimSpace(seg.NarrativeText)
		if text == "" {
			text = strings.TrimSpace(seg.OpeningSentence + " " + seg.ClosingSentence)
		}
		if text != "" {
			chunks = append(chunks, text)
		}
	}
	return chunks
}

func renderMetadata(req ScriptDocsRequest) string {
	var b strings.Builder
	b.WriteString("Topic: ")
	b.WriteString(req.Topic)
	b.WriteString("\nDuration: ")
	b.WriteString(fmt.Sprintf("%d seconds", req.Duration))
	b.WriteString("\nLanguage: ")
	b.WriteString(req.Language)
	b.WriteString("\nTemplate: ")
	b.WriteString(req.Template)
	b.WriteString("\nMode: modular")
	return b.String()
}

func renderEntityExtractionSection(analysis *types.FullEntityAnalysis) string {
	if analysis == nil || len(analysis.SegmentEntities) == 0 {
		return "Entity analysis unavailable."
	}

	phrases := make([]string, 0)
	names := make([]string, 0)
	words := make([]string, 0)
	for _, seg := range analysis.SegmentEntities {
		phrases = append(phrases, seg.FrasiImportanti...)
		names = append(names, seg.NomiSpeciali...)
		words = append(words, seg.ParoleImportanti...)
		for name := range seg.EntitaSenzaTesto {
			names = append(names, name)
		}
	}
	phrases = limitStrings(uniqueStrings(phrases), 2)
	names = limitStrings(uniqueStrings(names), 2)
	words = limitStrings(uniqueStrings(words), 2)

	var b strings.Builder
	b.WriteString("📽️ NARRATIVE AND VISUAL ANALYSIS\n")
	b.WriteString("==========================================\n")
	b.WriteString(fmt.Sprintf("📊 Segments analyzed: %d\n", analysis.TotalSegments))
	b.WriteString(fmt.Sprintf("🔍 Total assets detected: %d\n", analysis.TotalEntities))
	b.WriteString("------------------------------------------\n")

	if len(phrases) > 0 {
		b.WriteString("\n📢 IMPORTANT PHRASES:\n")
		for _, phrase := range phrases {
			b.WriteString("   ✨ \"")
			b.WriteString(phrase)
			b.WriteString("\"\n")
		}
	}
	if len(names) > 0 {
		b.WriteString("\n⭐ SPECIAL NAMES:\n")
		for _, name := range names {
			b.WriteString("   🆔 ")
			b.WriteString(name)
			b.WriteString("\n")
		}
	}
	if len(words) > 0 {
		b.WriteString("\n🗝️ IMPORTANT WORDS:\n")
		for _, word := range words {
			b.WriteString("   🔹 ")
			b.WriteString(word)
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func limitStrings(items []string, limit int) []string {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}
