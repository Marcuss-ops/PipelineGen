package processor

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ytdlp"
)

type captureYTDLP struct {
	req *downloader.DownloadRequest
}

func (c *captureYTDLP) Download(_ context.Context, req *downloader.DownloadRequest) error {
	c.req = req
	return nil
}

// TestDownloadStep_SectionLeavesTimeoutToDownloader pins that the processor
// does not override the downloader's configured section bound with a
// hardcoded 10 minutes: a zero Timeout lets effectiveDownloadTimeout apply
// VELOX_YOUTUBE_SECTION_DOWNLOAD_TIMEOUT_SECONDS (default 600s).
func TestDownloadStep_SectionLeavesTimeoutToDownloader(t *testing.T) {
	dl := &captureYTDLP{}
	p := &Processor{dl: dl, log: zap.NewNop()}
	input := &detail.ProcessInput{
		ID:               "clip",
		SourceURL:        "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		DownloadSections: []string{"*00:00:00-00:00:10"},
	}

	if _, err := p.downloadStep(context.Background(), input, t.TempDir()+"/raw"); err != nil {
		t.Fatalf("downloadStep: %v", err)
	}
	if dl.req == nil {
		t.Fatal("downloader was not called")
	}
	if dl.req.Timeout != 0 {
		t.Fatalf("section Timeout = %v, want 0 so the configured bound applies", dl.req.Timeout)
	}
	if dl.req.Format != ytdlp.DefaultYouTubeSectionFormatSelectors {
		t.Fatalf("section Format = %q, want the canonical section selector", dl.req.Format)
	}
	if dl.req.MergeFormat != "mp4" {
		t.Fatalf("section MergeFormat = %q, want mp4", dl.req.MergeFormat)
	}
	if !dl.req.NoPlaylist {
		t.Fatal("section download must disable playlists")
	}
}
