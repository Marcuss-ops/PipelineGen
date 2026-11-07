package rustexec

import (
	"context"
	"strings"
	"testing"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
)

// TestCutRefusesANonCanonicalEncoderBeforeEncoding pins the stock cutter's
// encoder gate: only libx264 produces the certified assembly SPS/PPS, so an
// NVENC policy must fail before a single frame is encoded.
func TestCutRefusesANonCanonicalEncoderBeforeEncoding(t *testing.T) {
	p := &VideoProcessor{
		policy: mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "veryfast", CRF: 23},
		profile: mediaexec.VideoProfile{
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, KeyframeInterval: 48,
			AudioCodec: "aac", AudioBitrate: "128k", SampleRate: 48000, Channels: 2,
		},
	}
	_, err := p.Cut(context.Background(), stockpipeline.CutRequest{
		SourcePath: "/source.mp4",
		Jobs:       []stockpipeline.CutJob{{StartSec: 0, EndSec: 5, OutputPath: "/out.mp4"}},
	})
	if err == nil || !strings.Contains(err.Error(), "STOCK_CANONICAL_ENCODER_REQUIRED") {
		t.Fatalf("Cut error = %v, want STOCK_CANONICAL_ENCODER_REQUIRED", err)
	}
}
