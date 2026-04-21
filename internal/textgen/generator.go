// Package textgen provides AI text generation with NVIDIA GPU acceleration
// This service orchestrates Ollama/OpenAI/Groq calls with GPU resource management
package textgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"velox/go-master/internal/gpu"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Provider represents an AI text generation backend
type Provider string

const (
	ProviderOllama  Provider = "ollama"
	ProviderOpenAI  Provider = "openai"
	ProviderGroq    Provider = "groq"
)

// GenerationRequest contains parameters for text generation
type GenerationRequest struct {
	Provider     Provider        `json:"provider"`
	Model        string          `json:"model"`
	Prompt       string          `json:"prompt"`
	SystemPrompt string          `json:"system_prompt"`
	Temperature  float64         `json:"temperature"`
	MaxTokens    int             `json:"max_tokens"`
	TopP         float64         `json:"top_p"`
	Stream       bool            `json:"stream"`
	UseGPU       bool            `json:"use_gpu"` // Request GPU acceleration
}

// GenerationResult contains the generated text and metadata
type GenerationResult struct {
	Text         string        `json:"text"`
	Provider     Provider      `json:"provider"`
	Model        string        `json:"model"`
	GPUUsed      bool          `json:"gpu_used"`
	GPUDevice    int           `json:"gpu_device,omitempty"`
	TokensUsed   int           `json:"tokens_used"`
	Duration     time.Duration `json:"duration"`
	CreatedAt    time.Time     `json:"created_at"`
}

// ScriptType represents different types of scripts to generate
type ScriptType string

const (
	ScriptYouTube     ScriptType = "youtube"
	ScriptInterview   ScriptType = "interview"
	ScriptHighlight   ScriptType = "highlight"
	ScriptPromo       ScriptType = "promo"
	ScriptEducational ScriptType = "educational"
)

// ScriptRequest contains parameters for script generation
type ScriptRequest struct {
	Type         ScriptType `json:"type"`
	Topic        string     `json:"topic"`
	TargetLength int        `json:"target_length"` // Target word count
	Language     string     `json:"language"`
	Tone         string     `json:"tone"` // professional, casual, energetic, etc.
	Keywords     []string   `json:"keywords"`
	Structure    []string   `json:"structure"` // intro, main, conclusion, etc.
	UseGPU       bool       `json:"use_gpu"`
}

// ScriptResult contains the generated script
type ScriptResult struct {
	Script         string        `json:"script"`
	WordCount      int           `json:"word_count"`
	Type           ScriptType    `json:"type"`
	Sections       []ScriptSection `json:"sections"`
	GPUUsed        bool          `json:"gpu_used"`
	Duration       time.Duration `json:"duration"`
}

// ScriptSection represents a section of the generated script
type ScriptSection struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	StartTime string `json:"start_time,omitempty"`
	EndTime   string `json:"end_time,omitempty"`
}

// Generator orchestrates AI text generation with GPU support
type Generator struct {
	gpuManager *gpu.Manager
	config     *GeneratorConfig
	httpClient *http.Client
}

// GeneratorConfig holds configuration for the text generator
type GeneratorConfig struct {
	DefaultProvider    Provider       `json:"default_provider"`
	DefaultModel       string         `json:"default_model"`
	DefaultTemperature float64       `json:"default_temperature"`
	DefaultMaxTokens   int           `json:"default_max_tokens"`
	MaxRetries         int           `json:"max_retries"`
	Timeout            time.Duration `json:"timeout"`
	GPUSupported       bool          `json:"gpu_supported"`

	// Provider endpoints
	OllamaURL  string `json:"ollama_url"`
	OpenAIKey  string `json:"openai_key"`
	GroqKey    string `json:"groq_key"`
	GroqURL    string `json:"groq_url"`
}

