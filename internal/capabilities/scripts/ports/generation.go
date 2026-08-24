package ports

import (
	"context"
	"encoding/json"
)

// OutputMode declares the response shape requested from the script generator.
type OutputMode string

const (
	OutputModePlainText OutputMode = "plain_text"
	OutputModeScriptV1  OutputMode = "script_v1"
)

// TextGenerationRequest is the provider-neutral script generation request.
type TextGenerationRequest struct {
	Language         string
	Duration         int
	DurationMinutes  int
	MinWords         int
	WordsPerMinute   int
	MaxChars         int
	Tone             string
	Model            string
	Prompt           string
	SourceText       string
	Title            string
	ClipIDs          []string
	Options          map[string]any
	WebContext       string
	DisableWebSearch bool
	GroundingPolicy  string
	OutputMode       OutputMode
	Format           json.RawMessage
	Temperature      float64
	TopP             float64
	Seed             int
	NoSeed           bool
}

// ScriptGenerator is the application port for model-backed script generation.
type ScriptGenerator interface {
	GenerateScript(ctx context.Context, req TextGenerationRequest) (*GenerationResult, error)
}

// GenerationResult is the provider-neutral result returned by ScriptGenerator.
type GenerationResult struct {
	Script           string
	WordCount        int
	EstDuration      int
	Model            string
	Prompt           string
	GenerationSource string
}
