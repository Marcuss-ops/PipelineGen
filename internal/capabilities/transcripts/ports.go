package transcripts

import (
	"context"

	transcript "github.com/Marcuss-ops/PipelineGen/internal/kernel/transcript"
)

// SubtitleSource retrieves one canonical transcript document for a video URL.
// Implementations belong to infrastructure/platform packages; application
// services depend only on this port and the domain document.
type SubtitleSource interface {
	Fetch(ctx context.Context, videoURL string) (transcript.Document, error)
}

// TranscriptFetcher is a descriptive alias for callers that use the
// transcript-fetching vocabulary. SubtitleSource remains the canonical name.
type TranscriptFetcher = SubtitleSource
