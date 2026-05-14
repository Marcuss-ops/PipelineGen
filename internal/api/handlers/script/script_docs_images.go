package script

import (
	"fmt"
	"strings"

	"go.uber.org/zap"
	imgservice "velox/go-master/internal/service/images"
)

type imagePlanItem struct {
	Subject string
	URL     string
	Path    string
}

func buildImagePlanningSection(req ScriptDocsRequest, plan *VisualPlan, imgService *imgservice.Service) ScriptSection {
	subjects := plan.GlobalImageSubjects(5)

	zap.L().Info("Image planning starting from visual plan",
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

			asset, err := imgService.SearchAndDownload(slug, subject, query, req.Language, nil)
			if err != nil {
				zap.L().Warn("Image search failed", zap.String("subject", subject), zap.Error(err))
				continue
			}

			if asset == nil {
				continue
			}

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
