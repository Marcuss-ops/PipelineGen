package script

import (
	"strings"
	"fmt"

	"velox/go-master/internal/ml/ollama/types"
	imgservice "velox/go-master/internal/service/images"
	"go.uber.org/zap"
)

type imagePlanItem struct {
	Subject string
	URL     string
	Path    string
}

func buildImagePlanningSection(req ScriptDocsRequest, narrative string, analysis *types.FullEntityAnalysis, pythonScriptsDir string, imgService *imgservice.Service) ScriptSection {
	subjects := pickImageSubjects(req.Topic, analysis, 5)
	
	zap.L().Info("Image planning starting", 
		zap.Strings("subjects", subjects),
		zap.String("topic", req.Topic),
	)

	if len(subjects) == 0 {
		return ScriptSection{
			Title: "📸 Entità con Immagine",
			Body:  "Nessun soggetto identificato per le immagini.",
		}
	}

	var items []imagePlanItem
	for _, subject := range subjects {
		if imgService != nil {
			slug := Slugify(subject)
			
			// Costruiamo una query più specifica se il soggetto è corto
			query := subject
			if len(strings.Fields(subject)) < 2 && !strings.Contains(strings.ToLower(req.Topic), strings.ToLower(subject)) {
				query = subject + " " + req.Topic
			}
			
			zap.L().Debug("Searching for image subject", 
				zap.String("subject", subject), 
				zap.String("query", query),
			)
			
			asset, err := imgService.SearchAndDownload(slug, subject, query, req.Language, nil)
			if err != nil {
				zap.L().Warn("Image search failed", zap.String("subject", subject), zap.Error(err))
				continue
			}
			
			if asset == nil {
				continue
			}

			zap.L().Info("Found image asset", 
				zap.String("subject", subject), 
				zap.String("url", asset.SourceURL),
			)

			items = append(items, imagePlanItem{
				Subject: subject,
				URL:     asset.SourceURL,
				Path:    asset.PathRel,
			})
		}
	}

	if len(items) == 0 {
		return ScriptSection{
			Title: "📸 Entità con Immagine",
			Body:  "Ricerca completata: nessuna immagine trovata online.",
		}
	}

	return ScriptSection{
		Title: "📸 Entità con Immagine",
		Body:  renderImagePlans(items),
	}
}

func pickImageSubjects(topic string, analysis *types.FullEntityAnalysis, max int) []string {
	seen := make(map[string]struct{})
	var result []string

	add := func(s string, priority bool) {
		s = strings.TrimSpace(s)
		if s == "" || len(result) >= max {
			return
		}
		
		// Filtro qualitativo: AVOID generic common nouns unless high priority
		lower := strings.ToLower(s)
		genericNouns := map[string]bool{
			"bottega": true, "pizzaiolo": true, "storia": true, "passione": true, 
			"tradizione": true, "vita": true, "momento": true, "segreto": true,
		}
		
		if !priority && genericNouns[lower] {
			return
		}

		// Filtriamo nomi troppo lunghi o troppo corti
		if len([]rune(s)) < 3 || strings.Count(s, " ") > 5 {
			return
		}
		
		if _, ok := seen[lower]; ok {
			return
		}
		
		seen[lower] = struct{}{}
		result = append(result, s)
	}

	if analysis != nil {
		for _, segment := range analysis.SegmentEntities {
			// 1. Nomi speciali (priorità massima)
			for _, name := range segment.NomiSpeciali {
				add(name, strings.Contains(name, " "))
			}
			
			// 2. Parole importanti (solo se specifiche)
			for _, word := range segment.ParoleImportanti {
				if strings.Contains(word, " ") {
					add(word, true)
				}
			}

			// 3. Entità senza testo
			if segment.EntitaSenzaTesto != nil {
				for name := range segment.EntitaSenzaTesto {
					add(name, false)
				}
			}
		}
	}

	// 4. Topic completo come fallback finale
	add(topic, true)

	return result
}

func renderImagePlans(items []imagePlanItem) string {
	var b strings.Builder
	for i, item := range items {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("🖼️ \"%s\": %s", item.Subject, item.URL))
	}
	return b.String()
}

// Local slugify
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
