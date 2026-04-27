package script

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/models"
)

type nodeScraperResponse struct {
	OK    bool `json:"ok"`
	Clips []struct {
		Title       string `json:"title"`
		ClipPageURL string `json:"clip_page_url"`
		PrimaryURL  string `json:"primary_url"`
		ClipID      string `json:"clip_id"`
	} `json:"clips"`
}

func fetchFromArtlistScraper(ctx context.Context, keyword, nodeScraperDir string) ([]models.Clip, error) {
	scriptPath := filepath.Join(nodeScraperDir, "artlist_search.js")
	cmd := exec.CommandContext(ctx, "node", scriptPath, "--term", keyword, "--limit", "3", "--save-db")
	cmd.Dir = nodeScraperDir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return nil, err
	}

	var payload nodeScraperResponse
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return nil, err
	}

	var results []models.Clip
	for _, c := range payload.Clips {
		results = append(results, models.Clip{
			ID:          c.ClipID,
			Name:        c.Title,
			ExternalURL: c.PrimaryURL,
			DriveLink:   c.ClipPageURL, // Used as fallback for preview
			Source:      "artlist",
			Category:    "dynamic",
			Tags:        []string{keyword},
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		})
	}
	return results, nil
}

func buildEntityExtractionAnalysis(ctx context.Context, gen *ollama.Generator, topic, script string, dataDir, nodeScraperDir, pythonScriptsDir string, StockDriveRepo, ArtlistRepo *clips.Repository) (*types.FullEntityAnalysis, error) {
	client := gen.GetClient()
	if client == nil {
		return fallbackEntityExtractionAnalysis(topic, script), fmt.Errorf("Ollama client not initialized")
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
		return fallbackEntityExtractionAnalysis(topic, script), fmt.Errorf("Entity extraction unavailable: %v", err)
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
	var artlistDBClient *ArtlistDBClient
	if strings.TrimSpace(nodeScraperDir) != "" {
		artlistDBClient = NewArtlistDBClient(nodeScraperDir)
	}

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

		link := ""

		if artlistDBClient != nil && len(keywords) > 0 {
			matches, err := artlistDBClient.SearchClipsByKeywords(keywords, 3)
			if err == nil && len(matches) > 0 {
				link = selectBestMatchLink(matches)
			}
		}

		if link == "" && ArtlistRepo != nil && len(keywords) > 0 {
			matches, err := ArtlistRepo.SearchStockByKeywords(ctx, keywords, 3)
			if err == nil && len(matches) > 0 {
				artlistOnly := filterMatchesBySource(clipMatchesToScored(matches, nodeScraperDir), "artlist")
				link = selectBestMatchLink(artlistOnly)
			}
		}

		if link == "" && nodeScraperDir != "" {
			bestKeyword := phrase
			if len(keywords) > 0 {
				bestKeyword = keywords[0]
				if len(keywords) > 1 {
					bestKeyword = strings.Join(keywords[:2], " ")
				}
			}

			fmt.Printf("Scraping Artlist for keyword: %s\n", bestKeyword)
			scrapedClips, scrapeErr := fetchFromArtlistScraper(ctx, bestKeyword, nodeScraperDir)
			if scrapeErr == nil && len(scrapedClips) > 0 {
				for _, sc := range scrapedClips {
					sc.Tags = append(sc.Tags, keywords...)
					if ArtlistRepo != nil {
						_ = ArtlistRepo.UpsertClip(ctx, &sc)
					}
					candidateLink := sc.ExternalURL
					if candidateLink == "" {
						candidateLink = sc.DriveLink
					}
					if candidateLink != "" {
						link = candidateLink
						break
					}
				}
			}
		}

		if link != "" {
			artlistMatches[phrase] = []string{link}
		}
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

func clipMatchesToScored(clips []*models.Clip, nodeScraperDir string) []scoredMatch {
	if len(clips) == 0 {
		return nil
	}
	matches := make([]scoredMatch, 0, len(clips))
	for _, clip := range clips {
		if clip == nil {
			continue
		}
		link := resolveArtlistClipLink(*clip, nodeScraperDir)
		matches = append(matches, scoredMatch{
			Title:   clip.Name,
			Score:   100,
			Source:  clip.Source + " db",
			Link:    link,
			Details: strings.Join(clip.Tags, ", "),
		})
	}
	return matches
}

func fallbackEntityExtractionAnalysis(topic, script string) *types.FullEntityAnalysis {
	firstSentence, lastSentence := extractOpeningAndClosingSentence(script)
	topTerms := collectTopicTerms(topic)

	segment := types.SegmentEntities{
		SegmentIndex:     0,
		SegmentText:      truncateScript(script, 6000),
		FrasiImportanti:  []string{},
		EntitaSenzaTesto: map[string]string{},
		NomiSpeciali:     []string{},
		ParoleImportanti: []string{},
		ArtlistPhrases:   map[string][]string{},
	}

	if strings.TrimSpace(firstSentence) != "" {
		segment.FrasiImportanti = append(segment.FrasiImportanti, firstSentence)
	}
	if strings.TrimSpace(lastSentence) != "" && lastSentence != firstSentence {
		segment.FrasiImportanti = append(segment.FrasiImportanti, lastSentence)
	}
	if len(topTerms) > 0 {
		segment.ParoleImportanti = append(segment.ParoleImportanti, topTerms...)
		segment.ArtlistPhrases[topic] = topTerms
		segment.NomiSpeciali = append(segment.NomiSpeciali, topTerms[0])
	}

	segment.FrasiImportanti = uniqueStrings(segment.FrasiImportanti)
	segment.NomiSpeciali = uniqueStrings(segment.NomiSpeciali)
	segment.ParoleImportanti = uniqueStrings(segment.ParoleImportanti)

	analysis := &types.FullEntityAnalysis{
		TotalSegments:         1,
		EntityCountPerSegment: 6,
		SegmentEntities:       []types.SegmentEntities{segment},
	}
	analysis.TotalEntities = len(segment.FrasiImportanti) + len(segment.EntitaSenzaTesto) + len(segment.NomiSpeciali) + len(segment.ParoleImportanti) + len(segment.ArtlistPhrases)
	return analysis
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
