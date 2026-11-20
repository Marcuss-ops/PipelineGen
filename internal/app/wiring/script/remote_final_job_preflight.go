package script

import (
	"context"
	"fmt"
	"math"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
)

func finalJobMediaPreflightRequest(plan scriptgen.FinalJobCompiledPlanV2) (rustexec.PreflightRequest, error) {
	if plan.DurationUS <= 0 || plan.Output.FPSNum <= 0 || plan.Output.FPSDen <= 0 {
		return rustexec.PreflightRequest{}, fmt.Errorf("final_job preflight: compiled plan has an invalid duration or frame rate")
	}
	// The plan duration is the audio master, which can be an exact number of
	// milliseconds while the frame-aligned video track ends a few hundred
	// microseconds later (9592 frames at 24 fps = 399,666,666us against a
	// 399,666,000us master). The preflight checks overlays against the video
	// end, so declare the furthest sealed track end, not the audio duration:
	// a plain ms floor otherwise rejects the last video frame as
	// overlay_beyond_video by sub-millisecond rounding.
	videoEndUS := plan.DurationUS
	request := rustexec.PreflightRequest{}
	for trackIndex, track := range plan.VideoTracks {
		if uint64(trackIndex) > math.MaxUint32 {
			return rustexec.PreflightRequest{}, fmt.Errorf("final_job preflight: video track index %d exceeds mediaexec.v2 range", trackIndex)
		}
		for _, segment := range track.Segments {
			if strings.TrimSpace(segment.SegmentID) == "" || segment.TimelineStartFrame < 0 || segment.FrameCount <= 0 || segment.TimelineStartFrame > math.MaxInt64-segment.FrameCount {
				return rustexec.PreflightRequest{}, fmt.Errorf("final_job preflight: segment %q has an invalid timeline window", segment.SegmentID)
			}
			inUS, err := finalJobFrameToUS(segment.TimelineStartFrame, plan.Output.FPSNum, plan.Output.FPSDen)
			if err != nil {
				return rustexec.PreflightRequest{}, fmt.Errorf("final_job preflight: segment %q start: %w", segment.SegmentID, err)
			}
			outUS, err := finalJobFrameToUS(segment.TimelineStartFrame+segment.FrameCount, plan.Output.FPSNum, plan.Output.FPSDen)
			if err != nil {
				return rustexec.PreflightRequest{}, fmt.Errorf("final_job preflight: segment %q end: %w", segment.SegmentID, err)
			}
			if outUS > videoEndUS {
				videoEndUS = outUS
			}
			request.Overlays = append(request.Overlays, rustexec.PreflightOverlay{
				ItemID: segment.SegmentID,
				Track:  uint32(trackIndex),
				InUS:   inUS,
				OutUS:  outUS,
			})
		}
	}
	durationMS := videoEndUS / 1000
	if videoEndUS%1000 != 0 {
		durationMS++
	}
	if durationMS <= 0 {
		return rustexec.PreflightRequest{}, fmt.Errorf("final_job preflight: compiled plan video end rounds to zero milliseconds")
	}
	request.VideoDurationMS = &durationMS
	return request, nil
}

func finalJobFrameToUS(frame int64, fpsNum, fpsDen int) (int64, error) {
	if frame < 0 || fpsNum <= 0 || fpsDen <= 0 {
		return 0, fmt.Errorf("invalid frame clock")
	}
	scale := int64(fpsDen) * 1_000_000
	if scale <= 0 || frame > math.MaxInt64/scale {
		return 0, fmt.Errorf("frame clock overflows microseconds")
	}
	return frame * scale / int64(fpsNum), nil
}

func (a *RemoteFinalJobAdapter) preflightFinalJob(ctx context.Context, plan scriptgen.FinalJobCompiledPlanV2) error {
	if a == nil || a.preflight == nil {
		return fmt.Errorf("remote final-job Rust media preflight is not configured")
	}
	request, err := finalJobMediaPreflightRequest(plan)
	if err != nil {
		return err
	}
	decision, err := a.preflight.Preflight(ctx, request)
	if err != nil {
		return fmt.Errorf("final_job Rust media preflight: %w", err)
	}
	if decision.Blocked {
		codes := decision.BlockingCodes
		if len(codes) == 0 {
			codes = []string{"preflight_blocked"}
		}
		return fmt.Errorf("final_job Rust media preflight blocked the handoff (%s)", strings.Join(codes, ", "))
	}
	return nil
}
