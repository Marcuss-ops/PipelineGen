package render

import (
	"context"
	"os"
	"strings"
	"testing"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"go.uber.org/zap"
)

// TestConfiguredCutterKeepsTheCanonicalEncoderGate pins the composition root,
// not just the adapter: WireStockPipeline obtains its cutter from this factory,
// so a future wrapper returning a different implementation would bypass the
// codec/preset/avcC gates even though the rustexec unit tests still pass.
func TestConfiguredCutterKeepsTheCanonicalEncoderGate(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	cutter, err := NewConfiguredCutterWithExecutionConfig(executable, "ffmpeg", mediaexec.ExecutionConfig{
		Policy: mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "veryfast", CRF: 23},
		Profile: mediaexec.VideoProfile{
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, KeyframeInterval: 48,
			AudioCodec: "aac", AudioBitrate: "128k", SampleRate: 48000, Channels: 2,
		},
	}, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("configure stock cutter: %v", err)
	}
	_, err = cutter.Cut(context.Background(), stockpipeline.CutRequest{
		SourcePath: "/source.mp4",
		Jobs:       []stockpipeline.CutJob{{StartSec: 0, EndSec: 1, OutputPath: "/out.mp4"}},
	})
	if err == nil || !strings.Contains(err.Error(), "STOCK_CANONICAL_ENCODER_REQUIRED") {
		t.Fatalf("factory cutter accepted an NVENC policy: %v", err)
	}
}
