package rustexec

import (
	"encoding/json"
	"strings"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

func TestRequestValidateMediaexecV1Operations(t *testing.T) {
	tests := []struct {
		name string
		req  request
		want string
	}{
		{
			name: "probe requires source",
			req:  request{Version: ProtocolVersion, Operation: OperationProbe},
			want: "probe: source_path is required",
		},
		{
			name: "cut batch requires jobs",
			req:  request{Version: ProtocolVersion, Operation: OperationCutBatch, SourcePath: "/input.mp4"},
			want: "cut_batch: jobs are required",
		},
		{
			name: "render requires output",
			req:  request{Version: ProtocolVersion, Operation: OperationRenderStock, InputPaths: []string{"/clip.mp4"}},
			want: "render_stock: output_path is required",
		},
		{
			name: "normalize requires source",
			req:  request{Version: ProtocolVersion, Operation: OperationNormalize, OutputPath: "/out.mp4"},
			want: "normalize: source_path is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.req.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRequestValidateRejectsUnsupportedVersionAndOperation(t *testing.T) {
	if err := (request{Version: "mediaexec.v2", Operation: OperationHealth}).Validate(); err == nil {
		t.Fatal("Validate() accepted unsupported protocol version")
	}
	if err := (request{Version: ProtocolVersion, Operation: Operation("run_command")}).Validate(); err == nil {
		t.Fatal("Validate() accepted unsupported operation")
	}
}

func TestRequestValidateAcceptsHealthEnvelope(t *testing.T) {
	req := request{Version: ProtocolVersion, Operation: OperationHealth}
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRequestValidateAcceptsRenderAudioPlanEnvelope(t *testing.T) {
	req := request{Version: ProtocolVersion, Operation: OperationRenderAudioPlan, OutputPath: "/tmp/final_audio.m4a", AudioPlan: json.RawMessage(`{"version":"compiled-audio-plan.v1"}`), AudioAssets: []audioAsset{{AssetID: "vo-1", Path: "/tmp/vo.mp3"}}}
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// sealedClipRenderPlan builds a valid, sealed ClipRenderPlanV1 through the
// canonical Compile (the same path the worker uses) so the transport tests
// exercise the real drift gate.
func sealedClipRenderPlan(t *testing.T) cliprender.ClipRenderPlanV1 {
	t.Helper()
	plan, err := cliprender.Compile(cliprender.CompileInput{
		RunID: "job-1",
		Source: &cliprender.MaterializedAsset{
			AssetID:   "asset-src",
			LocalPath: "/tmp/source.mp4",
			SHA256:    strings.Repeat("a", 64),
		},
		Contract: &cliprender.ResolvedContract{
			ContractID:   cliprender.OutputContractVeloxAssemblyReadyV1,
			Container:    "mp4",
			VideoCodec:   "h264",
			VideoProfile: "high",
			PixelFormat:  "yuv420p",
			Width:        1080,
			Height:       1920,
			FPSNum:       60,
			FPSDen:       1,
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		},
		AudioMode:  cliprender.AudioModeCopyIfCompatible,
		OutputPath: "/tmp/out.mp4",
	})
	if err != nil {
		t.Fatalf("build sealed clip plan: %v", err)
	}
	return plan
}

func marshalClipPlan(t *testing.T, plan cliprender.ClipRenderPlanV1) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal clip plan: %v", err)
	}
	return raw
}

func TestRequestValidateAcceptsRenderClipEnvelope(t *testing.T) {
	req := request{
		Version:    ProtocolVersion,
		Operation:  OperationRenderClip,
		SourcePath: "/tmp/source.mp4",
		OutputPath: "/tmp/out.mp4",
		ClipPlan:   marshalClipPlan(t, sealedClipRenderPlan(t)),
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRequestValidateRenderClipRequiresClipPlan(t *testing.T) {
	req := request{
		Version:    ProtocolVersion,
		Operation:  OperationRenderClip,
		SourcePath: "/tmp/source.mp4",
		OutputPath: "/tmp/out.mp4",
	}
	if err := req.Validate(); err == nil || !strings.Contains(err.Error(), "clip_plan is required") {
		t.Fatalf("Validate() = %v, want clip_plan required", err)
	}
}

func TestRequestValidateRenderClipRequiresSource(t *testing.T) {
	req := request{
		Version:    ProtocolVersion,
		Operation:  OperationRenderClip,
		OutputPath: "/tmp/out.mp4",
		ClipPlan:   marshalClipPlan(t, sealedClipRenderPlan(t)),
	}
	if err := req.Validate(); err == nil || !strings.Contains(err.Error(), "source_path is required") {
		t.Fatalf("Validate() = %v, want source_path required", err)
	}
}

func TestRequestValidateRenderClipRequiresOutput(t *testing.T) {
	req := request{
		Version:    ProtocolVersion,
		Operation:  OperationRenderClip,
		SourcePath: "/tmp/source.mp4",
		ClipPlan:   marshalClipPlan(t, sealedClipRenderPlan(t)),
	}
	if err := req.Validate(); err == nil || !strings.Contains(err.Error(), "output_path is required") {
		t.Fatalf("Validate() = %v, want output_path required", err)
	}
}

// TestRequestValidateRenderClipRejectsTamperedPlan verifies the transport is
// the last Go fail-closed boundary: a tampered plan (mutated after sealing)
// is rejected before any Rust process starts.
func TestRequestValidateRenderClipRejectsTamperedPlan(t *testing.T) {
	plan := sealedClipRenderPlan(t)
	plan.Output.FPSNum = 30 // mutate after seal → PlanSHA256 no longer matches
	req := request{
		Version:    ProtocolVersion,
		Operation:  OperationRenderClip,
		SourcePath: "/tmp/source.mp4",
		OutputPath: "/tmp/out.mp4",
		ClipPlan:   marshalClipPlan(t, plan),
	}
	err := req.Validate()
	if err == nil {
		t.Fatal("Validate() accepted a tampered clip plan")
	}
	if !strings.Contains(err.Error(), "sealed clip_plan validation failed") {
		t.Fatalf("Validate() = %v, want sealed clip_plan validation failure", err)
	}
}

func TestRequestValidateAcceptsMuxAudioCopyOnlyWithTwoInputs(t *testing.T) {
	req := request{Version: ProtocolVersion, Operation: OperationMuxAudioCopy, OutputPath: "/tmp/video-final.mp4", InputPaths: []string{"/tmp/video.mp4", "/tmp/final_audio.m4a"}}
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, inputs := range [][]string{nil, {"/tmp/video.mp4"}, {"/tmp/video.mp4", "/tmp/audio.m4a", "/tmp/extra.m4a"}} {
		req.InputPaths = inputs
		if err := req.Validate(); err == nil {
			t.Fatalf("Validate() accepted %d mux inputs", len(inputs))
		}
	}
}
