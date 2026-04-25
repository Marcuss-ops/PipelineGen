package script

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"velox/go-master/internal/ml/ollama"
)

type imagePlanItem struct {
	Subject string
	URL     string
	Reason  string
}

func buildImagePlanningSection(req ScriptDocsRequest, narrative string, analysis *ollama.FullEntityAnalysis, stockSection, artlistSection, driveSection ScriptSection) ScriptSection {
	subject := pickImageSubject(req.Topic, analysis)
	if subject == "" {
		return ScriptSection{
			Title: "🖼️ Immagine DDG",
			Body:  "None",
		}
	}

	item := imagePlanItem{
		Subject: subject,
		URL:     searchDDGImage(subject),
		Reason:  "DDG search from topic/entity terms",
	}

	return ScriptSection{
		Title: "🖼️ Immagine DDG",
		Body:  renderImagePlans(item, stockSection, artlistSection, driveSection),
	}
}

func pickImageSubject(topic string, analysis *ollama.FullEntityAnalysis) string {
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
	b.WriteString("🖼️ ")
	b.WriteString(item.Subject)
	b.WriteString("\n")
	if strings.TrimSpace(item.URL) != "" {
		b.WriteString("   🔗 ")
		b.WriteString(item.URL)
		b.WriteString("\n")
	} else {
		b.WriteString("   ❌ None\n")
	}
	b.WriteString("   ✨ ")
	b.WriteString(item.Reason)
	b.WriteString("\n")

	if sectionHasContent(stockSection) {
		b.WriteString("\n📦 Stock input available.\n")
	}
	if sectionHasContent(artlistSection) {
		b.WriteString("🎞️ Artlist input available.\n")
	}
	if sectionHasContent(driveSection) {
		b.WriteString("📁 Drive input available.\n")
	}
	return strings.TrimSpace(b.String())
}

func sectionHasContent(section ScriptSection) bool {
	body := strings.TrimSpace(section.Body)
	return body != "" && body != "None"
}

func searchDDGImage(query string) string {
	client := &http.Client{Timeout: 10 * time.Second}
	if img := ddgImageSearch(client, query); img != "" {
		return img
	}
	return ddgInstantImage(client, query)
}

func ddgImageSearch(client *http.Client, query string) string {
	apiURL := fmt.Sprintf("https://duckduckgo.com/i.js?q=%s", url.QueryEscape(query))
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://duckduckgo.com/")

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var payload struct {
		Results []struct {
			Image string `json:"image"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	if len(payload.Results) == 0 {
		return ""
	}
	return strings.TrimSpace(payload.Results[0].Image)
}

func ddgInstantImage(client *http.Client, query string) string {
	apiURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_redirect=1", url.QueryEscape(query))
	resp, err := client.Get(apiURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var payload struct {
		Image string `json:"Image"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Image)
}
