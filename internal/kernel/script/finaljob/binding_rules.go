package finaljob

import (
	"errors"
	"fmt"
	"strings"
)

// ValidatePlanBindingRules mirrors the admission surface the remote
// assembler applies to a compiled V2 plan, so a plan the worker would reject is
// rejected in this process instead. The rules were confirmed against the worker
// contract on 2026-11-01:
//
//   - assets[] must carry unique ids, and two assets may not share one
//     asset_key: the resolver rejects a second id for the same content;
//   - two assets may not certify the same bytes, because the plan refuses two
//     assets with one canonical identity;
//   - several segments MAY reference one asset. The placement lives on the
//     segment — unique segment_id, own timeline_start_frame and frame_count —
//     so a repeated window is one artifact referenced twice.
//
// The last rule is why identical work is canonicalised by sha256 before this
// check runs: the plan is only correct when every distinct byte-set has exactly
// one asset, whatever number of placements point at it.
func ValidatePlanBindingRules(plan CompiledPlanV2) error {
	if plan.StockPool != nil && (plan.StockPool.SourceKind != "drive" || strings.TrimSpace(plan.StockPool.DriveFolderID) == "" || plan.StockPool.Selection != "shuffle") {
		return fmt.Errorf("final_job plan has an invalid stock_pool contract")
	}
	byID := make(map[string]PlanAsset, len(plan.Assets))
	byKey := make(map[string]string, len(plan.Assets))
	bySHA := make(map[string]string, len(plan.Assets))
	for _, asset := range plan.Assets {
		id := strings.TrimSpace(asset.AssetID)
		if id == "" {
			return fmt.Errorf("final_job plan asset without an id")
		}
		if _, exists := byID[id]; exists {
			return fmt.Errorf("final_job plan assets repeat id %q", id)
		}
		byID[id] = asset
		key := strings.TrimSpace(asset.AssetKey)
		if key == "" {
			key = "sha256:" + strings.ToLower(asset.SHA256)
		}
		if owner, exists := byKey[key]; exists {
			return fmt.Errorf("final_job plan assets %q and %q share one asset key %q", owner, id, key)
		}
		byKey[key] = id
		if sha := strings.ToLower(strings.TrimSpace(asset.SHA256)); sha != "" {
			if owner, exists := bySHA[sha]; exists {
				return fmt.Errorf("final_job plan assets %q and %q certify the same bytes %s", owner, id, sha)
			}
			bySHA[sha] = id
		}
	}
	seenSegment := make(map[string]struct{}, len(plan.Assets))
	var violations []error
	for trackIndex, track := range plan.VideoTracks {
		for segmentIndex, segment := range track.Segments {
			id := strings.TrimSpace(segment.SegmentID)
			if id == "" {
				violations = append(violations, fmt.Errorf("final_job plan segment without an id at video_tracks[%d].segments[%d]", trackIndex, segmentIndex))
				continue
			}
			if _, exists := seenSegment[id]; exists {
				violations = append(violations, fmt.Errorf("final_job plan segments repeat id %q", id))
				continue
			}
			seenSegment[id] = struct{}{}
			if segment.StockPoolSlot {
				if plan.StockPool == nil {
					return fmt.Errorf("final_job plan segment %q uses stock_pool_slot without stock_pool", id)
				}
				if segment.AssetID != "" || segment.SHA256 != "" || segment.SourceInUS != 0 || segment.SourceDurationUS != 0 || segment.FrameCount <= 0 {
					return fmt.Errorf("final_job plan stock_pool_slot %q must leave its source to the Master", id)
				}
				continue
			}
			asset, ok := byID[segment.AssetID]
			if !ok {
				violations = append(violations, fmt.Errorf("final_job plan segment %q references unknown asset %q", id, segment.AssetID))
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(asset.SHA256), strings.TrimSpace(segment.SHA256)) {
				violations = append(violations, fmt.Errorf("final_job plan segment %q does not certify its asset %q", id, asset.AssetID))
				continue
			}
			if segment.FrameCount <= 0 {
				violations = append(violations, fmt.Errorf("final_job plan segment %q has no frame span", id))
				continue
			}
			if violation := sourceRangeViolation(trackIndex, segmentIndex, segment, asset); violation != "" {
				violations = append(violations, fmt.Errorf("invalid_compiled_render_plan: %s", violation))
			}
			if plan.Output.FPSNum > 0 && plan.Output.FPSDen > 0 {
				if violation := frameDurationViolation(id, trackIndex, segmentIndex, track.Segments, segment, int64(plan.Output.FPSNum), int64(plan.Output.FPSDen)); violation != "" {
					violations = append(violations, fmt.Errorf("invalid_compiled_render_plan: %s", violation))
				}
			}
		}
	}
	return errors.Join(violations...)
}

