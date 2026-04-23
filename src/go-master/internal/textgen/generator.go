// Package textgen provides AI text generation with NVIDIA GPU acceleration
// This service orchestrates Ollama/OpenAI/Groq calls with GPU resource management
package textgen

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/gpu"
	"velox/go-master/pkg/logger"
)

// Provider represents an AI text generation backend
type Provider string

const (
	ProviderOllama Provider = "ollama"
	ProviderOpenAI Provider = "openai"
	ProviderGroq   Provider = "groq"
)

// GenerationRequest contains parameters for text generation
type GenerationRequest struct {
	Provider     Provider `json:"provider"`
	Model        string   `json:"model"`
	Prompt       string   `json:"prompt"`
	SystemPrompt string   `json:"system_prompt"`
	Temperature  float64  `json:"temperature"`
	MaxTokens    int      `json:"max_tokens"`
	TopP         float64  `json:"top_p"`
	Stream       bool     `json:"stream"`
	UseGPU       bool     `json:"use_gpu"` // Request GPU acceleration
}

// GenerationResult contains the generated text and metadata
type GenerationResult struct {
	Text       string        `json:"text"`
	Provider   Provider      `json:"provider"`
	Model      string        `json:"model"`
	GPUUsed    bool          `json:"gpu_used"`
	GPUDevice  int           `json:"gpu_device,omitempty"`
	TokensUsed int           `json:"tokens_used"`
	Duration   time.Duration `json:"duration"`
	CreatedAt  time.Time     `json:"created_at"`
}

// Generator orchestrates AI text generation with GPU support
type Generator struct {
	gpuManager *gpu.Manager
	config     *GeneratorConfig
	httpClient *http.Client
}

// GeneratorConfig holds configuration for the text generator
type GeneratorConfig struct {
	DefaultProvider    Provider      `json:"default_provider"`
	DefaultModel       string        `json:"default_model"`
	DefaultTemperature float64       `json:"default_temperature"`
	DefaultMaxTokens   int           `json:"default_max_tokens"`
	MaxRetries         int           `json:"max_retries"`
	Timeout            time.Duration `json:"timeout"`
	GPUSupported       bool          `json:"gpu_supported"`

	// Provider endpoints
	OllamaURL string `json:"ollama_url"`
	OpenAIKey string `json:"openai_key"`
	GroqKey   string `json:"groq_key"`
	GroqURL   string `json:"groq_url"`
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

	return result, nil
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
