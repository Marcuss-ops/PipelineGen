package script

import (
	"fmt"
	"strings"
	"velox/go-master/internal/matching"

	"velox/go-master/internal/ml/ollama/types"
	imgservice "velox/go-master/internal/service/images"
)

type imagePlanItem struct {
	Subject string
	URL     string
	Path    string
	Reason  string
}

func buildImagePlanningSection(req ScriptDocsRequest, narrative string, analysis *types.FullEntityAnalysis, stockSection, artlistSection, driveSection ScriptSection, pythonScriptsDir string, imgService *imgservice.Service) ScriptSection {
	subjects := pickImageSubjects(req.Topic, analysis, 5)
	if len(subjects) == 0 {
		return ScriptSection{
			Title: "📸 Entità con Immagine",
			Body:  "Nessuna entità rilevata per la ricerca immagini.",
		}
	}

	var items []imagePlanItem
	for _, subject := range subjects {
		item := imagePlanItem{
			Subject: subject,
			Reason:  "Ricerca automatica per entità rilevata",
		}

		if imgService != nil {
			slug := strings.ReplaceAll(strings.ToLower(subject), " ", "-")
			// SearchAndDownload cerca già nel DB (tramite GetSubjectBySlugOrAlias e GetImageByHash)
			// e fa fallback su Wikipedia/DDG se non trova nulla.
			asset, err := imgService.SearchAndDownload(slug, subject, subject)
			if err == nil && asset != nil {
				item.URL = asset.SourceURL
				item.Path = asset.PathRel
				item.Reason = "Trovata e scaricata (Wikipedia/DDG)"
			} else {
				item.Reason = fmt.Sprintf("Ricerca fallita: %v", err)
			}
		}
		items = append(items, item)
	}

	return ScriptSection{
		Title: "📸 Entità con Immagine",
		Body:  renderImagePlans(items),
	}
}

func pickImageSubjects(topic string, analysis *types.FullEntityAnalysis, max int) []string {
	seen := make(map[string]struct{})
	var result []string

	add := func(s string) bool {
		if len(result) >= max {
			return false
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return false
		}
		// Filtriamo nomi troppo lunghi o troppo corti per la ricerca immagini
		if strings.Count(s, " ") > 2 {
			return false
		}
		if len([]rune(s)) < 3 {
			return false
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			return false
		}
		seen[key] = struct{}{}
		result = append(result, s)
		return true
	}

	// 1. Nomi speciali dall'analisi (priorità alta)
	if analysis != nil {
		for _, segment := range analysis.SegmentEntities {
			for _, name := range segment.NomiSpeciali {
				add(name)
			}
		}
	}

	// 2. Parole importanti dal topic
	for _, term := range matching.Tokenize(topic) {
		if matching.IsStopWord(term) || len(term) < 4 {
			continue
		}
		add(term)
	}

	// 3. Entità senza testo (keyword estratte)
	if analysis != nil {
		for _, segment := range analysis.SegmentEntities {
			for name := range segment.EntitaSenzaTesto {
				add(name)
			}
		}
	}

	return result
}

func renderImagePlans(items []imagePlanItem) string {
	var b strings.Builder
	for i, item := range items {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Soggetto: ")
		b.WriteString(item.Subject)
		b.WriteString("\n")
		
		if item.URL != "" {
			b.WriteString("   URL:      ")
			b.WriteString(item.URL)
			b.WriteString("\n")
		} else {
			b.WriteString("   URL:      None\n")
		}
		
		if item.Path != "" {
			b.WriteString("   Path:     ")
			b.WriteString(item.Path)
			b.WriteString("\n")
		}
		
		b.WriteString("   Stato:    ")
		b.WriteString(item.Reason)
	}
	return b.String()
}
