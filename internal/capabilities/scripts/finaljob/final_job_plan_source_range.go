package finaljob

import (
	"fmt"
	"strings"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// final_job_plan_source_windows.go — source-window quantization and video
// coverage of the final_job V2 plan: the frame math that binds a segment to the
// bytes it names and proves the track still covers the audio timeline.

// AlignFinalJobSegmentSourceWindows performs metadata-only frame quantization
// for prepared/non-stock assets. Raw stock ranges are already selected by the
// stockpipeline and remain immutable; the remote renderer may loop such a
// range for a longer timeline segment.
func AlignFinalJobSegmentSourceWindows(plan *FinalJobCompiledPlan, loopableStockIDs ...map[string]struct{}) error {
	if plan == nil || len(plan.VideoTracks) == 0 {
		return nil
	}
	stockIDs := map[string]struct{}{}
	if len(loopableStockIDs) > 0 && loopableStockIDs[0] != nil {
		stockIDs = loopableStockIDs[0]
	}
	assets := make(map[string]FinalJobPlanAsset, len(plan.Assets))
	for _, asset := range plan.Assets {
		assets[asset.AssetID] = asset
	}
	fpsNum, fpsDen := int64(plan.Output.FPSNum), int64(plan.Output.FPSDen)
	if fpsNum <= 0 || fpsDen <= 0 {
		output := FinalJobPlanOutputBlock()
		fpsNum, fpsDen = int64(output.FPSNum), int64(output.FPSDen)
	}
	for trackIndex := range plan.VideoTracks {
		for segmentIndex := range plan.VideoTracks[trackIndex].Segments {
			segment := &plan.VideoTracks[trackIndex].Segments[segmentIndex]
			if segment.FrameCount <= 0 {
				continue
			}
			if segment.StockPoolSlot {
				continue
			}
			asset, ok := assets[segment.AssetID]
			if !ok {
				return fmt.Errorf("segment %q has no plan asset %q", segment.SegmentID, segment.AssetID)
			}
			if _, loopable := stockIDs[segment.AssetID]; loopable {
				// Keep the stockpipeline's exact source start and duration. These
				// source frames may be fewer than the timeline FrameCount by design;
				// looping the bound range to fill the scene belongs to Master/worker.
				continue
			}
			totalFrames := asset.FrameCount
			if totalFrames <= 0 && asset.DurationUS > 0 {
				totalFrames = FinalJobFrameAtUS(asset.DurationUS, fpsNum, fpsDen)
				if totalFrames > 0 && FinalJobFrameStartUS(totalFrames, fpsNum, fpsDen) > asset.DurationUS {
					totalFrames--
				}
			}
			if totalFrames <= 0 {
				return fmt.Errorf("segment %q asset %q has no frame-addressable duration", segment.SegmentID, asset.AssetID)
			}
			shortfall := segment.FrameCount - totalFrames
			startFrame := FinalJobFrameAtUS(segment.SourceInUS, fpsNum, fpsDen)
			if shortfall > 1 || (shortfall == 1 && startFrame != 0) {
				return fmt.Errorf("segment %q asks for %d frames from asset %q, which carries %d", segment.SegmentID, segment.FrameCount, asset.AssetID, totalFrames)
			}
			if shortfall == 1 && segmentIndex == len(plan.VideoTracks[trackIndex].Segments)-1 {
				// The worker holds the last displayed frame when the certified
				// audio stretches the final scene one frame past a whole source
				// file. Keep the output frame count and clamp only the source
				// window: a second, sub-window segment would violate the
				// whole-file stock contract the Master packet-copies. Only the
				// trailing segment may hold; an earlier one would overlap its
				// successor on the timeline.
				segment.SourceInUS = 0
				segment.SourceDurationUS = asset.DurationUS
				continue
			}
			if shortfall > 0 {
				segment.FrameCount = totalFrames
			}
			maxStart := max(int64(0), totalFrames-segment.FrameCount)
			startFrame = min(max(startFrame, int64(0)), maxStart)
			segment.SourceInUS = FinalJobFrameStartUS(startFrame, fpsNum, fpsDen)
			segment.SourceDurationUS = FinalJobFrameStartUS(startFrame+segment.FrameCount, fpsNum, fpsDen) - segment.SourceInUS
			if overrun := segment.SourceInUS + segment.SourceDurationUS - asset.DurationUS; overrun > 0 {
				// A whole-frame window can exceed a container's reported duration
				// by a few microseconds because duration is rounded on a different
				// timebase (for example 55 frames at 24 fps end at 2,291,667us,
				// while the MP4 reports 2,291,666us). Keep the certified frame count
				// when the source starts within its extent and clamp only that tiny
				// timestamp discrepancy; the remote worker still validates the
				// source bytes before rendering.
				frameToleranceUS := FinalJobFrameStartUS(1, fpsNum, fpsDen)
				if shortfall <= 0 && segment.SourceInUS <= asset.DurationUS && overrun <= frameToleranceUS {
					segment.SourceDurationUS = asset.DurationUS - segment.SourceInUS
					continue
				}
				if shortfall <= 0 || overrun > FinalJobFrameStartUS(shortfall, fpsNum, fpsDen) {
					return fmt.Errorf("segment %q aligned source window exceeds asset %q: source_in_us=%d source_duration_us=%d asset_duration_us=%d frame_count=%d asset_frame_count=%d shortfall_frames=%d", segment.SegmentID, asset.AssetID, segment.SourceInUS, segment.SourceDurationUS, asset.DurationUS, segment.FrameCount, totalFrames, shortfall)
				}
			}
		}
	}
	return nil
}

// clampFinalJobRuntimeSourceWindows reconciles the plan with the durations in
// the runtime asset refs. The plan is first aligned against manifest metadata,
// then AlignFinalJobCompiledPlanAssetIDs replaces that metadata with the
// producer's authoritative durations. Container duration can be one rounding
// tick shorter than the frame-grid end (98 frames at 24 fps end at 4,083,333us
// while the artifact reports 4,083,332us), so re-align the source window after
// the rebinding instead of letting the strict binding validator reject the
// whole handoff. Only a sub-frame overrun is tolerated; anything larger is a
// real half-open-window violation and stays fail-closed.
func clampFinalJobRuntimeSourceWindows(plan *FinalJobCompiledPlan) error {
	if plan == nil {
		return nil
	}
	assets := make(map[string]FinalJobPlanAsset, len(plan.Assets))
	for _, asset := range plan.Assets {
		assets[asset.AssetID] = asset
	}
	fpsNum, fpsDen := int64(plan.Output.FPSNum), int64(plan.Output.FPSDen)
	if fpsNum <= 0 || fpsDen <= 0 {
		output := FinalJobPlanOutputBlock()
		fpsNum, fpsDen = int64(output.FPSNum), int64(output.FPSDen)
	}
	frameToleranceUS := FinalJobFrameStartUS(1, fpsNum, fpsDen)
	for trackIndex := range plan.VideoTracks {
		for segmentIndex := range plan.VideoTracks[trackIndex].Segments {
			segment := &plan.VideoTracks[trackIndex].Segments[segmentIndex]
			asset, ok := assets[segment.AssetID]
			if !ok || asset.DurationUS <= 0 || segment.SourceDurationUS <= 0 {
				continue
			}
			overrun := segment.SourceInUS + segment.SourceDurationUS - asset.DurationUS
			if overrun <= 0 {
				continue
			}
			// Only reconcile a window that is exactly a frame-grid span. An
			// arbitrary offset or duration is not container rounding; leave it
			// for the binding validator to reject.
			startFrame := FinalJobFrameAtUS(segment.SourceInUS, fpsNum, fpsDen)
			endFrame := FinalJobFrameAtUS(segment.SourceInUS+segment.SourceDurationUS, fpsNum, fpsDen)
			if segment.SourceInUS != FinalJobFrameStartUS(startFrame, fpsNum, fpsDen) ||
				segment.SourceInUS+segment.SourceDurationUS != FinalJobFrameStartUS(endFrame, fpsNum, fpsDen) ||
				endFrame-startFrame != segment.FrameCount {
				continue
			}
			if segment.SourceInUS > asset.DurationUS || overrun > frameToleranceUS {
				return fmt.Errorf("video_tracks[%d].segments[%d] asset %q: source_range_out_of_bounds after runtime binding (source_in_us=%d source_duration_us=%d asset_duration_us=%d)", trackIndex, segmentIndex, asset.AssetID, segment.SourceInUS, segment.SourceDurationUS, asset.DurationUS)
			}
			segment.SourceDurationUS = asset.DurationUS - segment.SourceInUS
		}
	}
	return nil
}

func FinalJobFrameAtUS(us, fpsNum, fpsDen int64) int64 {
	numerator := us * fpsNum
	denominator := int64(1_000_000) * fpsDen
	return (numerator + denominator/2) / denominator
}

func FinalJobFrameStartUS(frame, fpsNum, fpsDen int64) int64 {
	return kernelmedia.FrameIndexToUSAtFPS(frame, fpsNum, fpsDen)
}

// finalJobFramesCoveringUS returns how many output frames are needed to cover
// durationUS. The ceil subtracts one microsecond first: a frame start rounded
// UP to the microsecond grid (1441 frames = 60,041,667us) must not demand a
// 1442nd frame, while a genuinely partial frame (19,709,000us) still needs the
// 474th. This is the complement of FrameResolver.FrameAlignedDurationUS, which
// rounds the master up to a frame boundary.
func finalJobFramesCoveringUS(durationUS, fpsNum, fpsDen int64) int64 {
	if durationUS <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	adjusted := durationUS - 1
	denominator := int64(1_000_000) * fpsDen
	return (adjusted*fpsNum + denominator - 1) / denominator
}

// FinalJobCompleteVideoFrameCoverage closes the output timeline through the
// final audio frame. Audio duration remains the plan SSOT. When the trailing
// source still has frames, extend its window. When it is exhausted, absorb the
// last frame in an earlier stock-pool placement: those placements have no
// source binding and the Master loops the selected clip, while the remote
// plan validator requires an exact frame count. A run with no stock-pool
// placement at all (media_mode=clip_only, stock_enabled=disabled) certifies
// the same frame on its own trailing prepared clip, whose fragment the local
// preparer pads by exactly that one cloned frame; more than one missing frame
// stays fail-closed.
func FinalJobCompleteVideoFrameCoverage(segments []FinalJobPlanSegment, assets []FinalJobPlanAsset, durationUS, fpsNum, fpsDen int64) ([]FinalJobPlanSegment, error) {
	return finalJobCompleteVideoFrameCoverageWithStockAssets(segments, assets, durationUS, fpsNum, fpsDen, nil)
}

// finalJobSourceLockedAssetIDs names the assets whose source window is a
// binding and not a cut: a "stock" scene is picked by the stockpipeline and the
// Master packet-copies the whole file. Clip and overlay scenes also travel in
// the `stock` media slot of the remote contract, so the slot alone cannot
// decide this — the scene kind does.
func finalJobSourceLockedAssetIDs(scenes []FinalJobRemoteScene) map[string]struct{} {
	locked := make(map[string]struct{}, len(scenes))
	for _, scene := range scenes {
		if !strings.EqualFold(scene.Kind, "stock") {
			continue
		}
		if id := FinalJobStringValue(scene.Stock["asset_id"]); id != "" {
			locked[id] = struct{}{}
		}
	}
	return locked
}

// finalJobCompleteVideoFrameCoverageWithStockAssets keeps concrete stock
// windows immutable while allowing a trailing, non-stock clip to contribute
// its immediately following source frame — or, when its source is exhausted,
// one cloned copy of its final frame — when AAC packet rounding leaves the
// final audio master one video frame longer than the authored timeline.
//
// sourceLockedAssetIDs is the set of "stock" scene assets (see
// finalJobSourceLockedAssetIDs); every other video asset is a prepared cut the
// Producer renders and pads itself.
func finalJobCompleteVideoFrameCoverageWithStockAssets(segments []FinalJobPlanSegment, assets []FinalJobPlanAsset, durationUS, fpsNum, fpsDen int64, sourceLockedAssetIDs map[string]struct{}) ([]FinalJobPlanSegment, error) {
	if len(segments) == 0 || durationUS <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return nil, fmt.Errorf("final_job V2 cannot complete video coverage without segments and a valid output clock")
	}
	// The duration is the frame-aligned audio master, already rounded onto
	// the video grid. Re-applying a plain ceil double-rounds the microsecond
	// representation (1441 frames = 60,041,667us; *24 = 1441.000008) and
	// demands a frame the timeline does not have. The 1us guard keeps the
	// genuinely partial frame (19,709,000us -> 474) covered.
	neededFrames := finalJobFramesCoveringUS(durationUS, fpsNum, fpsDen)
	assetsByID := make(map[string]FinalJobPlanAsset, len(assets))
	for _, asset := range assets {
		assetsByID[asset.AssetID] = asset
	}
	lastIndex, endFrame := -1, int64(0)
	for i := range segments {
		end := segments[i].TimelineStartFrame + segments[i].FrameCount
		if end > endFrame {
			lastIndex, endFrame = i, end
		}
	}
	// The master is rounded up to the video grid, so a whole-file source can
	// be exactly one frame short. Cap the trailing placement whose frame count
	// is certified/exact: earlier placements are deliberately looped by the
	// worker, while a container-rounded file (4.979s for 120 frames) must keep
	// the sub-frame hold it already carries. A real stock binding is a trailing
	// placement too: its raw range is immutable, but its FrameCount must not
	// ask the preparer for a frame past the bound file (the r25-r30 1441-frame
	// WINDOW_CERTIFICATION_FAILED failure).
	if lastIndex >= 0 {
		segment := &segments[lastIndex]
		if asset, ok := assetsByID[segment.AssetID]; ok {
			totalFrames := asset.FrameCount
			certified := totalFrames > 0
			if totalFrames <= 0 && asset.DurationUS > 0 {
				totalFrames = FinalJobFrameAtUS(asset.DurationUS, fpsNum, fpsDen)
				if totalFrames > 0 && FinalJobFrameStartUS(totalFrames, fpsNum, fpsDen) > asset.DurationUS {
					totalFrames--
				}
				certified = totalFrames > 0 && FinalJobFrameStartUS(totalFrames, fpsNum, fpsDen) == asset.DurationUS
			}
			sourceStartFrame := FinalJobFrameAtUS(segment.SourceInUS, fpsNum, fpsDen)
			if sourceFrames := totalFrames - sourceStartFrame; certified && sourceFrames > 0 && segment.FrameCount == sourceFrames+1 {
				segment.FrameCount = sourceFrames
				endFrame = segment.TimelineStartFrame + segment.FrameCount
			}
		}
	}
	if endFrame >= neededFrames {
		return segments, nil
	}
	if lastIndex < 0 || segments[lastIndex].TimelineStartFrame+segments[lastIndex].FrameCount != endFrame {
		return nil, fmt.Errorf("final_job V2 video has no trailing segment at frame %d", endFrame)
	}
	segment := &segments[lastIndex]
	if segment.StockPoolSlot {
		// The stock binding fixes the source range; the preparer cannot be
		// asked for a frame beyond it. A single frame past the bound file is
		// the muxer's final hold, exactly like the trailing prepared asset
		// below. More than one frame is a real timeline/audio mismatch and
		// stays fail-closed instead of silently looping/truncating.
		sourceStart := FinalJobFrameAtUS(segment.SourceInUS, fpsNum, fpsDen)
		sourceEnd := FinalJobFrameAtUS(segment.SourceInUS+segment.SourceDurationUS, fpsNum, fpsDen)
		if sourceFrames := sourceEnd - sourceStart; sourceFrames > 0 && segment.FrameCount == sourceFrames+1 {
			segment.FrameCount = sourceFrames
			endFrame = segment.TimelineStartFrame + segment.FrameCount
		}
		if neededFrames-endFrame <= 1 {
			return segments, nil
		}
		return nil, fmt.Errorf("final_job V2 trailing stock segment %q needs %d more frames than its bound source", segment.SegmentID, neededFrames-endFrame)
	}
	assetIndex := -1
	for i := range assets {
		if assets[i].AssetID == segment.AssetID {
			assetIndex = i
			break
		}
	}
	if assetIndex < 0 {
		return nil, fmt.Errorf("final_job V2 trailing segment %q has no source asset", segment.SegmentID)
	}
	asset := assets[assetIndex]
	totalFrames := asset.FrameCount
	if totalFrames <= 0 {
		totalFrames = FinalJobFrameAtUS(asset.DurationUS, fpsNum, fpsDen)
		if totalFrames > 0 && FinalJobFrameStartUS(totalFrames, fpsNum, fpsDen) > asset.DurationUS {
			totalFrames--
		}
	}
	sourceStartFrame := FinalJobFrameAtUS(segment.SourceInUS, fpsNum, fpsDen)
	missing := neededFrames - endFrame
	available := totalFrames - sourceStartFrame - segment.FrameCount
	if segment.SourceInUS+segment.SourceDurationUS < asset.DurationUS {
		// A concrete stock binding owns an immutable source window. For a
		// trailing non-stock clip, one extra frame is safe when the rendered
		// asset contains it: it covers only the AAC-rounded audio tail and does
		// not turn a short authored scene into a loop or a longer take.
		if _, stockBound := sourceLockedAssetIDs[segment.AssetID]; stockBound || missing > 1 {
			available = 0
		} else {
			available = min(available, int64(1))
		}
	}
	if available > 0 {
		extend := min(available, missing)
		segment.FrameCount += extend
		segment.SourceDurationUS = FinalJobFrameStartUS(sourceStartFrame+segment.FrameCount, fpsNum, fpsDen) - segment.SourceInUS
		if segment.SourceInUS+segment.SourceDurationUS > asset.DurationUS {
			return nil, fmt.Errorf("final_job V2 trailing segment %q frame completion exceeds source %q duration", segment.SegmentID, segment.AssetID)
		}
		missing -= extend
		endFrame += extend
	}
	if missing == 0 {
		return segments, nil
	}
	if missing > 1 {
		return nil, fmt.Errorf("final_job V2 trailing segment %q needs %d more frames but source %q has no spare frames", segment.SegmentID, missing, segment.AssetID)
	}
	// The trailing source is fully consumed. The local muxer can hold its last
	// frame, but the remote Master validates the timeline frame count against
	// the plan duration and rejects the one-frame hold. Absorb the frame in the
	// nearest earlier stock-pool placement: that segment has no bound source and
	// is looped by the Master, and shifting the trailing placements by one
	// frame keeps the track contiguous. Overlay windows keep their absolute
	// times, so the shift is at most one frame of A/V offset.
	for i := lastIndex - 1; i >= 0; i-- {
		if !segments[i].StockPoolSlot {
			continue
		}
		segments[i].FrameCount++
		for j := i + 1; j < len(segments); j++ {
			segments[j].TimelineStartFrame++
		}
		return segments, nil
	}
	// No earlier loopable placement exists. A prepared clip still certifies the
	// audio master's final <1/24s itself: keep its source window inside the
	// asset and ask the timeline for one more frame. The trailing source stays
	// bound to the frames it certifies (AlignFinalJobSegmentSourceWindows) and
	// the local preparer clones its last frame (tpad stop_mode=clone capped by
	// -frames:v), so the prepared fragment carries exactly frame_count frames
	// and the timeline covers the audio master exactly. This is the r26b
	// clip_only shape: media_mode=clip_only with stock_enabled=disabled has no
	// StockPoolSlot anywhere, and the 7ms AAC tail made the run die at
	// PUBLISHING_DOCUMENTS instead of shipping the frame the muxer already
	// held.
	//
	// Stock-backed bytes must NOT take this shape: the Master packet-copies
	// the whole file, so a sub-window/short-source segment is inadmissible and
	// an unabsorbable stock tail stays fail-closed.
	if _, stockBound := sourceLockedAssetIDs[segment.AssetID]; stockBound || totalFrames <= 0 {
		return nil, fmt.Errorf("final_job V2 trailing segment %q needs one frame but no earlier stock-pool placement can absorb it", segment.SegmentID)
	}
	segment.FrameCount++
	return segments, nil
}

