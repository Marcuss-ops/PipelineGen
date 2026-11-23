package adapters

// cliprender_transcript_acquire_identity_test.go pins the wiring the 5-priority
// transcript chain depends on: a materialized clip only knows its temporary
// local path, so the resolver must forward the source video id, the clip window
// and the Drive identity. Without VideoID the YouTube-subtitle priorities could
// never run (the live failure logged "video_id":"").

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

type recordingSubtitleFetcher struct {
	calls    int
	videoID  string
	startSec int
	endSec   int
}

func (r *recordingSubtitleFetcher) FetchSegmentSubtitles(_ context.Context, videoID string, startSec, endSec int) (*detail.ResolvedTextBundle, error) {
	r.calls++
	r.videoID, r.startSec, r.endSec = videoID, startSec, endSec
	return &detail.ResolvedTextBundle{
		LanguageCode: "en",
		PlainText:    "hello world",
		// Source-video timeline: the acquire service rebases the cues onto the
		// clip window (10s..25s), so this segment lands at 0..1000ms.
		Cues:       []detail.TimedCue{{StartMs: 10000, EndMs: 11000, Text: "hello world"}},
		SourceType: detail.TextSourceYouTubeSubtitle,
	}, nil
}

func TestTranscriptResolverGeneratePassesSourceIdentityToAcquireChain(t *testing.T) {
	subtitles := &recordingSubtitleFetcher{}
	acquire, err := texttracks.NewAcquireService(subtitles, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewAcquireService: %v", err)
	}
	resolver := NewClipRenderTranscriptResolver(zap.NewNop())
	resolver.SetAcquire(acquire)

	result, err := resolver.Generate(context.Background(), cliprender.TranscriptInput{
		AssetID:  "asset-1",
		Language: "en",
	}, &cliprender.MaterializedAsset{
		AssetID:     "asset-1",
		LocalPath:   filepath.Join(t.TempDir(), "scratch-clip.mp4"),
		VideoID:     "yt-video-1",
		DriveFileID: "drive-1",
		StartSec:    10,
		EndSec:      25,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if subtitles.calls != 1 {
		t.Fatalf("youtube subtitle priority calls = %d, want 1 (VideoID must reach the chain)", subtitles.calls)
	}
	if subtitles.videoID != "yt-video-1" || subtitles.startSec != 10 || subtitles.endSec != 25 {
		t.Fatalf("acquire command = video %q %d..%d, want yt-video-1 10..25",
			subtitles.videoID, subtitles.startSec, subtitles.endSec)
	}
	if result.Text != "hello world" || len(result.Cues) != 1 {
		t.Fatalf("result = %+v, want the rebased subtitle bundle", result)
	}
	if result.Cues[0].StartMs != 0 || result.Cues[0].EndMs != 1000 {
		t.Fatalf("cue window = %d..%d, want the clip-local 0..1000", result.Cues[0].StartMs, result.Cues[0].EndMs)
	}
}

func TestNewStreamingTranscriberUsesConfiguredWhisperPython(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.WhisperPythonBin = "/nonexistent/venv-whisper/bin/python"
	_, err := NewClipRenderStreamingTranscriber(cfg, zap.NewNop())
	if err == nil {
		t.Fatal("a configured interpreter that is not executable must fail closed at construction")
	}
	// The error must name the CONFIGURED path, proving the adapter no longer
	// hardcodes PATH's python3.
	if !strings.Contains(err.Error(), "/nonexistent/venv-whisper/bin/python") {
		t.Fatalf("error does not name the configured interpreter: %v", err)
	}
}