// FrameDurationUSAtFPS is the wire contract used by video.assemble.copy.v1:
// a frame span is the integer-microsecond floor of frame_count / fps. Keep the
// Producer validator on the same arithmetic so invalid plans fail before
// PREPARE instead of after the remote worker claims them.
func FrameDurationUSAtFPS(frameCount, fpsNum, fpsDen int64) int64 {
	if frameCount <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	return frameCount * 1_000_000 * fpsDen / fpsNum
}

// frameDurationViolation mirrors the packet-copy rule the Master applies to a
// segment: source_duration_us must describe exactly the frames the placement
// consumes. Two shapes are admissible on top of the floored span:
//
//   - one microsecond of slack, because a window quantized on the frame grid
//     (source_in_us + n frames) and the floored frame span differ by that tick
//     whenever frames/fps is not a whole number of microseconds;
//   - one cloned frame on the trailing placement of a track. The compiled plan
//     certifies the final <1/24s of the audio master on the last prepared
//     fragment and the local muxer clones that frame, so the source window is a
//     full frame shorter than the timeline placement.
//
// Anything else is a real source/timeline mismatch: the r15 (179 frames over
// 5,000,000us) and r22/r28 (a window longer than its frame count) failures stay
// fail-closed here, before PREPARE.
func frameDurationViolation(segmentID string, trackIndex, segmentIndex int, trackSegments []PlanSegment, segment PlanSegment, fpsNum, fpsDen int64) string {
	expectedUS := FrameDurationUSAtFPS(segment.FrameCount, fpsNum, fpsDen)
	delta := segment.SourceDurationUS - expectedUS
	if delta >= -1 && delta <= 1 {
		return ""
	}
	if delta < 0 && segmentIndex == len(trackSegments)-1 {
		if windowFrames := framesCoveringUS(segment.SourceDurationUS, fpsNum, fpsDen); segment.FrameCount-windowFrames == 1 {
			return ""
		}
	}
	return fmt.Sprintf(
		"segment %q video_tracks[%d].segments[%d] asset %q: source_duration_us=%d does not match frame_count=%d at %d/%d fps (expected %d)",
		segmentID, trackIndex, segmentIndex, segment.AssetID, segment.SourceDurationUS,
		segment.FrameCount, fpsNum, fpsDen, expectedUS,
	)
}

// framesCoveringUS rounds a microsecond window UP onto the output frame grid.
func framesCoveringUS(durationUS, fpsNum, fpsDen int64) int64 {
	if durationUS <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	denominator := int64(1_000_000) * fpsDen
	return (durationUS*fpsNum + denominator - 1) / denominator
}

// finalJobSourceRangeViolation mirrors the Master's source_range_out_of_bounds
// admission rule. It is the local half of the r36/r37 fix: a plan whose segment
// asks for more source than the asset certifies must die in this process, with
// the same segment coordinates the Master would print, before PREPARE and
// before any fragment is uploaded.
func sourceRangeViolation(trackIndex, segmentIndex int, segment PlanSegment, asset PlanAsset) string {
	if segment.SourceInUS < 0 {
		return fmt.Sprintf("video_tracks[%d].segments[%d]: source_in_us is negative (%d)", trackIndex, segmentIndex, segment.SourceInUS)
	}
	if segment.SourceDurationUS < 0 {
		return fmt.Sprintf("video_tracks[%d].segments[%d]: source_duration_us is negative (%d)", trackIndex, segmentIndex, segment.SourceDurationUS)
	}
	if segment.SourceDurationUS == 0 || asset.DurationUS <= 0 {
		// A zero duration is either a fragment whose asset carries the span in
		// frame_count (prepared path) or a plan this validator cannot certify.
		// The exact-span check below is meaningful only when both sides exist.
		return ""
	}
	if end := segment.SourceInUS + segment.SourceDurationUS; end > asset.DurationUS {
		return fmt.Sprintf(
			"video_tracks[%d].segments[%d] asset %q: source_range_out_of_bounds (expected source_in_us + source_duration_us <= %d, observed %d + %d)",
			trackIndex, segmentIndex, segment.AssetID, asset.DurationUS, segment.SourceInUS, segment.SourceDurationUS,
		)
	}
	return ""
}
