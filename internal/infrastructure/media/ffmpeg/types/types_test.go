package types

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestDefaultNormalizeOptionsLeavesEncoderSelectionToRuntimeResolver(t *testing.T) {
	opts := DefaultNormalizeOptions(&config.Config{})

	if opts.Policy.Codec != "" {
		t.Fatalf("DefaultNormalizeOptions must not materialize a codec: got policy=%+v", opts.Policy)
	}
	if opts.Codec != "" {
		t.Fatalf("legacy codec field must remain empty for runtime resolution: got %q", opts.Codec)
	}
	if opts.Policy.Preset != "veryfast" || opts.Policy.CRF != 23 {
		t.Fatalf("safe policy defaults were lost: got %+v", opts.Policy)
	}
	if opts.Profile.Width != 1920 || opts.Profile.Height != 1080 || opts.Profile.FPS != 24 {
		t.Fatalf("canonical profile defaults were lost: got %+v", opts.Profile)
	}
}

func TestDefaultNormalizeOptionsPreservesExplicitEncoderPolicy(t *testing.T) {
	opts := DefaultNormalizeOptions(&config.Config{Video: config.VideoConfig{
		Codec:  "h264_nvenc",
		Preset: "p1",
		CRF:    19,
	}})

	if opts.Policy.Codec != "h264_nvenc" || opts.Codec != "h264_nvenc" {
		t.Fatalf("explicit codec was not propagated: %+v / legacy=%q", opts.Policy, opts.Codec)
	}
	if opts.Policy.Preset != "p1" || opts.Policy.CRF != 19 {
		t.Fatalf("explicit encoder policy was not propagated: %+v", opts.Policy)
	}
}
