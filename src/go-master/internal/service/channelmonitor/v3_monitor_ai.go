package channelmonitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// Highlight is the local V3 representation of a clip segment.
type Highlight struct {
	StartSec int
	Duration int
	Reason   string
}

// GemmaResult holds the classification metadata returned by Ollama/Gemma.
type GemmaResult struct {
	Category    string `json:"category"`
	Protagonist string `json:"protagonist"`
	Reason      string `json:"reason"`
}

func (m *V3Monitor) findHighlightsV3(ctx context.Context, title, transcript string) ([]Highlight, error) {
	const maxTranscriptLen = 3000
	if len(transcript) > maxTranscriptLen {
		transcript = transcript[:maxTranscriptLen] + "..."
	}

	prompt := fmt.Sprintf(`You are a YouTube viral moments expert. Analyze this video transcript and find the 3 MOST INTERESTING/VIRAL segments.

Title: "%s"
Transcript with timestamps: "%s"

CRITICAL REQUIREMENTS:
1. Extract timestamps directly from the transcript (look for timing markers like 0:15, 1:23, etc.)
2. EACH segment MUST be between 30-60 seconds duration. NO EXCEPTIONS.
3. Find moments with high engagement potential (surprising twists, peak emotional moments, shocking revelations, climactic points)
4. Prioritize moments where the speaker emphasizes or repeats important words
5. Return ONLY valid JSON array, no explanation text.

JSON format (MUST match exactly):
[
  {"start_sec": <seconds as integer>, "duration": <seconds as integer>, "reason": "<why this is viral>"},
  {"start_sec": <seconds as integer>, "duration": <seconds as integer>, "reason": "<why this is viral>"},
  {"start_sec": <seconds as integer>, "duration": <seconds as integer>, "reason": "<why this is viral>"}
]

Examples of good reasons: "shocking reveal", "peak emotional moment", "surprising plot twist", "viral trend reference"`, title, transcript)

	reqBody := map[string]interface{}{
		"model":  "gemma3:4b",
		"prompt": prompt,
		"stream": false,
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", m.ollamaURL+"/api/generate", bytes.NewReader(reqJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 35 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logger.Warn("Ollama request timeout or connection failed", zap.Error(err))
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var ollamaResp struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	highlights, err := m.parseHighlightsFromGemma(ollamaResp.Response)
	if err != nil {
		logger.Warn("Failed to parse Gemma highlights response",
			zap.String("response", ollamaResp.Response),
			zap.Error(err))
		return nil, fmt.Errorf("invalid highlights response: %w", err)
	}

	var validHighlights []Highlight
	for _, h := range highlights {
		if h.Duration >= 30 && h.Duration <= 60 {
			validHighlights = append(validHighlights, h)
			continue
		}
		logger.Debug("Highlight filtered out (invalid duration)",
			zap.Int("start", h.StartSec),
			zap.Int("duration", h.Duration))
	}

	if len(validHighlights) == 0 {
		return nil, fmt.Errorf("no valid highlights found (all outside 30-60 sec range)")
	}

	logger.Info("Found highlights via Gemma",
		zap.Int("count", len(validHighlights)),
		zap.Ints("start_times", func() []int {
			var times []int
			for _, h := range validHighlights {
				times = append(times, h.StartSec)
			}
			return times
		}()))

	return validHighlights, nil
}

func (m *V3Monitor) parseHighlightsFromGemma(response string) ([]Highlight, error) {
	start := strings.Index(response, "[")
	end := strings.LastIndex(response, "]")
	if start == -1 || end == -1 || start >= end {
		return nil, fmt.Errorf("no JSON array found in response")
	}

	jsonStr := response[start : end+1]

	var rawHighlights []map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &rawHighlights); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	var highlights []Highlight
	for i, raw := range rawHighlights {
		startSec, ok := raw["start_sec"].(float64)
		if !ok {
			logger.Warn("Missing or invalid start_sec in highlight", zap.Int("index", i))
			continue
		}

		duration, ok := raw["duration"].(float64)
		if !ok {
			logger.Warn("Missing or invalid duration in highlight", zap.Int("index", i))
			continue
		}

		reason, _ := raw["reason"].(string)

		highlights = append(highlights, Highlight{
			StartSec: int(startSec),
			Duration: int(duration),
			Reason:   reason,
		})
	}

	return highlights, nil
}

