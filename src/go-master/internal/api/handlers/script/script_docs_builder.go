package script

import (
	"context"
	"fmt"
	"strings"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/clips"
)

// BuildScriptDocument assembles the modular document with explicit sections.
func BuildScriptDocument(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, dataDir, clipTextDir, pythonScriptsDir, nodeScraperDir string, StockDriveRepo, ArtlistRepo *clips.Repository) (*ScriptDocument, error) {
	narrative, err := buildNarrativeScript(ctx, gen, req)
	if err != nil {
		return nil, err
	}

	analysis, err := buildEntityExtractionAnalysis(ctx, gen, req.Topic, narrative, dataDir, nodeScraperDir, pythonScriptsDir, StockDriveRepo, ArtlistRepo)
	if err != nil {
		// handle error or just pass nil analysis
	}

	timelinePlan, err := buildTimelinePlan(ctx, gen, req, narrative, analysis, dataDir, StockDriveRepo, nodeScraperDir)
	if err != nil {
		timelinePlan = &TimelinePlan{
			PrimaryFocus:  req.Topic,
			SegmentCount:  0,
			TotalDuration: req.Duration,
		}
	}

	artlistSection := buildArtlistMatchingSection(ctx, dataDir, req, narrative, analysis, timelinePlan, ArtlistRepo)

	sections := []ScriptSection{
		buildMetadataSection(req),
		{
			Title: "🎙️ Narrative Script",
			Body:  narrative,
		},
		{
			Title: "⏱️ Timeline",
			Body:  renderTimelinePlan(timelinePlan),
		},
		{
			Title: "🔎 Entity Extraction",
			Body:  renderEntityAnalysis(analysis, timelinePlan),
		},
		artlistSection,
	}

	return &ScriptDocument{
		Title:    fmt.Sprintf("SCRIPT TEST: %s", req.Topic),
		Content:  renderScriptDocument(fmt.Sprintf("SCRIPT TEST: %s", req.Topic), sections),
		Sections: sections,
		Timeline: timelinePlan,
	}, nil
}

func buildNarrativeScript(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest) (string, error) {
	if strings.TrimSpace(req.SourceText) != "" {
		return buildNarrativeFromSourceText(ctx, gen, req)
	}

	prompt := buildPrompt(req.Topic, req.Duration, req.Language, req.Template)
	client := gen.GetClient()
	if client == nil {
		return "", fmt.Errorf("ollama client not initialized")
	}

	text, err := client.GenerateWithOptions(ctx, "gemma3:12b", prompt, nil)
	if err != nil {
		return "", err
	}
	return normalizeNarrativeText(text), nil
}

func buildNarrativeFromSourceText(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest) (string, error) {
	client := gen.GetClient()
	if client == nil {
		return "", fmt.Errorf("ollama client not initialized")
	}

	wordCount := req.Duration * 3
	prompt := fmt.Sprintf(`Rewrite the following source text into a clean documentary narration in %s.

Rules:
- Keep the original topic and factual direction.
- Do not copy headings, markdown, bullet lists, or section titles.
- Do not include labels like "Opening", "Closing", "###", or commentary about the script.
- Output only continuous narration in plain prose.
- Target length: %d words.
- Minimum length: %d words.
- Maximum length: %d words.
- Write at least 3 paragraphs.
- Expand short source material with transitions, context, and cinematic detail until you hit the target.
- Do not be shorter than the target.

Source text:
%s`, req.Language, wordCount, wordCount-25, wordCount+25, req.SourceText)

	text, err := client.GenerateWithOptions(ctx, "gemma3:12b", prompt, nil)
	if err != nil {
		return "", err
	}
	return normalizeNarrativeText(text), nil
}

func buildMetadataSection(req ScriptDocsRequest) ScriptSection {
	return ScriptSection{
		Title: "🧾 Metadata",
		Body: fmt.Sprintf(
			"Topic: %s\nDurata: %d secondi\nLingua: %s\nTemplate: %s\nMode: modular",
			req.Topic, req.Duration, req.Language, req.Template,
		),
	}
}

func normalizeNarrativeText(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "•") || strings.HasPrefix(line, "*") {
			continue
		}
		if len(filtered) == 0 && (strings.Contains(lower, "here's") || strings.Contains(lower, "here is") || strings.Contains(lower, "ready for narration") || strings.HasPrefix(lower, "okay,")) {
			continue
		}
		if strings.HasPrefix(lower, "narrative script") || strings.HasPrefix(lower, "metadata") || strings.HasPrefix(lower, "timeline") {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.TrimSpace(strings.Join(filtered, "\n"))
}
