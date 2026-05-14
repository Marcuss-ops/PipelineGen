package scriptdocs

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/scripts"
)

// Service handles script document generation
type Service struct {
	generator   *ollama.Generator
	scriptsRepo *scripts.ScriptRepository
	log         *zap.Logger
}

// NewService creates a new script docs service
func NewService(gen *ollama.Generator, repo *scripts.ScriptRepository, log *zap.Logger) *Service {
	return &Service{
		generator:   gen,
		scriptsRepo: repo,
		log:         log,
	}
}

// GenerateScript generates a script and saves it to the database
func (s *Service) GenerateScript(ctx context.Context, topic, style, language string) (int64, error) {
	if s.generator == nil {
		return 0, fmt.Errorf("ollama generator is not initialized")
	}

	if topic == "" {
		return 0, fmt.Errorf("topic is required")
	}

	if language == "" {
		language = "english"
	}

	// Build request
	req := types.TextGenerationRequest{
		Language: language,
		Duration: 60,
		Tone:     style,
		Title:    topic,
		Prompt:   topic,
	}

	// Generate script using Ollama
	result, err := s.generator.GenerateScript(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("failed to generate script: %w", err)
	}

	// Save to database
	scriptRec := &scripts.ScriptRecord{
		Topic:         topic,
		Duration:      60,
		Language:      language,
		Template:      style,
		Mode:          "generated",
		NarrativeText: result.Script,
		ModelUsed:     result.Model,
		Version:       1,
		IsDeleted:     false,
	}

	scriptID, err := s.scriptsRepo.SaveScript(scriptRec, nil, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to save script: %w", err)
	}

	s.log.Info("script generated and saved",
		zap.Int64("script_id", scriptID),
		zap.String("topic", topic),
	)

	return scriptID, nil
}
