package script

import (
	"context"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/ml/ollama"
)

func buildEntityExtractionSection(ctx context.Context, gen *ollama.Generator, script string) (ScriptSection, *ollama.FullEntityAnalysis) {
	client := gen.GetClient()
	if client == nil {
		return ScriptSection{
			Title: "Entity Extraction",
			Body:  "Entity extraction unavailable: Ollama client not initialized.",
		}, nil
	}

	shortScript := truncateScript(script, 6000)
	extractCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	analysis, err := client.ExtractEntitiesFromSegment(extractCtx, ollama.EntityExtractionRequest{
		SegmentText:  shortScript,
		SegmentIndex: 0,
		EntityCount:  6,
	})
	if err != nil {
		return ScriptSection{
			Title: "Entity Extraction",
			Body:  fmt.Sprintf("Entity extraction unavailable: %v", err),
		}, nil
	}

	fullAnalysis := &ollama.FullEntityAnalysis{
		TotalSegments:         1,
		EntityCountPerSegment: 6,
		TotalEntities:         len(analysis.FrasiImportanti) + len(analysis.EntitaSenzaTesto) + len(analysis.NomiSpeciali) + len(analysis.ParoleImportanti),
		SegmentEntities: []ollama.SegmentEntities{
			{
				SegmentIndex:     analysis.SegmentIndex,
				SegmentText:      shortScript,
				FrasiImportanti:  analysis.FrasiImportanti,
				EntitaSenzaTesto: analysis.EntitaSenzaTesto,
				NomiSpeciali:     analysis.NomiSpeciali,
				ParoleImportanti: analysis.ParoleImportanti,
			},
		},
	}

	return ScriptSection{
		Title: "🔎 Entity Extraction",
		Body:  renderEntityAnalysis(fullAnalysis),
	}, fullAnalysis
}

func truncateScript(script string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(script))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes])
}

func renderEntityAnalysis(analysis *ollama.FullEntityAnalysis) string {
	if analysis == nil {
		return "No entity analysis available."
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Segments analyzed: %d\n", analysis.TotalSegments))
	b.WriteString(fmt.Sprintf("Total entities: %d\n", analysis.TotalEntities))
	b.WriteString("\nPer-segment breakdown:\n")

	for _, segment := range analysis.SegmentEntities {
		b.WriteString(fmt.Sprintf("• Segment %d\n", segment.SegmentIndex+1))
		if len(segment.FrasiImportanti) > 0 {
			b.WriteString("  ✨ Important phrases:\n")
			for _, item := range segment.FrasiImportanti {
				b.WriteString("    • " + item + "\n")
			}
		}
		if len(segment.NomiSpeciali) > 0 {
			b.WriteString("  👤 Special names:\n")
			for _, item := range segment.NomiSpeciali {
				b.WriteString("    • " + item + "\n")
			}
		}
		if len(segment.ParoleImportanti) > 0 {
			b.WriteString("  🔑 Keywords:\n")
			for _, item := range segment.ParoleImportanti {
				b.WriteString("    • " + item + "\n")
			}
		}
	}

	return strings.TrimSpace(b.String())
}
