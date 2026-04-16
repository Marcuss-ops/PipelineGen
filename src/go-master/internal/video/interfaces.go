// Package video provides video processing capabilities.
package video

import (
	"context"
)

// VideoProcessor interface for dependency injection
type VideoProcessor interface {
	GenerateVideo(ctx context.Context, req GenerationRequest) (*GenerationResult, error)
	CheckBinary() error
}