// NewGenerator creates a new text generator
func NewGenerator(gpuMgr *gpu.Manager, config *GeneratorConfig) *Generator {
	if config == nil {
		config = &GeneratorConfig{
			DefaultProvider:    ProviderOllama,
			DefaultModel:       "llama2",
			DefaultTemperature: 0.7,
			DefaultMaxTokens:   2048,
			MaxRetries:         3,
			Timeout:            5 * time.Minute,
			GPUSupported:       true,
		}
	}

	// Populate API keys from environment if not set in config
	if config.OllamaURL == "" {
		config.OllamaURL = os.Getenv("OLLAMA_ADDR")
		if config.OllamaURL == "" {
			config.OllamaURL = "http://localhost:11434"
		}
	}
	if config.OpenAIKey == "" {
		config.OpenAIKey = os.Getenv("OPENAI_API_KEY")
	}
	if config.GroqKey == "" {
		config.GroqKey = os.Getenv("GROQ_API_KEY")
	}
	if config.GroqURL == "" {
		config.GroqURL = "https://api.groq.com/openai/v1"
	}

	return &Generator{
		gpuManager: gpuMgr,
		config:     config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}
}

// GenerateText generates text using the specified provider
func (g *Generator) GenerateText(ctx context.Context, req *GenerationRequest) (*GenerationResult, error) {
	if req.Temperature == 0 {
		req.Temperature = g.config.DefaultTemperature
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = g.config.DefaultMaxTokens
	}
	
	startTime := time.Now()
	
	// Check if GPU should be used
	gpuUsed := false
	gpuDevice := -1
	
	if req.UseGPU && g.config.GPUSupported && g.gpuManager != nil {
		if g.gpuManager.IsHealthy(ctx) {
			gpuUsed = true
			gpuInfo, _ := g.gpuManager.GetSelectedGPU()
			if gpuInfo != nil {
				gpuDevice = gpuInfo.Index
			}
			logger.Info("Using GPU for text generation",
				zap.Int("device", gpuDevice),
			)
		} else {
			logger.Warn("GPU unhealthy, falling back to CPU")
		}
	}
	
	// Generate text based on provider
	var text string
	var err error
	
	switch req.Provider {
	case ProviderOllama:
		text, err = g.generateWithOllama(ctx, req, gpuUsed)
	case ProviderOpenAI:
		text, err = g.generateWithOpenAI(ctx, req)
	case ProviderGroq:
		text, err = g.generateWithGroq(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", req.Provider)
	}
	
	if err != nil {
		return nil, fmt.Errorf("generation failed: %w", err)
	}
	
	result := &GenerationResult{
		Text:      text,
		Provider:  req.Provider,
		Model:     req.Model,
		GPUUsed:   gpuUsed,
		GPUDevice: gpuDevice,
		Duration:  time.Since(startTime),
		CreatedAt: startTime,
	}
	
	logger.Info("Text generation completed",
		zap.String("provider", string(req.Provider)),
		zap.String("model", req.Model),
		zap.Bool("gpu_used", gpuUsed),
		zap.Duration("duration", result.Duration),
		zap.Int("text_length", len(text)),
	)
	
	return result, nil
}

// GenerateScript generates a structured script for video creation
func (g *Generator) GenerateScript(ctx context.Context, req *ScriptRequest) (*ScriptResult, error) {
	if req.TargetLength == 0 {
		req.TargetLength = 500 // Default 500 words
	}
	if req.Language == "" {
		req.Language = "en"
	}
	if req.Tone == "" {
		req.Tone = "professional"
	}
	
	startTime := time.Now()
	
	// Build system prompt based on script type
	systemPrompt := g.buildScriptSystemPrompt(req)
	
	// Build user prompt
	userPrompt := g.buildScriptUserPrompt(req)
	
	// Generate the script
	genReq := &GenerationRequest{
		Provider:     g.config.DefaultProvider,
		Model:        g.config.DefaultModel,
		Prompt:       userPrompt,
		SystemPrompt: systemPrompt,
		Temperature:  0.7,
		MaxTokens:    req.TargetLength * 2, // Approximate tokens needed
		UseGPU:       req.UseGPU,
	}
	
	result, err := g.GenerateText(ctx, genReq)
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}
	
	// Parse sections (simple parsing by paragraphs)
	sections := g.parseScriptSections(result.Text)
	
	wordCount := len(strings.Fields(result.Text))
	
	scriptResult := &ScriptResult{
		Script:    result.Text,
		WordCount: wordCount,
		Type:      req.Type,
		Sections:  sections,
		GPUUsed:   result.GPUUsed,
		Duration:  time.Since(startTime),
	}
	
	logger.Info("Script generation completed",
		zap.String("type", string(req.Type)),
		zap.String("topic", req.Topic),
		zap.Int("word_count", wordCount),
		zap.Bool("gpu_used", result.GPUUsed),
		zap.Duration("duration", scriptResult.Duration),
	)
	
	return scriptResult, nil
}

