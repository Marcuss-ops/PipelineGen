package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// TestCutRefusesANonCanonicalPresetBeforeEncoding pins the second half of the
// stock encoder lane: libx264 alone is not the certified profile when the
// resolved preset is not the one the avcC identity was measured with.
func TestCutRefusesANonCanonicalPresetBeforeEncoding(t *testing.T) {
	p := &VideoProcessor{
		policy: mediaexec.EncoderPolicy{Codec: "libx264", Preset: "medium", CRF: 23},
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

// cutResponseRunner writes the output the Rust process would have produced and
// reports the per-item success the real protocol returns.
type cutResponseRunner struct {
	payload []byte
}

func (r *cutResponseRunner) Run(_ context.Context, _ string, input []byte) ([]byte, []byte, error) {
	var req request
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, nil, fmt.Errorf("decode cut request: %w", err)
	}
	if len(req.Jobs) != 1 {
		return nil, nil, fmt.Errorf("cut request has %d jobs, want 1", len(req.Jobs))
	}
	out := req.Jobs[0].OutputPath
	if err := os.WriteFile(out, r.payload, 0o600); err != nil {
		return nil, nil, fmt.Errorf("write cut output: %w", err)
	}
	responsePayload, err := json.Marshal(response{
		OK:        true,
		Operation: string(OperationCutBatch),
		Items: []cutItem{{
			JobID: out, OutputPath: out, Status: "succeeded", SizeBytes: int64(len(r.payload)),
			SHA256:               strings.Repeat("a", 64),
			VideoExtradataSHA256: "drifted-avcc-digest",
		}},
	})
	if err != nil {
		return nil, nil, err
	}
	return responsePayload, nil, nil
}

// TestCutRejectsANonCanonicalBitstream checks the contract boundary: a cut
// item with a non-canonical avcC digest from Rust is rejected before Go returns
// the artifact SHA to the publisher.
func TestCutRejectsANonCanonicalBitstream(t *testing.T) {
	output := filepath.Join(t.TempDir(), "stock.mp4")
	runner := &cutResponseRunner{payload: []byte("rust-validated-output")}
	client := NewClientWithExecutor(nil, nil)
	client.runner = runner
	processor := &VideoProcessor{
		client: client,
		policy: mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23},
		profile: mediaexec.VideoProfile{
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, KeyframeInterval: 48,
			AudioCodec: "aac", AudioBitrate: "128k", SampleRate: 48000, Channels: 2,
		},
	}
	result, err := processor.Cut(context.Background(), stockpipeline.CutRequest{
		SourcePath: "/source.mp4",
		Jobs:       []stockpipeline.CutJob{{StartSec: 0, EndSec: 5, OutputPath: output}},
	})
	if err != nil {
		t.Fatalf("Cut returned a transport error instead of a per-item verdict: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("Cut returned %d items, want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.Status != stockpipeline.CutItemStatusFailed {
		t.Fatalf("item status = %s, want failed for a non-canonical avcC", item.Status)
	}
	if item.Err == nil || !strings.Contains(item.Err.Error(), "avcC SHA-256") {
		t.Fatalf("item error = %v, want rejected avcC digest", item.Err)
	}
	if item.SHA256Hex != "" {
		t.Fatalf("failed item published SHA %q; a rejected clip must not be certifiable", item.SHA256Hex)
	}
}
