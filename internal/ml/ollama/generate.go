// Package ollama provides script generation functionality.
package ollama

import (
	"context"
	"fmt"

	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Generator implementa ScriptGenerator
type Generator struct {
	client       *Client
	youtubeClient youtube.Client
}

// NewGenerator crea un nuovo generatore di script
func NewGenerator(client *Client) *Generator {
	return &Generator{client: client}
}

// SetYouTubeClient sets the YouTube client for transcript download
func (g *Generator) SetYouTubeClient(ytClient youtube.Client) {
	g.youtubeClient = ytClient
}

// GetClient restituisce il client Ollama sottostante
func (g *Generator) GetClient() *Client {
	return g.client
}

// Generate is a thin wrapper around client.Generate
func (g *Generator) Generate(ctx context.Context, prompt string) (string, error) {
	return g.client.Generate(ctx, prompt)
}

// GenerateFromText genera uno script da testo
func (g *Generator) GenerateFromText(ctx context.Context, req *TextGenerationRequest) (*GenerationResult, error) {
	// Applica defaults
	if req.Language == "" {
		req.Language = "italian"
	}
	if req.Duration == 0 {
		req.Duration = 60
	}
	if req.Tone == "" {
		req.Tone = "professional"
	}
	if req.Model == "" {
		req.Model = "gemma3:4b"
	}

	// Costruisci prompt
	prompt := buildTextPrompt(req)

	// Genera
	response, err := g.client.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to generate script: %w", err)
	}

	// Pulisci e calcola statistiche
	script := cleanScript(response)
	wordCount := countWords(script)
	estDuration := estimateDuration(wordCount)

	logger.Info("Script generated from text",
		zap.Int("words", wordCount),
		zap.Int("duration_secs", estDuration),
		zap.String("model", req.Model),
	)

	return &GenerationResult{
		Script:      script,
		WordCount:   wordCount,
		EstDuration: estDuration,
		Model:       req.Model,
		Prompt:      prompt,
	}, nil
}

// GenerateFromYouTube genera uno script da URL YouTube
func (g *Generator) GenerateFromYouTube(ctx context.Context, req *YouTubeGenerationRequest) (*GenerationResult, error) {
	// Applica defaults
	if req.Language == "" {
		req.Language = "italian"
	}
	if req.Duration == 0 {
		req.Duration = 60
	}
	if req.Model == "" {
		req.Model = "gemma3:4b"
	}

	if g.youtubeClient == nil {
		return nil, fmt.Errorf("YouTube client not configured - use GenerateFromYouTubeTranscript with pre-fetched transcript text")
	}

	// Download transcript using YouTube client
	transcript, err := g.youtubeClient.GetTranscript(ctx, req.YouTubeURL, req.Language)
	if err != nil {
		return nil, fmt.Errorf("failed to download YouTube transcript: %w", err)
	}

	if transcript == "" {
		return nil, fmt.Errorf("YouTube transcript is empty — the video may not have subtitles available")
	}

	logger.Info("YouTube transcript downloaded",
		zap.String("url", req.YouTubeURL),
		zap.Int("transcript_len", len(transcript)),
	)

	// Delegate to GenerateFromYouTubeTranscript
	return g.GenerateFromYouTubeTranscript(ctx, transcript, req)
}

// GenerateFromYouTubeTranscript genera uno script da trascrizione YouTube
func (g *Generator) GenerateFromYouTubeTranscript(ctx context.Context, transcript string, req *YouTubeGenerationRequest) (*GenerationResult, error) {
	// Applica defaults
	if req.Language == "" {
		req.Language = "italian"
	}
	if req.Duration == 0 {
		req.Duration = 60
	}
	if req.Model == "" {
		req.Model = "gemma3:4b"
	}

	// Costruisci prompt
	prompt := buildYouTubePrompt(transcript, req.Title, req.Language, "professional", req.Duration)

	// Genera
	response, err := g.client.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to generate script from YouTube: %w", err)
	}

	// Pulisci e calcola statistiche
	script := cleanScript(response)
	wordCount := countWords(script)
	estDuration := estimateDuration(wordCount)

	logger.Info("Script generated from YouTube transcript",
		zap.Int("words", wordCount),
		zap.Int("duration_secs", estDuration),
		zap.String("model", req.Model),
	)

	return &GenerationResult{
		Script:      script,
		WordCount:   wordCount,
		EstDuration: estDuration,
		Model:       req.Model,
		Prompt:      prompt,
	}, nil
}

// Regenerate rigenera uno script esistente
func (g *Generator) Regenerate(ctx context.Context, req *RegenerationRequest) (*GenerationResult, error) {
	// Applica defaults
	if req.Language == "" {
		req.Language = "italian"
	}
	if req.Tone == "" {
		req.Tone = "professional"
	}
	if req.Model == "" {
		req.Model = "gemma3:4b"
	}

	// Costruisci prompt
	prompt := buildRegeneratePrompt(req)

	// Genera
	response, err := g.client.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to regenerate script: %w", err)
	}

	// Pulisci e calcola statistiche
	script := cleanScript(response)
	wordCount := countWords(script)
	estDuration := estimateDuration(wordCount)

	logger.Info("Script regenerated",
		zap.Int("words", wordCount),
		zap.Int("duration_secs", estDuration),
		zap.String("model", req.Model),
	)

	return &GenerationResult{
		Script:      script,
		WordCount:   wordCount,
		EstDuration: estDuration,
		Model:       req.Model,
	}, nil
}

// ListModels restituisce i modelli disponibili
func (g *Generator) ListModels(ctx context.Context) ([]Model, error) {
	models, err := g.client.ListModels(ctx)
	if err != nil {
		return nil, err
	}

	// Converti nel tipo Model
	result := make([]Model, len(models))
	for i, m := range models {
		result[i] = Model{
			Name:       m.Name,
			ModifiedAt: m.ModifiedAt,
			Size:       m.Size,
		}
	}

	return result, nil
}