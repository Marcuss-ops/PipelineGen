// Package tts provides text-to-speech capabilities.
package tts

import (
	"context"

	"velox/go-master/internal/service/pipeline"
)

// TTSAdapter wraps EdgeTTS to satisfy pipeline.TTSGenerator interface
type TTSAdapter struct {
	edgeTTS *EdgeTTS
}

// NewTTSAdapter creates a new TTS adapter for pipeline
func NewTTSAdapter(edgeTTS *EdgeTTS) *TTSAdapter {
	return &TTSAdapter{edgeTTS: edgeTTS}
}

// Generate generates voiceover from text and returns pipeline-compatible result
func (a *TTSAdapter) Generate(ctx context.Context, text string, language string) (*pipeline.TTSResult, error) {
	result, err := a.edgeTTS.Generate(ctx, text, language)
	if err != nil {
		return nil, err
	}

	return &pipeline.TTSResult{
		FilePath:  result.FilePath,
		Duration:  float64(result.Duration),
		Language:  result.Language,
		VoiceUsed: result.VoiceUsed,
	}, nil
}

// Ensure TTSAdapter satisfies the TTSGenerator interface
var _ pipeline.TTSGenerator = (*TTSAdapter)(nil)
