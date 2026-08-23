package youtube

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	transcript "github.com/Marcuss-ops/PipelineGen/internal/kernel/transcript"
)

// Fetch downloads the timed transcript once and assembles the canonical
// transcript.Document consumed by application transcript ports.
func (a *YTDLPSubtitleAdapter) Fetch(parent context.Context, videoURL string) (transcript.Document, error) {
	ctx, cancel := inheritOrWithTimeout(parent, a.timeout)
	defer cancel()

	entries, err := a.fetchTimedTranscript(ctx, videoURL)
	if err != nil {
		return transcript.Document{}, err
	}
	if len(entries) == 0 {
		return transcript.Document{}, fmt.Errorf("transcript empty for video (0 timed entries): %s", videoURL)
	}

	videoID := extractIDFromURL(videoURL)
	var sb strings.Builder
	maxLen := a.maxTranscriptLen
	if maxLen <= 0 {
		maxLen = 8000
	}
	for _, entry := range entries {
		sb.WriteString(entry.Text)
		sb.WriteString(" ")
		if sb.Len() > maxLen*2 {
			break
		}
	}
	text := strings.TrimSpace(sb.String())
	if len(text) > maxLen {
		text = text[:maxLen]
	}
	if wordCount := len(strings.Fields(text)); wordCount < a.minTranscriptWords {
		return transcript.Document{}, fmt.Errorf("transcript too short (%d words), skipping", wordCount)
	}

	lastEnd := entries[len(entries)-1].End
	doc := transcript.Document{
		VideoID:     videoID,
		Language:    "en",
		Source:      "asr",
		Text:        text,
		DurationSec: lastEnd,
		Entries:     entries,
		FetchedAt:   time.Now().UTC(),
	}
	a.log.Debug("YTDLPSubtitleAdapter Fetch succeeded",
		zap.String("video_id", videoID),
		zap.Int("entries", len(entries)),
		zap.Int("text_len", len(text)),
		zap.Float64("duration_sec", lastEnd),
		zap.Int("word_count", len(strings.Fields(text))))
	return doc, nil
}

// inheritOrWithTimeout preserves a shorter parent deadline and otherwise owns
// a fresh timeout for the yt-dlp subprocess.
func inheritOrWithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, hasDeadline := parent.Deadline(); hasDeadline {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}
