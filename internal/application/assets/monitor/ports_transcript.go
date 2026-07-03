// Package monitor — transcript provider port.
package monitor

import (
	"context"

	transcript "github.com/Marcuss-ops/PipelineGen/internal/domain/transcript"
)

// TranscriptProvider abstracts transcript retrieval for a YouTube URL.
type TranscriptProvider interface {
	Fetch(ctx context.Context, videoURL string) (transcript.Document, error)
}
