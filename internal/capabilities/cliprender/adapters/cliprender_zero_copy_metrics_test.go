package adapters

import (
	"context"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
)

func u64ptr(v uint64) *uint64 { return &v }

func TestClipRenderExecutorAdapter_ProjectsMeasuredZeroCopyCounters(t *testing.T) {
	fake := &fakeClipRenderExecutor{result: rustexec.ClipRenderResult{
		OutputPath:              "/out.mp4",
		SizeBytes:               1024,
		DurationSec:             1,
		Width:                   1280,
		Height:                  720,
		FPSNum:                  30,
		FPSDen:                  1,
		GPUReadbackBytes:        u64ptr(0),
		EncoderStagingCopyBytes: u64ptr(4096),
		NV12ToRGBAFrames:        u64ptr(3),
		RGBAToNV12Frames:        u64ptr(4),
	}}
	adapter := &ClipRenderExecutorAdapter{
		renderer: fake,
		resolver: cliprender.NewRenderBackendResolver(nil),
		probe:    emptyCapabilityProbe{},
	}

	outcome, err := adapter.Render(context.Background(), cliprender.ClipRenderPlanV1{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	m := outcome.Metrics
	if int64(m.GPUReadbackBytes) != 0 {
		t.Fatalf("gpu_readback_bytes = %d, want measured zero", int64(m.GPUReadbackBytes))
	}
	if int64(m.EncoderStagingCopyBytes) != 4096 {
		t.Fatalf("encoder_staging_copy_bytes = %d, want 4096", int64(m.EncoderStagingCopyBytes))
	}
	if int64(m.NV12ToRGBAFrames) != 3 {
		t.Fatalf("nv12_to_rgba_frames = %d, want 3", int64(m.NV12ToRGBAFrames))
	}
	if int64(m.RGBAToNV12Frames) != 4 {
		t.Fatalf("rgba_to_nv12_frames = %d, want 4", int64(m.RGBAToNV12Frames))
	}
}
