// Package nvidia fornisce integrazione con NVIDIA AI API per verifica pertinenza
package nvidia

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Client gestisce le chiamate all'API NVIDIA
type Client struct {
	openaiClient *openai.Client
	model        string
	temperature  float32
	maxTokens    int
	timeout      time.Duration
}

// Config configurazione NVIDIA
type Config struct {
	APIKey      string
	BaseURL     string
	Model       string
	Temperature float32
	MaxTokens   int
	Timeout     time.Duration
}

// DefaultConfig ritorna configurazione di default (legge da env)
func DefaultConfig() *Config {
	return &Config{
		APIKey:      os.Getenv("NVIDIA_API_KEY"),
		BaseURL:     getEnvOrDefault("NVIDIA_BASE_URL", "https://integrate.api.nvidia.com/v1"),
		Model:       getEnvOrDefault("NVIDIA_MODEL", "z-ai/glm5"),
		Temperature: 0.7,
		MaxTokens:   4096,
		Timeout:     30 * time.Second,
	}
}

// NewClient crea un nuovo client NVIDIA
func NewClient(cfg *Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("NVIDIA_API_KEY is required")
	}

	config := openai.DefaultConfig(cfg.APIKey)
	config.BaseURL = cfg.BaseURL

	httpClient := openai.DefaultConfig("").HTTPClient
	if httpClient != nil {
		config.HTTPClient = httpClient
	}

	return &Client{
		openaiClient: openai.NewClientWithConfig(config),
		model:        cfg.Model,
		temperature:  cfg.Temperature,
		maxTokens:    cfg.MaxTokens,
		timeout:      cfg.Timeout,
	}, nil
}

// VerifyYouTubeTitle verifica se un titolo YouTube è pertinente alla scena
// Ritorna un punteggio di pertinenza 0-100 e una spiegazione
func (c *Client) VerifyYouTubeTitle(ctx context.Context, sceneText, sceneKeywords string, videoTitle, videoDescription string) (*VerificationResult, error) {
	logger.Info("Verifying YouTube title with NVIDIA AI",
		zap.String("video_title", videoTitle),
		zap.String("scene_keywords", sceneKeywords),
	)

	prompt := c.buildVerificationPrompt(sceneText, sceneKeywords, videoTitle, videoDescription)

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.openaiClient.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: c.model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: c.systemPrompt(),
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
		Temperature: c.temperature,
		MaxTokens:   c.maxTokens,
	})

	if err != nil {
		return nil, fmt.Errorf("NVIDIA API call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from NVIDIA API")
	}

	content := resp.Choices[0].Message.Content
	result := c.parseResponse(content)

	logger.Info("YouTube title verification completed",
		zap.String("video_title", videoTitle),
		zap.Int("relevance_score", result.RelevanceScore),
		zap.String("recommendation", result.Recommendation),
	)

	return result, nil
}

// VerifyBatchTitles verifica più titoli YouTube in una sola chiamata
func (c *Client) VerifyBatchTitles(ctx context.Context, sceneText, sceneKeywords string, videos []VideoCandidate) ([]*VerificationResult, error) {
	logger.Info("Verifying batch of YouTube titles with NVIDIA AI",
		zap.Int("video_count", len(videos)),
	)

	if len(videos) == 0 {
		return nil, nil
	}

	// Costruisce prompt batch
	prompt := c.buildBatchPrompt(sceneText, sceneKeywords, videos)

	ctx, cancel := context.WithTimeout(ctx, c.timeout*2) // Timeout più lungo per batch
	defer cancel()

	resp, err := c.openaiClient.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: c.model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: c.systemPrompt(),
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
		Temperature: c.temperature,
		MaxTokens:   c.maxTokens * 2, // Più token per risposta batch
	})

	if err != nil {
		return nil, fmt.Errorf("NVIDIA API call failed for batch: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from NVIDIA API for batch")
	}

	content := resp.Choices[0].Message.Content
	results := c.parseBatchResponse(content, len(videos))

	logger.Info("Batch YouTube title verification completed",
		zap.Int("video_count", len(results)),
	)

	return results, nil
}

// CheckHealth verifica se l'API NVIDIA è disponibile
func (c *Client) CheckHealth(ctx context.Context) error {
	_, err := c.VerifyYouTubeTitle(ctx,
		"Test scene about technology",
		"technology, computer, AI",
		"Test video title",
		"Test video description",
	)
	return err
}

// ============================================================================
// PROMPTS
// ============================================================================

