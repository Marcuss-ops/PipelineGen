package render

import (
	"context"
	"testing"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/require"
)

type rendererPolicyCaptureRunner struct {
	args []string
}

func (r *rendererPolicyCaptureRunner) Run(_ context.Context, _ string, args []string, _ process.Options) (*process.Result, error) {
	r.args = append([]string(nil), args...)
	return &process.Result{}, nil
}

func rendererHasPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestFFmpegRenderer_RequestCodecInheritsConfiguredPolicy(t *testing.T) {
	renderer := NewFFmpegRendererWithPolicy("ffmpeg", config.VideoEncoderPolicy{
		Codec:  "h264_nvenc",
		Preset: "p4",
		CRF:    17,
	}, nil, nil)
	capture := &rendererPolicyCaptureRunner{}
	renderer.encoder.WithRunner(capture)

	_, err := renderer.Render(context.Background(), stockpipeline.RenderRequest{
		InputPaths: []string{"input.mp4"},
		OutputPath: "output.mp4",
		Codec:      "libx264",
	})
	require.NoError(t, err)
	require.True(t, rendererHasPair(capture.args, "-c:v", "libx264"), "request codec must override configured codec: argv=%v", capture.args)
	require.True(t, rendererHasPair(capture.args, "-preset", "p4"), "request must inherit configured preset: argv=%v", capture.args)
	require.True(t, rendererHasPair(capture.args, "-crf", "17"), "argv=%v", capture.args)
}

var _ ffmpeg.ProcessRunner = (*rendererPolicyCaptureRunner)(nil)