// FinalJobValidateVideoCoverage proves the sealed video track is either
// exactly as long as the frame-aligned audio timeline or exactly one frame
// short. That one frame is the hold the muxer owns: the audio master is
// rounded up to the video grid and the last source frame stays on screen for
// the final <1/24s. A larger gap would silently truncate the master, and an
// overrun would ask for frames the audio does not have; both stay fail-closed.
func FinalJobValidateVideoCoverage(plan FinalJobCompiledPlan) error {
	if plan.DurationUS <= 0 {
		return fmt.Errorf("final_job V2 video coverage requires a positive duration")
	}
	fpsNum, fpsDen := int64(plan.Output.FPSNum), int64(plan.Output.FPSDen)
	if fpsNum <= 0 || fpsDen <= 0 {
		output := FinalJobPlanOutputBlock()
		fpsNum, fpsDen = int64(output.FPSNum), int64(output.FPSDen)
	}
	neededFrames := finalJobFramesCoveringUS(plan.DurationUS, fpsNum, fpsDen)
	if neededFrames <= 0 {
		return fmt.Errorf("final_job V2 video coverage rounds to zero frames")
	}
	for trackIndex, track := range plan.VideoTracks {
		expectedStart := int64(0)
		for segmentIndex, segment := range track.Segments {
			if segment.TimelineStartFrame != expectedStart {
				return fmt.Errorf("video_tracks[%d].segments[%d]: timeline_start_frame %d does not continue frame %d", trackIndex, segmentIndex, segment.TimelineStartFrame, expectedStart)
			}
			if segment.FrameCount <= 0 {
				return fmt.Errorf("video_tracks[%d].segments[%d]: frame_count must be positive", trackIndex, segmentIndex)
			}
			expectedStart += segment.FrameCount
		}
		switch {
		case expectedStart > neededFrames:
			return fmt.Errorf("video_tracks[%d]: %d frames overrun the %d-frame audio timeline", trackIndex, expectedStart, neededFrames)
		case neededFrames-expectedStart > 1:
			return fmt.Errorf("video_tracks[%d]: %d frames leave a %d-frame gap before the %d-frame audio timeline (at most the final hold frame is allowed)", trackIndex, expectedStart, neededFrames-expectedStart, neededFrames)
		}
	}
	return nil
}