func (c *Client) systemPrompt() string {
	return `Sei un assistente AI specializzato nella valutazione della pertinenza di video YouTube per scene di script video.

Il tuo compito è analizzare titoli e descrizioni di video YouTube e determinare se sono pertinenti alla scena di uno script.

Rispondi SOLO nel seguente formato JSON:
{
  "relevance_score": 0-100,
  "recommendation": "download" | "review" | "reject",
  "reason": "Breve spiegazione in italiano del perché",
  "match_keywords": ["keyword1", "keyword2"],
  "warning": "Eventuale avvertimento (es. 'clickbait', 'off-topic', etc.)"
}

Criteri di valutazione:
- 85-100: Altamente pertinente, scarica subito (recommendation: "download")
- 50-84: Potrebbe essere pertinente, richiede revisione umana (recommendation: "review")
- 0-49: Non pertinente, scarta (recommendation: "reject")

Attenzione ai titoli clickbait che non corrispondono al contenuto reale del video.`
}

func (c *Client) buildVerificationPrompt(sceneText, sceneKeywords, videoTitle, videoDescription string) string {
	return fmt.Sprintf(`Analizza questo video YouTube per la scena dello script:

=== SCENA DELLO SCRIPT ===
Testo: %s
Keywords: %s

=== VIDEO YOUTUBE ===
Titolo: %s
Descrizione: %s

Valuta la pertinenza del video per questa scena e rispondi in formato JSON.`,
		sceneText, sceneKeywords, videoTitle, videoDescription)
}

func (c *Client) buildBatchPrompt(sceneText, sceneKeywords string, videos []VideoCandidate) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`Analizza questi %d video YouTube per la scena dello script:

=== SCENA DELLO SCRIPT ===
Testo: %s
Keywords: %s

=== VIDEO YOUTUBE (lista) ===
`, len(videos), sceneText, sceneKeywords))

	for i, v := range videos {
		sb.WriteString(fmt.Sprintf("\n[%d] Titolo: %s\n    Descrizione: %s\n", i+1, v.Title, v.Description))
	}

	sb.WriteString("\nRispondi con un array JSON di oggetti, uno per ogni video, nello stesso ordine.")
	return sb.String()
}

// ============================================================================
// PARSING RISPOSTE
// ============================================================================

// VerificationResult risultato della verifica
type VerificationResult struct {
	RelevanceScore int      `json:"relevance_score"`
	Recommendation string   `json:"recommendation"` // "download", "review", "reject"
	Reason         string   `json:"reason"`
	MatchKeywords  []string `json:"match_keywords"`
	Warning        string   `json:"warning,omitempty"`
}

// VideoCandidate candidato per verifica batch
type VideoCandidate struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

func (c *Client) parseResponse(content string) *VerificationResult {
	result := &VerificationResult{
		RelevanceScore: 50,
		Recommendation: "review",
		Reason:         "Default - parsing fallito",
	}

	// Parsing semplificato - cerca score nel testo
	// In produzione usare JSON parser robusto
	contentLower := strings.ToLower(content)

	// Cerca punteggio
	if strings.Contains(contentLower, `"relevance_score"`) {
		// Estrai numero dopo relevance_score
		// Implementazione semplificata
		for i := 0; i <= len(content)-16; i++ {
			if content[i:i+16] == `"relevance_score"` {
				// Trova il numero dopo i due punti
				for j := i + 16; j < len(content); j++ {
					if content[j] >= '0' && content[j] <= '9' {
						start := j
						for j < len(content) && content[j] >= '0' && content[j] <= '9' {
							j++
						}
						scoreStr := content[start:j]
						fmt.Sscanf(scoreStr, "%d", &result.RelevanceScore)
						break
					}
				}
				break
			}
		}
	}

	// Cerca recommendation
	if strings.Contains(contentLower, `"download"`) {
		result.Recommendation = "download"
	} else if strings.Contains(contentLower, `"reject"`) {
		result.Recommendation = "reject"
	} else if strings.Contains(contentLower, `"review"`) {
		result.Recommendation = "review"
	}

	// Determina score basato su recommendation se parsing fallito
	if result.RelevanceScore == 50 {
		switch result.Recommendation {
		case "download":
			result.RelevanceScore = 90
		case "review":
			result.RelevanceScore = 65
		case "reject":
			result.RelevanceScore = 20
		}
	}

	return result
}

func (c *Client) parseBatchResponse(content string, expectedCount int) []*VerificationResult {
	results := make([]*VerificationResult, expectedCount)

	// Parsing semplificato - crea risultati default
	for i := 0; i < expectedCount; i++ {
		results[i] = &VerificationResult{
			RelevanceScore: 50,
			Recommendation: "review",
			Reason:         "Batch parsing - default",
		}
	}

	return results
}

// ============================================================================
// HELPERS
// ============================================================================

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
