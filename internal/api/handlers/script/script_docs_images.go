package script

import (
	"strings"
	"fmt"

	"velox/go-master/internal/ml/ollama/types"
	imgservice "velox/go-master/internal/service/images"
)

type imagePlanItem struct {
	Subject string
	URL     string
	Path    string
}

func buildImagePlanningSection(req ScriptDocsRequest, narrative string, analysis *types.FullEntityAnalysis, pythonScriptsDir string, imgService *imgservice.Service) ScriptSection {
	subjects := pickImageSubjects(req.Topic, analysis, 5)
	
	fmt.Printf("[DEBUG] Image planning starting for subjects: %v\n", subjects)

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
			fmt.Printf("[DEBUG] Searching for subject: '%s' (slug: '%s')\n", subject, slug)
			
			asset, err := imgService.SearchAndDownload(slug, subject, subject, req.Language)
			if err != nil {
				fmt.Printf("[DEBUG] SearchAndDownload ERROR for '%s': %v\n", subject, err)
				continue
			}
			
			if asset == nil {
				fmt.Printf("[DEBUG] SearchAndDownload returned NIL asset for '%s'\n", subject)
				continue
			}

			fmt.Printf("[DEBUG] Found asset for '%s': URL=%s, Path=%s\n", subject, asset.SourceURL, asset.PathRel)

			items = append(items, imagePlanItem{
				Subject: subject,
				URL:     asset.SourceURL,
				Path:    asset.PathRel,
			})
		}
	}

	fmt.Printf("[DEBUG] Image planning finished. Items found: %d\n", len(items))

	if len(items) == 0 {
		return ScriptSection{
			Title: "📸 Entità con Immagine",
			Body:  "Ricerca completata: nessuna immagine trovata online (zero items).",
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

	add := func(s string, allowSingle bool) {
		s = strings.TrimSpace(s)
		if s == "" || len(result) >= max {
			return
		}
		if !allowSingle && !strings.Contains(s, " ") {
			return
		}
		if len([]rune(s)) < 3 || strings.Count(s, " ") > 5 {
			return
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		result = append(result, s)
	}

	if analysis != nil {
		for _, segment := range analysis.SegmentEntities {
			for _, name := range segment.NomiSpeciali {
				add(name, true)
			}
			for name := range segment.EntitaSenzaTesto {
				add(name, true)
			}
		}
	}

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
