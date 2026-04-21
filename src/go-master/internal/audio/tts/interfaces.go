// Package tts provides text-to-speech capabilities.
package tts

import (
	"context"
)

// TTSGenerator interface for text-to-speech generation
type TTSGenerator interface {
	Generate(ctx context.Context, text string, lang string) (*GenerationResult, error)
	GenerateWithVoice(ctx context.Context, text string, voice string) (*GenerationResult, error)
}