// generateWithOllama generates text using Ollama API
func (g *Generator) generateWithOllama(ctx context.Context, req *GenerationRequest, gpuEnabled bool) (string, error) {
	logger.Info("Generating with Ollama",
		zap.String("model", req.Model),
		zap.Bool("gpu_enabled", gpuEnabled),
	)

	url := g.config.OllamaURL + "/api/generate"
	ollamaReq := map[string]interface{}{
		"model":  req.Model,
		"prompt": req.Prompt,
		"stream": false,
		"options": map[string]interface{}{
			"temperature": req.Temperature,
			"num_predict":  req.MaxTokens,
		},
	}
	if req.SystemPrompt != "" {
		ollamaReq["system"] = req.SystemPrompt
	}

	return g.doJSONPost(ctx, url, ollamaReq, "response")
}

// generateWithOpenAI generates text using OpenAI Chat Completions API
func (g *Generator) generateWithOpenAI(ctx context.Context, req *GenerationRequest) (string, error) {
	if g.config.OpenAIKey == "" {
		return "", fmt.Errorf("OpenAI API key not configured: set OPENAI_API_KEY env var")
	}

	logger.Info("Generating with OpenAI",
		zap.String("model", req.Model),
	)

	url := "https://api.openai.com/v1/chat/completions"
	return g.doChatCompletion(ctx, url, g.config.OpenAIKey, req)
}

// generateWithGroq generates text using Groq Chat Completions API
func (g *Generator) generateWithGroq(ctx context.Context, req *GenerationRequest) (string, error) {
	if g.config.GroqKey == "" {
		return "", fmt.Errorf("Groq API key not configured: set GROQ_API_KEY env var")
	}

	logger.Info("Generating with Groq",
		zap.String("model", req.Model),
	)

	url := g.config.GroqURL + "/chat/completions"
	return g.doChatCompletion(ctx, url, g.config.GroqKey, req)
}

// buildScriptSystemPrompt creates the system prompt for script generation
func (g *Generator) buildScriptSystemPrompt(req *ScriptRequest) string {
	prompt := fmt.Sprintf(`You are an expert script writer for %s videos. 

Guidelines:
- Write engaging, professional content
- Target length: approximately %d words
- Language: %s
- Tone: %s
- Structure the script with clear sections`,
		req.Type,
		req.TargetLength,
		req.Language,
		req.Tone,
	)
	
	if len(req.Keywords) > 0 {
		prompt += fmt.Sprintf("\n- Include these keywords naturally: %s", strings.Join(req.Keywords, ", "))
	}
	
	prompt += "\n\nFormat your response with clear section breaks using ===SECTION: [section name]=== markers."
	
	return prompt
}

// buildScriptUserPrompt creates the user prompt for script generation
func (g *Generator) buildScriptUserPrompt(req *ScriptRequest) string {
	prompt := fmt.Sprintf("Create a %s script about: %s\n\n", req.Type, req.Topic)
	
	if len(req.Structure) > 0 {
		prompt += fmt.Sprintf("Please structure it with these sections: %s\n\n", strings.Join(req.Structure, ", "))
	}
	
	prompt += "Make it engaging and professional."
	
	return prompt
}

