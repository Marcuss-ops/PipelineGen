// Package images provides the image generation use case.
//
// The implementation lives in internal/media/images/.
package images

import "context"

// Request is an image generation request.
type Request struct {
	Prompt    string
	Style     string
	Width     int
	Height    int
	OutputPath string
}

// Result is the outcome of image generation.
type Result struct {
	ImagePath string
	Error     string
}

// Generator is the contract for AI image generation.
type Generator interface {
	Generate(ctx context.Context, req Request) (*Result, error)
}
