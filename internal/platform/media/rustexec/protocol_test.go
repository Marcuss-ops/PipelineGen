package rustexec

import (
	"encoding/json"
	"testing"
)

func TestRequestValidateMediaexecV2Operations(t *testing.T) {
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
			name: "render audio plan requires plan",
			req:  request{Version: ProtocolVersion, Operation: OperationRenderAudioPlan, OutputPath: "/out.m4a"},
			want: "render_audio_plan: audio_plan is required",
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
	if err := (request{Version: "mediaexec.v1", Operation: OperationHealth}).Validate(); err == nil {
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
