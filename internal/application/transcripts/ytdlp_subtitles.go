// Package transcripts provides the canonical yt-dlp subtitle adapter used by
// the channel-monitor transcript port.
package transcripts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	monitor "github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// Deps is the constructor payload for YTDLPSubtitleAdapter.
type Deps struct {
	Ytdlp      *downloader.YTDLPDownloader
	CmdBuilder *ytdlp.CommandBuilder
	UseCookies bool
	Log        *zap.Logger
}

// YTDLPSubtitleAdapter implements monitor.TranscriptProvider. Subprocess and
// filesystem work live in ytdlp_subtitles_exec.go; document assembly and
// timeout ownership live in ytdlp_subtitles_fetch.go.
type YTDLPSubtitleAdapter struct {
	ytdlp              *downloader.YTDLPDownloader
	cmdBuilder         *ytdlp.CommandBuilder
	useCookies         bool
	log                *zap.Logger
	maxTranscriptLen   int
	minTranscriptWords int
	timeout            time.Duration
}

// NewYTDLPSubtitleAdapter constructs the adapter with the canonical defaults:
// 8000 characters, 10 words minimum and a 60-second subprocess timeout.
func NewYTDLPSubtitleAdapter(d Deps) *YTDLPSubtitleAdapter {
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	if d.CmdBuilder == nil {
		d.CmdBuilder = ytdlp.NewCommandBuilder(&ytcfg.Config{})
	}
	return &YTDLPSubtitleAdapter{
		ytdlp:              d.Ytdlp,
		cmdBuilder:         d.CmdBuilder,
		useCookies:         d.UseCookies,
		log:                d.Log,
		maxTranscriptLen:   8000,
		minTranscriptWords: 10,
		timeout:            60 * time.Second,
	}
}

// GetTranscript satisfies the legacy monitor transcript surface.
func (a *YTDLPSubtitleAdapter) GetTranscript(ctx context.Context, videoURL string) (string, error) {
	entries, err := a.fetchTimedTranscript(ctx, videoURL)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for _, entry := range entries {
		sb.WriteString(entry.Text)
		sb.WriteString(" ")
	}
	transcript := strings.TrimSpace(sb.String())

	if len(transcript) > a.maxTranscriptLen {
		transcript = transcript[:a.maxTranscriptLen]
	}
	if wordCount := len(strings.Fields(transcript)); wordCount < a.minTranscriptWords {
		return "", fmt.Errorf("transcript too short (%d words), skipping", wordCount)
	}

	a.log.Debug("YTDLPSubtitleAdapter GetTranscript succeeded",
		zap.Int("entries", len(entries)),
		zap.Int("transcript_len", len(transcript)),
		zap.Int("word_count", len(strings.Fields(transcript))))
	return transcript, nil
}

// GetTimedTranscript returns the parsed VTT cues for sibling analyzers.
func (a *YTDLPSubtitleAdapter) GetTimedTranscript(ctx context.Context, videoURL string) ([]TranscriptEntry, error) {
	return a.fetchTimedTranscript(ctx, videoURL)
}

var _ monitor.TranscriptProvider = (*YTDLPSubtitleAdapter)(nil)
