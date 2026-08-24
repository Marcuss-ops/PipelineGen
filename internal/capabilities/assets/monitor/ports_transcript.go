// Package monitor — transcript provider port.
package assets

import (
	"context"

	transcript "github.com/Marcuss-ops/PipelineGen/internal/kernel/transcript"
)

// TranscriptProvider abstracts transcript retrieval for a YouTube URL.
type TranscriptProvider interface {
	Fetch(ctx context.Context, videoURL string) (transcript.Document, error)
}
