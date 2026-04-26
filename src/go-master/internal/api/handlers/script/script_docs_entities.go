package script

import (
	"context"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/clips"
)

func buildEntityExtractionAnalysis(ctx context.Context, gen *ollama.Generator, script string, dataDir, nodeScraperDir, pythonScriptsDir string, clipsRepo, artlistRepo *clips.Repository) (*types.FullEntityAnalysis, error) {
	client := gen.GetClient()
	if client == nil {
		return nil, fmt.Errorf("Ollama client not initialized")
	}

	shortScript := truncateScript(script, 6000)
	extractCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	analysis, err := client.ExtractEntitiesFromSegment(extractCtx, types.EntityExtractionRequest{
		SegmentText:  shortScript,
		SegmentIndex: 0,
		EntityCount:  6,
	})
	if err != nil {
		return nil, fmt.Errorf("Entity extraction unavailable: %v", err)
	}

	// Resolve image URLs using DuckDuckGo search
	if analysis != nil && len(analysis.EntitaSenzaTesto) > 0 {
		for entity := range analysis.EntitaSenzaTesto {
			imgURL := searchDDGImage(entity, pythonScriptsDir)
			if imgURL == "" {
				imgURL = "Nessuna immagine trovata"
			}
			analysis.EntitaSenzaTesto[entity] = imgURL
		}
	}

	// Artlist DB Integration: search for clips based on artlist_phrases keywords
	artlistMatches := make(map[string][]string)

	clean := func(s string) string {
		s = strings.ToLower(s)
		// Remove all common punctuation and various quote marks
		replacer := strings.NewReplacer(
			".", "", ",", "", "!", "", "?", "",
			"\"", "", "'", "", "‘", "", "’", "",
			"“", "", "”", "", "(", "", ")", "",
			":", "", ";", "", "-", "",
		)
		s = replacer.Replace(s)
		return strings.Join(strings.Fields(s), " ") // normalize whitespace
	}

	scriptClean := clean(shortScript)

	for phrase, keywords := range analysis.ArtlistPhrases {
		pClean := clean(phrase)
		if pClean == "" {
			continue
		}

		// STRICT FILTER: Check if the phrase is literally in the script
		if !strings.Contains(scriptClean, pClean) {
			fmt.Printf("FILTERED HALLUCINATION: %s (Clean: %s)\n", phrase, pClean)
			continue
		}

		var links []string
		if artlistRepo != nil {
			matches, err := artlistRepo.SearchStockByKeywords(ctx, keywords, 3)
			if err == nil && len(matches) > 0 {
				for _, m := range matches {
					link := m.ExternalURL
					if link == "" {
						link = m.DriveLink
					}
					if link != "" {
						links = append(links, link)
					}
				}
			}
		}
		artlistMatches[phrase] = links
	}

	fullAnalysis := &types.FullEntityAnalysis{
		TotalSegments:         1,
		EntityCountPerSegment: 6,
		TotalEntities:         len(analysis.FrasiImportanti) + len(analysis.EntitaSenzaTesto) + len(analysis.NomiSpeciali) + len(analysis.ParoleImportanti) + len(analysis.ArtlistPhrases),
		SegmentEntities: []types.SegmentEntities{
			{
				SegmentIndex:     analysis.SegmentIndex,
				SegmentText:      shortScript,
				FrasiImportanti:  analysis.FrasiImportanti,
				EntitaSenzaTesto: analysis.EntitaSenzaTesto,
				NomiSpeciali:     analysis.NomiSpeciali,
				ParoleImportanti: analysis.ParoleImportanti,
				ArtlistPhrases:   analysis.ArtlistPhrases,
				ArtlistMatches:   artlistMatches,
			},
		},
	}

	return fullAnalysis, nil
}

func truncateScript(script string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(script))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes])
}

func renderEntityAnalysis(analysis *types.FullEntityAnalysis, timeline *TimelinePlan) string {
	if analysis == nil {
		return "⚠️ Nessuna analisi delle entità disponibile."
	}

	var b strings.Builder
	b.WriteString("📽️ ANALISI NARRATIVA E VISUALE\n")
	b.WriteString("==========================================\n")
	b.WriteString(fmt.Sprintf("📊 Segmenti analizzati: %d\n", analysis.TotalSegments))
	b.WriteString(fmt.Sprintf("🔍 Asset totali rilevati: %d\n", analysis.TotalEntities))
	b.WriteString("------------------------------------------\n")

	for _, segment := range analysis.SegmentEntities {
		b.WriteString(fmt.Sprintf("📍 SEGMENTO %d\n", segment.SegmentIndex+1))

		if len(segment.FrasiImportanti) > 0 {
			b.WriteString("\n📢 FRASI IMPORTANTI:\n")
			for _, item := range segment.FrasiImportanti {
				b.WriteString("   ✨ \"" + cleanDisplayPhrase(item) + "\"\n")
			}
		}

		if len(segment.NomiSpeciali) > 0 {
			b.WriteString("\n⭐ NOMI SPECIALI:\n")
			for _, item := range segment.NomiSpeciali {
				b.WriteString("   🆔 " + item + "\n")
			}
		}

		if len(segment.ParoleImportanti) > 0 {
			b.WriteString("\n🗝️ PAROLE IMPORTANTI:\n")
			for _, item := range segment.ParoleImportanti {
				b.WriteString("   🔹 " + item + "\n")
			}
		}

		if len(segment.EntitaSenzaTesto) > 0 {
			b.WriteString("\n🖼️ IMMAGINI CORRELATE:\n")
			for entity, imageLink := range segment.EntitaSenzaTesto {
				if imageLink == "" || strings.Contains(imageLink, "Nessuna immagine") || strings.Contains(imageLink, "placeholder") {
					b.WriteString(fmt.Sprintf("   🖼️ %s: (Ricerca in corso...)\n", entity))
				} else {
					b.WriteString(fmt.Sprintf("   ✅ %s:\n      🔗 %s\n", entity, imageLink))
				}
			}
		}

		b.WriteString("\n------------------------------------------\n")
	}

	return strings.TrimSpace(b.String())
}

func cleanDisplayPhrase(text string) string {
	return strings.TrimSpace(strings.Trim(text, "\"'“‘’”"))
}
