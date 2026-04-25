package script

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/ml/ollama"
)

type imagePlanItem struct {
	Subject string
	URL     string
	Reason  string
}

func buildImagePlanningSection(req ScriptDocsRequest, narrative string, analysis *ollama.FullEntityAnalysis, stockSection, artlistSection, driveSection ScriptSection, pythonScriptsDir string) ScriptSection {
	subject := pickImageSubject(req.Topic, analysis)
	if subject == "" {
		return ScriptSection{
			Title: "Immagine DDG",
			Body:  "None",
		}
	}

	item := imagePlanItem{
		Subject: subject,
		URL:     searchDDGImage(subject, pythonScriptsDir),
		Reason:  "DDG search from topic/entity terms",
	}

	return ScriptSection{
		Title: "Immagine DDG",
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
	b.WriteString("Immagine: ")
	b.WriteString(item.Subject)
	b.WriteString("\n")
	if strings.TrimSpace(item.URL) != "" {
		b.WriteString("   ")
		b.WriteString(item.URL)
		b.WriteString("\n")
	} else {
		b.WriteString("   None\n")
	}
	b.WriteString("   Note: ")
	b.WriteString(item.Reason)
	b.WriteString("\n")

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

func searchDDGImage(query string, pythonScriptsDir string) string {
	client := &http.Client{Timeout: 10 * time.Second}
	if img := searchWikipediaImage(client, query); img != "" {
		return img
	}
	if img := ddgInstantImage(client, query); img != "" {
		return img
	}
	if img := searchHeadlessImage(query, pythonScriptsDir); img != "" {
		return img
	}
	return ""
}

func searchHeadlessImage(query string, pythonScriptsDir string) string {
	scriptPath := filepath.Join(pythonScriptsDir, "headless_image_scraper.py")
	cmd := exec.Command("python3", scriptPath, query)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	res := strings.TrimSpace(string(out))
	if res == "null" || res == "" {
		return ""
	}
	return res
}

func searchWikipediaImage(client *http.Client, query string) string {
	// Try the original query first
	if img := wikipediaAPISearch(client, query, "en"); img != "" {
		return img
	}
	// Try Italian as fallback if query looks like Italian or just to be safe
	if img := wikipediaAPISearch(client, query, "it"); img != "" {
		return img
	}
	return ""
}

func wikipediaAPISearch(client *http.Client, query string, lang string) string {
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&prop=pageimages&titles=%s&pithumbsize=500&format=json&redirects=1", lang, url.QueryEscape(query))
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "VeloxEditingBot/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var payload struct {
		Query struct {
			Pages map[string]struct {
				Thumbnail struct {
					Source string `json:"source"`
				} `json:"thumbnail"`
			} `json:"pages"`
		} `json:"query"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}

	for _, page := range payload.Query.Pages {
		if page.Thumbnail.Source != "" {
			return page.Thumbnail.Source
		}
	}
	return ""
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
