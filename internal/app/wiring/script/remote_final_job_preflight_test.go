package script

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
)

type recordingFinalJobPreflighter struct {
	request  rustexec.PreflightRequest
	decision rustexec.PreflightDecision
	err      error
}

func (p *recordingFinalJobPreflighter) Preflight(_ context.Context, request rustexec.PreflightRequest) (rustexec.PreflightDecision, error) {
	p.request = request
	return p.decision, p.err
}

func TestFinalJobMediaPreflightRequestUsesTheSealedPlanClock(t *testing.T) {
	var plan scriptgen.FinalJobCompiledPlanV2
	if err := json.Unmarshal([]byte(`{"duration_us":1000001,"output":{"fps_num":30,"fps_den":1},"video_tracks":[{"track_id":"video","segments":[{"segment_id":"base","timeline_start_frame":0,"frame_count":30}]},{"track_id":"overlay","segments":[{"segment_id":"overlay","timeline_start_frame":15,"frame_count":15}]}]}`), &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	request, err := finalJobMediaPreflightRequest(plan)
	if err != nil {
		t.Fatalf("finalJobMediaPreflightRequest: %v", err)
	}
	if request.VideoDurationMS == nil || *request.VideoDurationMS != 1001 {
		t.Fatalf("duration_ms = %v, want ceil(1000001us/1000)=1001", request.VideoDurationMS)
	}
	if len(request.Overlays) != 2 {
		t.Fatalf("overlays = %+v, want the two sealed track segments", request.Overlays)
	}
	if got := request.Overlays[1]; got.ItemID != "overlay" || got.Track != 1 || got.InUS != 500_000 || got.OutUS != 1_000_000 {
		t.Fatalf("overlay = %+v, want frame-exact windows on track 1", got)
	}
}

// TestFinalJobMediaPreflightRequestCoversSubMillisecondVideoEnd pins the r15
// failure: a 399,666,000us audio master is an exact millisecond, but 9592
// frames at 24 fps end at 399,666,666us. Declaring only 399,666ms made the Rust
// preflight reject the last video frame as overlay_beyond_video by 666us.
func TestFinalJobMediaPreflightRequestCoversSubMillisecondVideoEnd(t *testing.T) {
	var plan scriptgen.FinalJobCompiledPlanV2
	if err := json.Unmarshal([]byte(`{"duration_us":399666000,"output":{"fps_num":24,"fps_den":1},"video_tracks":[{"track_id":"video","segments":[{"segment_id":"closing","timeline_start_frame":0,"frame_count":9592}]}]}`), &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	request, err := finalJobMediaPreflightRequest(plan)
	if err != nil {
		t.Fatalf("finalJobMediaPreflightRequest: %v", err)
	}
	if request.VideoDurationMS == nil || *request.VideoDurationMS != 399_667 {
		t.Fatalf("duration_ms = %v, want 399667 to cover the frame-grid video end", request.VideoDurationMS)
	}
	if len(request.Overlays) != 1 || request.Overlays[0].OutUS != 399_666_666 {
		t.Fatalf("overlays = %+v, want the sealed video end at 399666666us", request.Overlays)
	}
	if request.Overlays[0].OutUS > *request.VideoDurationMS*1000 {
		t.Fatalf("video end %dus still exceeds the declared %dms", request.Overlays[0].OutUS, *request.VideoDurationMS)
	}
}

func TestFinalJobMediaPreflightBlocksBeforePrepareOnRustErrorIssues(t *testing.T) {
	preflighter := &recordingFinalJobPreflighter{decision: rustexec.PreflightDecision{
		Blocked:       true,
		BlockingCodes: []string{"overlay_track_overlap"},
	}}
	adapter := &RemoteFinalJobAdapter{preflight: preflighter}
	var plan scriptgen.FinalJobCompiledPlanV2
	if err := json.Unmarshal([]byte(`{"duration_us":1000000,"output":{"fps_num":30,"fps_den":1},"video_tracks":[{"track_id":"video","segments":[{"segment_id":"a","timeline_start_frame":0,"frame_count":20},{"segment_id":"b","timeline_start_frame":10,"frame_count":20}]}]}`), &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if err := adapter.preflightFinalJob(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "overlay_track_overlap") {
		t.Fatalf("preflight error = %v, want the stable blocking code", err)
	}
	if len(preflighter.request.Overlays) != 2 {
		t.Fatalf("Rust was not given both sealed segments: %+v", preflighter.request)
	}
}

func TestFinalJobMediaPreflightPropagatesTransportErrors(t *testing.T) {
	want := errors.New("worker unavailable")
	adapter := &RemoteFinalJobAdapter{preflight: &recordingFinalJobPreflighter{err: want}}
	var plan scriptgen.FinalJobCompiledPlanV2
	if err := json.Unmarshal([]byte(`{"duration_us":1000000,"output":{"fps_num":30,"fps_den":1}}`), &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if err := adapter.preflightFinalJob(context.Background(), plan); !errors.Is(err, want) {
		t.Fatalf("preflight error = %v, want wrapped transport error", err)
	}
}
