// Package ollama provides Ollama API integration for Agent 5.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// Client client per Ollama API
type Client struct {
	baseURL    string
	httpClient *http.Client
	model      string
}

// NewClient crea un nuovo client Ollama
func NewClient(baseURL, model string) *Client {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "gemma3:12b"
	}

	return &Client{
		baseURL: baseURL,
		model:   model,
		httpClient: &http.Client{
			Timeout: 120 * time.Second, // Timeout aumentato per modelli grandi
		},
	}
}

// Message rappresenta un messaggio chat
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest richiesta chat
type ChatRequest struct {
	Model    string                 `json:"model"`
	Messages []Message              `json:"messages"`
	Stream   bool                   `json:"stream"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

// ChatResponse risposta chat
type ChatResponse struct {
	Message Message `json:"message"`
	Done    bool    `json:"done"`
}

// GenerateRequest richiesta generazione (Legacy API)
type GenerateRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Context []int                  `json:"context,omitempty"`
	Stream  bool                   `json:"stream"`
	Options map[string]interface{} `json:"options,omitempty"`
}

// GenerateResponse risposta generazione (Legacy API)
type GenerateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
	Context  []int  `json:"context,omitempty"`
}

// EmbedRequest richiesta embedding
type EmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// EmbedResponse risposta embedding
type EmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

// Chat esegue una richiesta chat a Ollama (API Raccomandata)
func (c *Client) Chat(ctx context.Context, messages []Message, options map[string]interface{}) (string, error) {
	req := ChatRequest{
		Model:    c.model,
		Messages: messages,
		Stream:   false,
		Options:  options,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama chat returned status %d", resp.StatusCode)
	}

	var result ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	logger.Info("Ollama chat response received",
		zap.Int("chars", len(result.Message.Content)),
		zap.Int("words", len(strings.Fields(result.Message.Content))),
	)

	return result.Message.Content, nil
}

// Embed genera embedding vettoriale con Ollama
func (c *Client) Embed(ctx context.Context, prompt string) ([]float32, error) {
	req := EmbedRequest{
		Model:  c.model,
		Prompt: prompt,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var result EmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Embedding, nil
}

// GenerateStream genera testo con Ollama in modalità streaming
func (c *Client) GenerateStream(ctx context.Context, prompt string) (<-chan string, <-chan error) {
	return c.GenerateStreamWithOptions(ctx, c.model, prompt, nil)
}

// GenerateStreamWithOptions genera testo con opzioni esplicite in modalità streaming.
func (c *Client) GenerateStreamWithOptions(ctx context.Context, model, prompt string, options map[string]interface{}) (<-chan string, <-chan error) {
	textChan := make(chan string, 100)
	errChan := make(chan error, 1)

	if model == "" {
		model = c.model
	}

	req := GenerateRequest{
		Model:   model,
		Prompt:  prompt,
		Stream:  true,
		Options: options,
	}

	body, err := json.Marshal(req)
	if err != nil {
		errChan <- fmt.Errorf("failed to marshal request: %w", err)
		close(textChan)
		close(errChan)
		return textChan, errChan
	}

	go func() {
		defer close(textChan)
		defer close(errChan)

		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/generate", bytes.NewReader(body))
		if err != nil {
			errChan <- err
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			errChan <- fmt.Errorf("ollama request failed: %w", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			errChan <- fmt.Errorf("ollama returned status %d", resp.StatusCode)
			return
		}

		decoder := json.NewDecoder(resp.Body)
		for {
			var result GenerateResponse
			if err := decoder.Decode(&result); err != nil {
				if err.Error() == "EOF" {
					break
				}
				errChan <- fmt.Errorf("failed to decode streaming response: %w", err)
				return
			}

			if result.Response != "" {
				textChan <- result.Response
			}

			if result.Done {
				break
			}
		}
	}()

	return textChan, errChan
}

// Generate genera testo con Ollama (Legacy API)
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	return c.GenerateWithOptions(ctx, c.model, prompt, nil)
}

// GenerateWithOptions genera testo con opzioni esplicite (Legacy API)
func (c *Client) GenerateWithOptions(ctx context.Context, model, prompt string, options map[string]interface{}) (string, error) {
	if model == "" {
		model = c.model
	}

	req := GenerateRequest{
		Model:   model,
		Prompt:  prompt,
		Stream:  false,
		Options: options,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var result GenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Response, nil
}

// CheckHealth verifica se Ollama è raggiungibile
func (c *Client) CheckHealth(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// ListModelsResponse risposta lista modelli
type ListModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

// ModelInfo info su un modello
type ModelInfo struct {
	Name       string `json:"name"`
	ModifiedAt string `json:"modified_at"`
	Size       int64  `json:"size"`
}

// ListModels restituisce la lista dei modelli disponibili
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var result ListModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Models, nil
}

// EntityExtractionRequest represents a request to extract entities from a segment
type EntityExtractionRequest struct {
	SegmentText  string `json:"segment_text"`
	SegmentIndex int    `json:"segment_index"`
	EntityCount  int    `json:"entity_count"`
}

// EntityExtractionResult represents the result of entity extraction for a segment
type EntityExtractionResult struct {
	SegmentIndex     int               `json:"segment_index"`
	FrasiImportanti  []string          `json:"frasi_importanti"`
	EntitaSenzaTesto map[string]string `json:"entity_senza_testo"`
	NomiSpeciali     []string          `json:"nomi_speciali"`
	ParoleImportanti []string          `json:"parole_importanti"`
}

// ExtractEntitiesFromSegment extracts entities from a single text segment using Ollama
func (c *Client) ExtractEntitiesFromSegment(ctx context.Context, req EntityExtractionRequest) (*EntityExtractionResult, error) {
	entityCount := req.EntityCount
	if entityCount <= 0 {
		entityCount = 12
	}

	// Build the entity extraction prompt
	prompt := buildEntityExtractionPrompt(req.SegmentText, entityCount)

	// Call Ollama using legacy generate for JSON tasks (often more stable for JSON)
	response, err := c.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("entity extraction failed: %w", err)
	}

	// Parse JSON response
	result, err := parseEntityExtractionResult(response, req.SegmentIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to parse entity result: %w", err)
	}

	return result, nil
}

// ExtractEntitiesFromScript extracts entities from all segments of a script
func (c *Client) ExtractEntitiesFromScript(ctx context.Context, segments []string, entityCount int) (*FullEntityAnalysis, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("no segments provided")
	}

	if entityCount <= 0 {
		entityCount = 12
	}

	analysis := &FullEntityAnalysis{
		TotalSegments:         len(segments),
		SegmentEntities:       make([]SegmentEntities, 0, len(segments)),
		EntityCountPerSegment: entityCount,
	}

	// Extract entities for each segment
	for i, segment := range segments {
		req := EntityExtractionRequest{
			SegmentText:  segment,
			SegmentIndex: i,
			EntityCount:  entityCount,
		}

		result, err := c.ExtractEntitiesFromSegment(ctx, req)
		if err != nil {
			// Continue with empty entities for this segment
			result = &EntityExtractionResult{
				SegmentIndex:     i,
				FrasiImportanti:  []string{},
				EntitaSenzaTesto: make(map[string]string),
				NomiSpeciali:     []string{},
				ParoleImportanti: []string{},
			}
		}

		segmentEntities := SegmentEntities{
			SegmentIndex:     i,
			SegmentText:      segment,
			FrasiImportanti:  result.FrasiImportanti,
			EntitaSenzaTesto: result.EntitaSenzaTesto,
			NomiSpeciali:     result.NomiSpeciali,
			ParoleImportanti: result.ParoleImportanti,
		}

		analysis.SegmentEntities = append(analysis.SegmentEntities, segmentEntities)

		// Count total entities
		analysis.TotalEntities += len(result.FrasiImportanti) +
			len(result.EntitaSenzaTesto) +
			len(result.NomiSpeciali) +
			len(result.ParoleImportanti)
	}

	return analysis, nil
}

// parseEntityExtractionResult parses the JSON response from Ollama
func parseEntityExtractionResult(response string, segmentIndex int) (*EntityExtractionResult, error) {
	jsonStr := response

	// Remove markdown code blocks if present
	if len(jsonStr) > 7 && jsonStr[:7] == "```json" {
		end := len(jsonStr) - 3
		if end > 7 {
			jsonStr = jsonStr[7:end]
		}
	} else if len(jsonStr) > 3 && jsonStr[:3] == "```" {
		end := len(jsonStr) - 3
		if end > 3 {
			jsonStr = jsonStr[3:end]
		}
	}

	var raw struct {
		FrasiImportanti  []string          `json:"frasi_importanti"`
		EntitaSenzaTesto map[string]string `json:"entity_senza_testo"`
		NomiSpeciali     []string          `json:"nomi_speciali"`
		ParoleImportanti []string          `json:"parole_importanti"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	// Ensure slices are not nil
	if raw.FrasiImportanti == nil {
		raw.FrasiImportanti = []string{}
	}
	if raw.NomiSpeciali == nil {
		raw.NomiSpeciali = []string{}
	}
	if raw.ParoleImportanti == nil {
		raw.ParoleImportanti = []string{}
	}
	if raw.EntitaSenzaTesto == nil {
		raw.EntitaSenzaTesto = make(map[string]string)
	}

	return &EntityExtractionResult{
		SegmentIndex:     segmentIndex,
		FrasiImportanti:  raw.FrasiImportanti,
		EntitaSenzaTesto: raw.EntitaSenzaTesto,
		NomiSpeciali:     raw.NomiSpeciali,
		ParoleImportanti: raw.ParoleImportanti,
	}, nil
}

// SegmentEntities represents extracted entities for a single segment
type SegmentEntities struct {
	SegmentIndex     int               `json:"segment_index"`
	SegmentText      string            `json:"segment_text"`
	FrasiImportanti  []string          `json:"frasi_importanti"`
	EntitaSenzaTesto map[string]string `json:"entity_senza_testo"`
	NomiSpeciali     []string          `json:"nomi_speciali"`
	ParoleImportanti []string          `json:"parole_importanti"`
}

// FullEntityAnalysis represents the complete entity analysis for a script
type FullEntityAnalysis struct {
	TotalSegments         int               `json:"total_segments"`
	SegmentEntities       []SegmentEntities `json:"segment_entities"`
	TotalEntities         int               `json:"total_entities"`
	EntityCountPerSegment int               `json:"entity_count_per_segment"`
}
