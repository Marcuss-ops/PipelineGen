// Package voiceover provides the voiceover generation use case.
//
// The implementation lives in internal/media/voiceover/.
package voiceover

import "context"

// Request is a voiceover generation request.
type Request struct {
	ScriptID  int64
	Text      string
	Voice     string
	Language  string
	OutputPath string
}

// Result is the outcome of voiceover generation.
type Result struct {
	AudioPath string
	Duration  float64
	Error     string
}

// Generator is the contract for voiceover generation.
type Generator interface {
	Generate(ctx context.Context, req Request) (*Result, error)
}
