package channelmonitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// classifyEntity uses Ollama to classify a video title into a category
func (m *Monitor) classifyEntity(ctx context.Context, title, protagonist string) (string, string, error) {
	candidates := m.categoryChoices(ctx)
	prompt := fmt.Sprintf(`You are a strict content router.
Choose exactly ONE category from this list: %s

Definitions:
- Boxe: boxing, fighters, sparring, press conference, weigh-in, bout, ring.
- Wwe: WWE/wrestling shows, wrestlers, RAW/SmackDown/PPV.
- Music: songs, albums, performances, music artists (including rappers), artist interviews.
- HipHop: hip-hop culture/news/scene in general (not mainly one artist's music interview).
- Various: mixed topics, uncategorized content, or content that doesn't fit the others.
- Crime: crime stories, arrests, gangs, investigations, court crime topics.
- Discovery: documentaries, science, education, nature, general knowledge.

Rule: if the main person is a rapper/singer/music artist, prefer Music.
Return JSON only: {"category":"<one from list>","reason":"<max 12 words>"}.

Title: "%s"
Protagonist: "%s"`, strings.Join(candidates, ", "), title, protagonist)

	reqBody := map[string]interface{}{
		"model":  "gemma3:4b",
		"prompt": prompt,
		"stream": false,
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", m.ollamaURL+"/api/generate", bytes.NewReader(reqJSON))
	if err != nil {
		return "", "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	var ollamaResp struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", "", fmt.Errorf("failed to decode response: %w", err)
	}

	category, reason := parseCategoryFromGemmaResponse(ollamaResp.Response, candidates)
	if category == "" {
		return "", "", fmt.Errorf("invalid category from model: %q", strings.TrimSpace(ollamaResp.Response))
	}
	category = applyCategoryGuardrails(category, title, protagonist)
	if normalized := fuzzyMatchFolder(category); normalized != "" {
		category = normalized
	}

	logger.Debug("Ollama classification result",
		zap.String("title", title),
		zap.String("protagonist", protagonist),
		zap.String("category", category),
	)

	return category, reason, nil
}

func parseCategoryFromGemmaResponse(raw string, candidates []string) (string, string) {
	candidateSet := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		candidateSet[c] = true
	}

	var payload struct {
		Category string `json:"category"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &payload); err == nil {
		cat := strings.TrimSpace(payload.Category)
		if cat != "" {
			if canonical := fuzzyMatchFolder(cat); canonical != "" && candidateSet[canonical] {
				return canonical, strings.TrimSpace(payload.Reason)
			}
			if candidateSet[cat] {
				return cat, strings.TrimSpace(payload.Reason)
			}
		}
	}

	clean := strings.TrimSpace(raw)
	clean = strings.Trim(clean, `"'.,;: `)
	if canonical := fuzzyMatchFolder(clean); canonical != "" && candidateSet[canonical] {
		return canonical, ""
	}

	lower := strings.ToLower(raw)
	for _, c := range candidates {
		if strings.Contains(lower, strings.ToLower(c)) {
			return c, ""
		}
	}
	return "", ""
}

func (m *Monitor) categoryChoices(ctx context.Context) []string {
	choices := append([]string{}, monitorDefaultCategories...)
	if m.driveClient == nil {
		return choices
	}
	clipRootID := strings.TrimSpace(m.config.ClipRootID)
	if clipRootID == "" {
		if id, err := m.findClipRootNoCreate(ctx); err == nil && strings.TrimSpace(id) != "" {
			clipRootID = id
		}
	}
	if clipRootID == "" {
		return choices
	}
	folders, err := m.driveClient.ListFoldersNoRecursion(ctx, drive.ListFoldersOptions{
		ParentID: clipRootID,
		MaxItems: 100,
	})
	if err != nil {
		return choices
	}
	set := make(map[string]bool, len(choices))
	for _, c := range choices {
		set[c] = true
	}
	for _, f := range folders {
		if canonical := fuzzyMatchFolder(f.Name); canonical != "" {
			set[canonical] = true
		}
	}
	out := make([]string, 0, len(set))
	for _, c := range monitorDefaultCategories {
		if set[c] {
			out = append(out, c)
		}
	}
	return out
}

func fallbackCategory(title, protagonist string) string {
	titleLower := strings.ToLower(title)
	protagonistLower := strings.ToLower(protagonist)

	boxeTerms := []string{"boxing", "boxe", "fight", "fighter", "ring", "weigh", "mayweather", "gervonta", "tyson"}
	wweTerms := []string{"wwe", "wrestling", "raw", "smackdown", "royal rumble", "wrestlemania", "roman reigns"}
	crimeTerms := []string{"crime", "murder", "arrest", "mafia", "gang", "cartel", "court case", "investigation"}
	musicTerms := []string{"song", "album", "official video", "lyrics", "live", "concert", "music", "rapper", "feat", "ft"}
	discoveryTerms := []string{"documentary", "science", "history", "nature", "education", "discovery"}

	if containsAny(titleLower, wweTerms) {
		return "Wwe"
	}
	if containsAny(titleLower, boxeTerms) {
		return "Boxe"
	}
	if containsAny(titleLower, crimeTerms) {
		return "Crime"
	}
	if containsAny(titleLower, musicTerms) || isLikelyMusicEntity(protagonistLower) {
		return "Music"
	}
	if containsAny(titleLower, discoveryTerms) {
		return "Discovery"
	}
	return "Various"
}

func applyCategoryGuardrails(category, title, protagonist string) string {
	canonical := category
	if normalized := fuzzyMatchFolder(category); normalized != "" {
		canonical = normalized
	}
	if canonical == "HipHop" {
		titleLower := strings.ToLower(title)
		if strings.Contains(titleLower, "interview") || strings.Contains(titleLower, "podcast") || strings.Contains(titleLower, "talk") {
			if isLikelyMusicEntity(strings.ToLower(protagonist)) {
				return "Music"
			}
		}
	}
	if canonical == "Various" {
		return "Various"
	}
	return canonical
}

func classificationConfidence(category, source string) float64 {
	switch source {
	case "override":
		return 1.0
	case "gemma":
		if category == "Various" {
			return 0.45
		}
		return 0.86
	case "fallback":
		if category == "Various" {
			return 0.35
		}
		return 0.60
	default:
		return 0.50
	}
}

func containsAny(text string, terms []string) bool {
	for _, t := range terms {
		if strings.Contains(text, t) {
			return true
		}
	}
	return false
}

func isLikelyMusicEntity(name string) bool {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return false
	}
	knownArtists := []string{
		"50 cent", "eminem", "drake", "kanye west", "jay z", "kendrick lamar",
		"travis scott", "rihanna", "beyonce", "taylor swift", "nicki minaj",
	}
	for _, artist := range knownArtists {
		if strings.Contains(name, artist) || strings.Contains(artist, name) {
			return true
		}
	}
	return false
}

// fuzzyMatchFolder maps a category string to a known canonical category
func fuzzyMatchFolder(category string) string {
	normalized := strings.ToLower(strings.TrimSpace(category))

	if canonical, ok := knownCategories[normalized]; ok {
		return canonical
	}

	for key, canonical := range knownCategories {
		if strings.Contains(normalized, key) || strings.Contains(key, normalized) {
			return canonical
		}
	}

	for _, canonical := range knownCategories {
		if strings.EqualFold(category, canonical) {
			return canonical
		}
	}

	return ""
}
