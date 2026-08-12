package rustexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
)

func TestStockRendererRenderCanonicalPlanValidatesAndTransportsPlan(t *testing.T) {
	path := t.TempDir() + "/clip.mp4"
	contents := []byte("canonical clip")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 5600000,
		Segments: []audio.TimelineSegment{{
			ID: "scene-a", Index: 0, TimelineStartUS: 0, DurationUS: 5600000,
			Video: audio.VideoSegment{AssetID: "clip-a", SourceInUS: 33200000, SourceDurationUS: 5600000},
			Audio: audio.AudioIntent{Mode: audio.AudioSilence},
		}},
	}
	plan, err := render.Compile(render.CompileInput{
		JobID: "job-1", Revision: "generation.v1", OutputPath: t.TempDir() + "/final.mp4", FrameRate: audio.FrameRate{Numerator: 30000, Denominator: 1001},
		Timeline: timeline,
		Manifest: []render.AssetManifestEntry{{AssetID: "clip-a", Path: path, SHA256: hex.EncodeToString(sum[:]), FrameCount: 2000}},
	})
	if err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{stdout: []byte(`{"ok":true,"operation":"render_stock"}`)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	stock := &StockRenderer{client: client, policy: mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 23}, profile: mediaexec.VideoProfile{}.WithDefaults()}
	validated, err := render.ValidateRenderPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := stock.RenderCanonicalPlan(context.Background(), validated); err != nil {
		t.Fatalf("RenderCanonicalPlan() error = %v", err)
	}
	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Operation != OperationRenderStock || len(sent.RenderPlan) == 0 || sent.RenderPlan[0] != '{' {
		t.Fatalf("canonical plan was not transported: %+v", sent)
	}
	var wire struct {
		PlanSHA256     string `json:"plan_sha256"`
		Frames         int64  `json:"duration_frames"`
		FPSNumerator   int64  `json:"fps_numerator"`
		FPSDenominator int64  `json:"fps_denominator"`
	}
	if err := json.Unmarshal(sent.RenderPlan, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.PlanSHA256 != plan.PlanSHA256 || wire.Frames != 168 || wire.FPSNumerator != 30000 || wire.FPSDenominator != 1001 {
		t.Fatalf("unexpected canonical wire contract: %+v", wire)
	}

	plan.PlanSHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err := render.ValidateRenderPlan(plan); err == nil {
		t.Fatal("tampered plan hash must be rejected before executor call")
	}
}

type recordingRunner struct {
	inputs [][]byte
}

func (r *recordingRunner) Run(_ context.Context, _ string, input []byte) ([]byte, []byte, error) {
	r.inputs = append(r.inputs, append([]byte(nil), input...))
	return []byte(`{"ok":true}`), nil, nil
}

func TestStockRendererRenderCanonicalPlanCopiesCertifiedFinalAudio(t *testing.T) {
	videoPath := t.TempDir() + "/clip.mp4"
	audioPath := t.TempDir() + "/final_audio.m4a"
	videoBytes := []byte("canonical clip")
	audioBytes := []byte("canonical final audio")
	if err := os.WriteFile(videoPath, videoBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(audioPath, audioBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	videoHash := sha256.Sum256(videoBytes)
	audioHash := sha256.Sum256(audioBytes)
	timeline := audio.CanonicalTimeline{Version: audio.TimelineVersion, DurationUS: 1000000, Segments: []audio.TimelineSegment{{ID: "scene", Index: 0, DurationUS: 1000000, Video: audio.VideoSegment{AssetID: "clip", SourceInUS: 0, SourceDurationUS: 1000000}, Audio: audio.AudioIntent{Mode: audio.AudioSilence}}}}
	plan, err := render.Compile(render.CompileInput{
		JobID: "job-audio", Revision: "generation.v1", OutputPath: t.TempDir() + "/final.mp4", FPS: 30, Timeline: timeline,
		FinalAudio: &render.FinalAudioAsset{
			AssetID: "final-audio", AssetKind: "final_audio", Strategy: string(audio.FinalAudioCopy), Path: audioPath, SHA256: hex.EncodeToString(audioHash[:]), PlanSHA256: strings.Repeat("a", 64),
			AudioContractVersion: audio.AudioContractVersion, AudioPlanVersion: audio.AudioPlanVersion,
			Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo",
			DurationMS: 1000, StartPTS: 0, SizeBytes: int64(len(audioBytes)), FinalMix: true, CopyEligible: true,
		},
		Manifest: []render.AssetManifestEntry{{AssetID: "clip", Path: videoPath, SHA256: hex.EncodeToString(videoHash[:]), FrameCount: 1000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner
	stock := &StockRenderer{client: client, policy: mediaexec.EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 23}, profile: mediaexec.VideoProfile{}.WithDefaults()}
	validated, err := render.ValidateRenderPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := stock.RenderCanonicalPlan(context.Background(), validated); err != nil {
		t.Fatal(err)
	}
	if len(runner.inputs) != 2 {
		t.Fatalf("expected render plus mux_audio_copy, got %d calls", len(runner.inputs))
	}
	var first, second request
	if err := json.Unmarshal(runner.inputs[0], &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(runner.inputs[1], &second); err != nil {
		t.Fatal(err)
	}
	if first.Operation != OperationRenderStock || second.Operation != OperationMuxAudioCopy || len(second.InputPaths) != 2 {
		t.Fatalf("unexpected audio copy sequence: first=%+v second=%+v", first, second)
	}
	if first.OutputPath != plan.OutputPath+".video.mp4" || second.InputPaths[0] != first.OutputPath || second.InputPaths[1] != audioPath || second.OutputPath != plan.OutputPath {
		t.Fatalf("FINAL_AUDIO_COPY paths are not wired canonically: first=%+v second=%+v", first, second)
	}
}