func (m *V3Monitor) classifyWithGemma(ctx context.Context, info interface{}) (*GemmaResult, error) {
	title, description, ok := extractTitleAndDescription(info)
	if !ok {
		return &GemmaResult{Category: "General", Reason: "Unable to extract info"}, nil
	}

	if len(description) > 500 {
		description = description[:500] + "..."
	}

	prompt := fmt.Sprintf(`You are a video content classifier. Analyze this YouTube video and classify it.

Title: "%s"
Description: "%s"

Extract and return JSON with:
1. category: One of [Gaming, Music, Education, Entertainment, Sports, Technology, Lifestyle, News, Other]
2. protagonist: Main person/subject in the video (e.g., "PewDiePie", "Taylor Swift", "Gordon Ramsay"). If not a person, use the topic.
3. reason: One sentence explanation

Return ONLY valid JSON, no other text:
{"category": "<category>", "protagonist": "<name>", "reason": "<explanation>"}`, title, description)

	reqBody := map[string]interface{}{
		"model":  "gemma3:4b",
		"prompt": prompt,
		"stream": false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal classification request: %w", err)
	}

	classCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(classCtx, "POST", m.ollamaURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create classification request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("classification api call failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode classification response: %w", err)
	}

	jsonStart := strings.Index(result.Response, "{")
	jsonEnd := strings.LastIndex(result.Response, "}")
	if jsonStart == -1 || jsonEnd == -1 {
		return &GemmaResult{Category: "General", Reason: "Failed to parse JSON"}, nil
	}

	jsonStr := result.Response[jsonStart : jsonEnd+1]
	var classification GemmaResult
	if err := json.Unmarshal([]byte(jsonStr), &classification); err != nil {
		logger.Warn("Failed to parse classification JSON",
			zap.String("json", jsonStr),
			zap.Error(err))
		return &GemmaResult{Category: "General", Reason: "JSON parse error"}, nil
	}

	return &GemmaResult{
		Category:    classification.Category,
		Protagonist: classification.Protagonist,
		Reason:      classification.Reason,
	}, nil
}

func (m *V3Monitor) fallbackHighlights(transcript string) []Highlight {
	keywords := []string{"killed", "died", "arrest", "win", "lose", "first", "never"}
	var highlights []Highlight

	lines := strings.Split(transcript, ".")
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				startSec := i * 30
				highlights = append(highlights, Highlight{
					StartSec: startSec,
					Duration: 45,
					Reason:   "Keyword match: " + kw,
				})
				break
			}
		}
		if len(highlights) >= 3 {
			break
		}
	}

	if len(highlights) == 0 {
		highlights = append(highlights, Highlight{
			StartSec: 0,
			Duration: 45,
			Reason:   "Default (no keywords found)",
		})
	}

	logger.Info("Using fallback highlights", zap.Int("count", len(highlights)))
	return highlights
}

func (m *V3Monitor) checkOllamaHealth(ctx context.Context) error {
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(healthCtx, "GET", m.ollamaURL+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("create ollama health check request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama service unavailable at %s: %w", m.ollamaURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return fmt.Errorf("parse ollama tags response: %w", err)
	}

	for _, model := range tagsResp.Models {
		if model.Name == "gemma3:4b" {
			logger.Info("Ollama health check passed",
				zap.String("service", m.ollamaURL),
				zap.String("model", "gemma3:4b"))
			return nil
		}
	}

	return fmt.Errorf("ollama running but gemma3:4b model not found")
}

func extractTitleAndDescription(info interface{}) (string, string, bool) {
	switch v := info.(type) {
	case map[string]interface{}:
		title, _ := v["title"].(string)
		description, _ := v["description"].(string)
		return title, description, title != "" || description != ""
	default:
		val := reflect.ValueOf(info)
		if val.Kind() == reflect.Ptr {
			val = val.Elem()
		}
		if !val.IsValid() || val.Kind() != reflect.Struct {
			return "", "", false
		}
		titleField := val.FieldByName("Title")
		descField := val.FieldByName("Description")
		if !titleField.IsValid() || !descField.IsValid() {
			return "", "", false
		}
		title, _ := titleField.Interface().(string)
		description, _ := descField.Interface().(string)
		return title, description, title != "" || description != ""
	}
}
