package script

import (
	"fmt"
	"strings"

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
	subject := pickImageSubject(req.Topic, analysis)
	if subject == "" {
		return ScriptSection{
			Title: "📸 Asset Immagini (Nuovo)",
			Body:  "None",
		}
	}

	item := imagePlanItem{
		Subject: subject,
		Reason:  "Automatic search from topic/entity",
	}

	if imgService != nil {
		slug := strings.ReplaceAll(strings.ToLower(subject), " ", "-")
		asset, err := imgService.SearchAndDownload(slug, subject, subject)
		if err == nil {
			item.URL = asset.SourceURL
			item.Path = asset.PathRel
			item.Reason = "Found and downloaded via Wikipedia/DDG"
		} else {
			item.Reason = fmt.Sprintf("Search failed: %v", err)
		}
	}

	return ScriptSection{
		Title: "📸 Asset Immagini (Nuovo)",
		Body:  renderImagePlans(item, stockSection, artlistSection, driveSection),
	}
}

func pickImageSubject(topic string, analysis *types.FullEntityAnalysis) string {
	seen := make(map[string]struct{})
	add := func(s string) bool {
		s = strings.TrimSpace(s)
		if s == "" {
			return false
		}
		if strings.Count(s, " ") > 1 {
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
		return true
	}

	for _, term := range tokenize(topic) {
		if isStopWord(term) || len(term) < 4 {
			continue
		}
		if add(strings.ToLower(term)) {
			return strings.ToLower(term)
		}
	}

	if analysis != nil {
		for _, segment := range analysis.SegmentEntities {
			for name := range segment.EntitaSenzaTesto {
				if add(name) {
					return name
				}
			}
			for _, name := range segment.NomiSpeciali {
				if add(name) {
					return name
				}
			}
		}
	}

	return ""
}

func renderImagePlans(item imagePlanItem, stockSection, artlistSection, driveSection ScriptSection) string {
	var b strings.Builder
	b.WriteString("Soggetto: ")
	b.WriteString(item.Subject)
	b.WriteString("\n")
	if strings.TrimSpace(item.URL) != "" {
		b.WriteString("   URL:  ")
		b.WriteString(item.URL)
		b.WriteString("\n")
	}
	if item.URL == "" {
		b.WriteString("   URL: None\n")
	}

	if sectionHasContent(stockSection) {
		b.WriteString("\nStock input available.\n")
	}
	if sectionHasContent(artlistSection) {
		b.WriteString("Artlist input available.\n")
	}
	if sectionHasContent(driveSection) {
		b.WriteString("Drive input available.\n")
	}
	return strings.TrimSpace(b.String())
}

func sectionHasContent(section ScriptSection) bool {
	body := strings.TrimSpace(section.Body)
	return body != "" && body != "None"
}
