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

	// SharedPrefix and SegmentAssignment implement the shared-prefix prompt
	// layout (B3, TODO-pipeline-100x-velocita). When SharedPrefix is set the
	// provider renders it FIRST in the user message (before the task
	// template) and renders SegmentAssignment LAST, so every per-segment
	// call in one job shares one KV-cacheable prefix and only the assignment
	// suffix differs. When SharedPrefix is empty the legacy layout applies:
	// Prompt alone is prepended above the template and the other two fields
	// are ignored.
	SharedPrefix      string
	SegmentAssignment string
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
