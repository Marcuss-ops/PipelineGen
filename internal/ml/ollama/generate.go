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

// Generate is a thin wrapper around client.Chat (preferred) or client.Generate
func (g *Generator) Generate(ctx context.Context, prompt string) (string, error) {
	// For raw prompt strings, we still use Generate for compatibility
	return g.client.Generate(ctx, prompt)
}

// GenerateFromText genera uno script da testo usando Chat API
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
		req.Model = "gemma3:12b"
	}

	// Costruisci messaggi chat (Molto più efficace per seguire istruzioni di lunghezza)
	messages := buildChatMessages(req)

	// Configura opzioni creative
	options := req.Options
	if options == nil {
		options = make(map[string]interface{})
	}
	if _, ok := options["temperature"]; !ok {
		options["temperature"] = 0.8
	}
	if _, ok := options["num_predict"]; !ok {
		options["num_predict"] = 4096 // Permetti risposte molto lunghe
	}

	// Usa Chat API
	response, err := g.client.Chat(ctx, messages, options)
	if err != nil {
		return nil, fmt.Errorf("failed to generate script via chat: %w", err)
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
		Prompt:      fmt.Sprintf("%v", messages),
	}, nil
}

// GenerateStreamFromText genera uno script da testo in modalità streaming
func (g *Generator) GenerateStreamFromText(ctx context.Context, req *TextGenerationRequest) (<-chan string, <-chan error) {
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
		req.Model = "gemma3:12b"
	}

	// Costruisci prompt
	prompt := buildTextPrompt(req)

	// Opzioni
	options := req.Options
	if options == nil {
		options = make(map[string]interface{})
	}
	if _, ok := options["temperature"]; !ok {
		options["temperature"] = 0.8
	}
	if _, ok := options["num_predict"]; !ok {
		options["num_predict"] = 4096
	}

	// Inizia lo streaming dal client (GenerateStream usa internamente GenerateWithOptions)
	return g.client.GenerateStreamWithOptions(ctx, req.Model, prompt, options)
}

// GenerateFromYouTube genera uno script da URL YouTube
func (g *Generator) GenerateFromYouTube(ctx context.Context, req *YouTubeGenerationRequest) (*GenerationResult, error) {
	if g.youtubeClient == nil {
		return nil, fmt.Errorf("YouTube client not configured")
	}

	// Applica defaults
	if req.Language == "" {
		req.Language = "italian"
	}
	if req.Duration == 0 {
		req.Duration = 60
	}
	if req.Model == "" {
		req.Model = "gemma3:12b"
	}

	// Download transcript
	transcript, err := g.youtubeClient.GetTranscript(ctx, req.YouTubeURL, req.Language)
	if err != nil {
		return nil, fmt.Errorf("failed to download YouTube transcript: %w", err)
	}

	logger.Info("YouTube transcript downloaded",
		zap.String("url", req.YouTubeURL),
		zap.Int("transcript_len", len(transcript)),
	)

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
		req.Model = "gemma3:12b"
	}

	// Costruisci messaggi chat
	targetWords := (req.Duration * 150) / 60
	messages := []Message{
		{Role: "system", Content: "Sei un documentarista esperto. Riscrivi trascrizioni in script avvincenti."},
		{Role: "user", Content: fmt.Sprintf("Scrivi uno script di almeno %d parole su %s basandoti su questa trascrizione: %s", targetWords, req.Title, transcript)},
	}

	// Opzioni
	options := req.Options
	if options == nil {
		options = make(map[string]interface{})
	}
	if _, ok := options["temperature"]; !ok {
		options["temperature"] = 0.7
	}
	if _, ok := options["num_predict"]; !ok {
		options["num_predict"] = 4096
	}

	// Genera
	response, err := g.client.Chat(ctx, messages, options)
	if err != nil {
		return nil, fmt.Errorf("failed to generate script from transcript: %w", err)
	}

	// Pulisci e calcola statistiche
	script := cleanScript(response)
	wordCount := countWords(script)
	estDuration := estimateDuration(wordCount)

	return &GenerationResult{
		Script:      script,
		WordCount:   wordCount,
		EstDuration: estDuration,
		Model:       req.Model,
		Prompt:      fmt.Sprintf("%v", messages),
	}, nil
}

// Regenerate rigenera uno script esistente
func (g *Generator) Regenerate(ctx context.Context, req *RegenerationRequest) (*GenerationResult, error) {
	// Applica defaults
	if req.Language == "" {
		req.Language = "italian"
	}
	if req.Model == "" {
		req.Model = "gemma3:12b"
	}

	messages := []Message{
		{Role: "system", Content: "Sei un copywriter senior. Migliora lo script fornito rendendolo più avvincente e lungo."},
		{Role: "user", Content: fmt.Sprintf("Migliora e amplia questo script (Titolo: %s):\n\n%s", req.Title, req.OriginalScript)},
	}

	// Opzioni
	options := req.Options
	if options == nil {
		options = make(map[string]interface{})
	}
	options["temperature"] = 0.8
	options["num_predict"] = 4096

	// Genera
	response, err := g.client.Chat(ctx, messages, options)
	if err != nil {
		return nil, fmt.Errorf("failed to regenerate script: %w", err)
	}

	// Pulisci e calcola statistiche
	script := cleanScript(response)
	wordCount := countWords(script)
	estDuration := estimateDuration(wordCount)

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
