package rustexec

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
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

// cutTestBox renders one ISO-BMFF box. The avcC gate reads only the box
// structure, so a synthetic file proves the post-encode identity check without
// needing a decodable stream.
func cutTestBox(typ string, payload []byte) []byte {
	box := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(box[:4], uint32(len(box)))
	copy(box[4:8], typ)
	copy(box[8:], payload)
	return box
}

func cutTestMP4(avcC []byte) []byte {
	sampleEntry := make([]byte, 78)
	sampleEntry = append(sampleEntry, cutTestBox("avcC", avcC)...)
	stsdPayload := make([]byte, 8)
	binary.BigEndian.PutUint32(stsdPayload[4:], 1)
	stsdPayload = append(stsdPayload, cutTestBox("avc1", sampleEntry)...)
	hdlrPayload := append(make([]byte, 8), []byte("vide")...)
	hdlrPayload = append(hdlrPayload, make([]byte, 12)...)
	mdia := cutTestBox("mdia", append(cutTestBox("hdlr", hdlrPayload),
		cutTestBox("minf", cutTestBox("stbl", cutTestBox("stsd", stsdPayload)))...))
	trak := cutTestBox("trak", mdia)
	return append(cutTestBox("ftyp", []byte("isom\x00\x00\x02\x00isom")), cutTestBox("moov", trak)...)
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
		Items:     []cutItem{{JobID: out, OutputPath: out, Status: "succeeded", SizeBytes: int64(len(r.payload))}},
	})
	if err != nil {
		return nil, nil, err
	}
	return responsePayload, nil, nil
}

// TestCutRejectsANonCanonicalBitstream is the post-encode half of the stock
// publication gate: the Rust process may report `succeeded`, but if the bytes
// carry a different SPS/PPS the item must become failed and no SHA may be
// returned to the publisher. The upload path keys on Status+SHA, so a gate that
// only logged the mismatch would still publish the clip.
func TestCutRejectsANonCanonicalBitstream(t *testing.T) {
	output := filepath.Join(t.TempDir(), "stock.mp4")
	runner := &cutResponseRunner{payload: cutTestMP4([]byte{
		0x01, 0x64, 0x00, 0x28, 0xff, 0xe1, 0x00, 0x04, 0x67, 0x64, 0x00, 0x28,
	})}
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
	if !errors.Is(item.Err, kernelmedia.ErrVideoExtradataNotCanonical) {
		t.Fatalf("item error = %v, want ErrVideoExtradataNotCanonical", item.Err)
	}
	if item.SHA256Hex != "" {
		t.Fatalf("failed item published SHA %q; a rejected clip must not be certifiable", item.SHA256Hex)
	}
}
