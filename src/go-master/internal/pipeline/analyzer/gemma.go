package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"velox/go-master/internal/pipeline"
)

type GemmaAnalyzer struct {
	ollamaURL string
	model     string
}

func NewGemmaAnalyzer(url, model string) *GemmaAnalyzer {
	if url == "" {
		url = "http://localhost:11434"
	}
	if model == "" {
		model = "gemma3:4b"
	}
	return &GemmaAnalyzer{ollamaURL: url, model: model}
}

func (a *GemmaAnalyzer) Analyze(ctx context.Context, info *pipeline.VideoInfo, transcript string) ([]pipeline.Highlight, error) {
	prompt := fmt.Sprintf(`Analyze this video transcript and find 3 VIRAL highlights (30-60 sec each).
Title: %s
Transcript: %s

Return JSON array only: [{"start_sec": 10, "duration": 45, "reason": "viral hook"}]`, info.Title, transcript)

	reqBody := map[string]interface{}{
		"model":  a.model,
		"prompt": prompt,
		"stream": false,
	}

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", a.ollamaURL+"/api/generate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var ollamaResp struct {
		Response string `json:"response"`
	}
	json.NewDecoder(resp.Body).Decode(&ollamaResp)

	return a.parseJSON(ollamaResp.Response)
}

func (a *GemmaAnalyzer) parseJSON(resp string) ([]pipeline.Highlight, error) {
	start := strings.Index(resp, "[")
	end := strings.LastIndex(resp, "]")
	if start == -1 || end == -1 {
		return nil, fmt.Errorf("no json array found")
	}

	var highlights []pipeline.Highlight
	if err := json.Unmarshal([]byte(resp[start:end+1]), &highlights); err != nil {
		return nil, err
	}
	return highlights, nil
}