// parseScriptSections parses the generated script into sections
func (g *Generator) parseScriptSections(script string) []ScriptSection {
	var sections []ScriptSection
	
	// Split by section markers
	parts := strings.Split(script, "===SECTION:")
	if len(parts) == 1 {
		// No section markers found, treat entire text as one section
		return []ScriptSection{
			{Name: "Main", Content: script},
		}
	}
	
	for _, part := range parts[1:] { // Skip first empty part
		lines := strings.SplitN(part, "\n", 2)
		if len(lines) < 2 {
			continue
		}
		
		sectionName := strings.TrimSpace(strings.Split(lines[0], "===")[0])
		content := strings.TrimSpace(lines[1])
		
		sections = append(sections, ScriptSection{
			Name:    sectionName,
			Content: content,
		})
	}
	
	if len(sections) == 0 {
		sections = append(sections, ScriptSection{
			Name: "Main",
			Content: script,
		})
	}
	
	return sections
}

// GetGPUStatus returns current GPU status
func (g *Generator) GetGPUStatus(ctx context.Context) map[string]interface{} {
	if g.gpuManager == nil {
		return map[string]interface{}{
			"gpu_available": false,
			"reason":        "GPU manager not configured",
		}
	}

	gpu, err := g.gpuManager.GetSelectedGPU()
	if err != nil {
		return map[string]interface{}{
			"gpu_available": false,
			"error":         err.Error(),
		}
	}

	return map[string]interface{}{
		"gpu_available": true,
		"gpu_info":      gpu,
		"is_healthy":    g.gpuManager.IsHealthy(ctx),
	}
}

// doChatCompletion sends a request to an OpenAI-compatible Chat Completions API.
// Used by both OpenAI and Groq since they share the same API format.
func (g *Generator) doChatCompletion(ctx context.Context, url, apiKey string, req *GenerationRequest) (string, error) {
	messages := []map[string]string{}
	if req.SystemPrompt != "" {
		messages = append(messages, map[string]string{"role": "system", "content": req.SystemPrompt})
	}
	messages = append(messages, map[string]string{"role": "user", "content": req.Prompt})

	body := map[string]interface{}{
		"model":       req.Model,
		"messages":    messages,
		"temperature": req.Temperature,
		"max_tokens":  req.MaxTokens,
		"stream":      false,
	}
	if req.TopP > 0 {
		body["top_p"] = req.TopP
	}

	return g.doJSONPostWithAuth(ctx, url, "Bearer "+apiKey, body, "choices.0.message.content")
}

// doJSONPost sends a JSON POST request and extracts a field from the response.
// responseKey uses dot notation to navigate nested JSON (e.g., "choices.0.message.content").
func (g *Generator) doJSONPost(ctx context.Context, url string, payload interface{}, responseKey string) (string, error) {
	return g.doJSONPostWithAuth(ctx, url, "", payload, responseKey)
}

// doJSONPostWithAuth sends a JSON POST with optional Authorization header.
func (g *Generator) doJSONPostWithAuth(ctx context.Context, url, authHeader string, payload interface{}, responseKey string) (string, error) {
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response and extract the requested key
	var result interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	return extractJSONKey(result, responseKey)
}

// extractJSONKey navigates a nested JSON structure using dot notation.
// Numeric keys (e.g., "0") are treated as array indices.
func extractJSONKey(data interface{}, key string) (string, error) {
	parts := strings.Split(key, ".")
	current := data

	for _, part := range parts {
		if current == nil {
			return "", fmt.Errorf("key %q not found in response", key)
		}

		// Try array index first
		var i int
		if n, err := fmt.Sscanf(part, "%d", &i); err == nil && n == 1 {
			arr, ok := current.([]interface{})
			if !ok || i >= len(arr) {
				return "", fmt.Errorf("key %q: array index %d out of bounds", key, i)
			}
			current = arr[i]
			continue
		}

		// Map key
		m, ok := current.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("key %q: expected object, got %T", key, current)
		}
		val, exists := m[part]
		if !exists {
			return "", fmt.Errorf("key %q not found in response", key)
		}
		current = val
	}

	switch v := current.(type) {
	case string:
		return v, nil
	default:
		bytes, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("failed to serialize result: %w", err)
		}
		return string(bytes), nil
	}
}
