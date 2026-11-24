package finaljob

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/finaljob"
)

// These wire structs mirror the strict CompiledRenderPlanV2 contract owned by
// the Master. Keep field order and omission rules aligned with
// velox-shared/contract.CompiledRenderPlanV2: the Master verifies canonical
// JSON byte-for-byte before dispatching the plan to worker 51.
type FinalJobCompiledPlan = finaljob.CompiledPlanV2
type finalJobPlanOutput = finaljob.PlanOutput
type FinalJobPlanAudio = finaljob.PlanAudio
type FinalJobPlanVideoTrack = finaljob.PlanVideoTrack
type FinalJobPlanSegment = finaljob.PlanSegment
type FinalJobPlanAsset = finaljob.PlanAsset

type FinalJobBaseSegment struct {
	StartUS, EndUS   int64
	SourceInUS       int64
	SourceDurationUS int64
	SourceLocked     bool
	AssetID          string
	SHA256           string
	StockPool        bool
}

func ValidateFinalJobPlanBindingRules(plan FinalJobCompiledPlan) error {
	return finaljob.ValidatePlanBindingRules(plan)
}

type FinalJobOverlayWindow struct {
	StartUS, EndUS int64
	ArtifactInUS   int64
	// ArtifactDurationUS is the certified length of the rendered artifact.
	// Every window must fit inside it: the Master validates the same range.
	ArtifactDurationUS int64
	AssetID            string
	SHA256             string
}

func FinalJobCompiledPlanPayload(scenes []map[string]any, overlayRows []any, overlayManifest map[string]any, audio contract.FinalAudioReference, audioRef map[string]any, timelineDurationUS int64) (string, string, error) {
	return FinalJobCompiledPlanPayloadWithStockPool(scenes, overlayRows, overlayManifest, audio, audioRef, timelineDurationUS, "", nil)
}

// FinalJobCompiledPlanPayloadWithStockPool builds the sealed V2 plan. The
// stock binding windows are keyed by scene id and stay Producer-local: the
// remote Master only receives the compiled plan and runtime asset refs.
func FinalJobCompiledPlanPayloadWithStockPool(scenes []map[string]any, overlayRows []any, overlayManifest map[string]any, audio contract.FinalAudioReference, audioRef map[string]any, timelineDurationUS int64, stockPoolFolderID string, stockBoundWindowsMS map[string]int64) (string, string, error) {
	if len(scenes) == 0 {
		return "", "", fmt.Errorf("final_job V2 render plan requires at least one visual scene")
	}
	if audio.DurationUS <= 0 && audio.DurationMS > 0 {
		audio.DurationUS = audio.DurationMS * 1000
	}
	if audio.DurationUS <= 0 || audio.DurationMS <= 0 || !audio.CopyEligible || !audio.FinalMix {
		return "", "", fmt.Errorf("final_job V2 render plan requires certified final audio")
	}
	if timelineDurationUS <= 0 {
		return "", "", fmt.Errorf("final_job V2 render plan requires a positive canonical duration")
	}
	// The plan's output duration must match the certified media duration. The
	// canonical scene/audio timeline can differ by a few milliseconds because
	// the final AAC container reports packet-rounded duration; keep that drift
	// within the existing 40 ms alignment allowance, then use the actual final
	// audio duration as the worker timeline SSOT. Video coverage is completed
	// on the output frame grid below without changing this certified duration.
	if delta := audio.DurationUS - timelineDurationUS; delta < -capabilityaudio.FinalAudioDurationToleranceUS || delta > capabilityaudio.FinalAudioDurationToleranceUS {
		return "", "", fmt.Errorf("final_job V2 final audio duration %dus does not match canonical timeline %dus", audio.DurationUS, timelineDurationUS)
	}
	timelineDurationUS = audio.DurationUS
	delivery := finalJobPlanDelivery()
	fpsNum, fpsDen := int64(delivery.FPS.Num), int64(delivery.FPS.Den)
	assets := &finalJobPlanAssets{}
	base, err := finalJobPlanBaseSegments(scenes, assets, stockBoundWindowsMS)
	if err != nil {
		return "", "", err
	}
	if err := assets.addRaw(audioRef, "final_audio", audio.DurationUS, 0, 0, 0); err != nil {
		return "", "", fmt.Errorf("final_job V2 final audio: %w", err)
	}
	baseEnd := base[len(base)-1].EndUS
	delta := timelineDurationUS - baseEnd
	if delta < -capabilityaudio.FinalAudioDurationToleranceUS || delta > capabilityaudio.FinalAudioDurationToleranceUS {
		return "", "", fmt.Errorf("final_job V2 scene duration %dus does not match certified final audio %dus", baseEnd, timelineDurationUS)
	}
	base[len(base)-1].EndUS = timelineDurationUS
	if base[len(base)-1].EndUS <= base[len(base)-1].StartUS {
		return "", "", fmt.Errorf("final_job V2 final scene has no positive duration after audio alignment")
	}

	windows, manifests, err := finalJobOverlayWindows(overlayManifest, overlayRows, assets, timelineDurationUS, fpsNum, fpsDen, delivery.Width, delivery.Height)
	if err != nil {
		return "", "", err
	}
	windows, err = finalJobMergeOverlayWindows(windows)
	if err != nil {
		return "", "", err
	}
	segments, err := FinalJobComposePlanSegments(base, windows, timelineDurationUS, fpsNum, fpsDen)
	if err != nil {
		return "", "", err
	}
	if len(segments) == 0 {
		return "", "", fmt.Errorf("final_job V2 render plan has no video segments")
	}
	// Producer deliberately keeps stock remote: it does not materialize Drive
	// bytes just to probe duration. The scene references can describe several
	// contiguous windows of one stock SHA, so declare the furthest range the
	// compiled plan actually needs and let the Master/worker verify that range
	// against the canonical Drive asset before dispatch.
	stockAssetIDs := finalJobRetainPlanAssets(scenes, segments, assets, FinalJobStringValue(audioRef["asset_id"]))

	timelineRevision := int64(1)
	timelineSHA, err := finalJobTimelineIdentity(timelineDurationUS, audio.PlanSHA256, manifests)
	if err != nil {
		return "", "", err
	}
	plan := FinalJobCompiledPlan{
		PlanVersion: 2, TimelineRevision: timelineRevision, TimelineSHA256: timelineSHA,
		DurationUS: timelineDurationUS,
		Output:     FinalJobPlanOutputBlock(),
		FinalAudio: FinalJobPlanAudio{
			Mode: "FINAL_AUDIO_COPY", AssetID: FinalJobStringValue(audioRef["asset_id"]),
			SHA256: strings.ToLower(FinalJobStringValue(audioRef["sha256"])), SizeBytes: finalJobInt64(audioRef["size_bytes"]),
			Codec: delivery.AudioCodec, SampleRateHz: delivery.AudioSampleRate, Channels: delivery.AudioChannels,
			DurationUS:       audio.DurationUS,
			TimelineRevision: timelineRevision, TimelineSHA256: timelineSHA,
		},
		VideoTracks: []FinalJobPlanVideoTrack{{TrackID: "main-video", Segments: segments}},
		Assets:      assets.sorted(),
	}
	if stockPoolFolderID = strings.TrimSpace(stockPoolFolderID); stockPoolFolderID != "" {
		plan.StockPool = &finaljob.PlanStockPool{SourceKind: "drive", DriveFolderID: stockPoolFolderID, Selection: "shuffle"}
	}
	return finalJobEncodeCompiledPlan(&plan, scenes, stockAssetIDs, fpsNum, fpsDen)
}

func finalJobTimelineIdentity(durationUS int64, audioPlanSHA string, manifests []map[string]any) (string, error) {
	material := map[string]any{
		"duration_us":             durationUS,
		"audio_plan_sha256":       strings.ToLower(strings.TrimSpace(audioPlanSHA)),
		"overlay_timeline_sha256": "",
	}
	if len(manifests) == 1 {
		if asset, ok := manifests[0]["asset"].(map[string]any); ok {
			material["overlay_timeline_sha256"] = strings.ToLower(FinalJobStringValue(asset["sha256"]))
		}
	} else if len(manifests) > 1 {
		hashes := make([]string, 0, len(manifests))
		for _, manifest := range manifests {
			asset := manifest["asset"].(map[string]any)
			hashes = append(hashes, strings.ToLower(FinalJobStringValue(asset["sha256"])))
		}
		material["overlay_timeline_sha256"] = digest.SHA256String(strings.Join(hashes, "\x00"))
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal final_job V2 timeline identity: %w", err)
	}
	return digest.SHA256String(string(encoded)), nil
}

func finalJobRetainPlanAssets(scenes []map[string]any, segments []FinalJobPlanSegment, assets *finalJobPlanAssets, audioAssetID string) map[string]struct{} {
	stockAssetIDs := make(map[string]struct{}, len(scenes))
	for _, scene := range scenes {
		if stock, ok := scene["stock"].(map[string]any); ok {
			if id := FinalJobStringValue(stock["asset_id"]); id != "" {
				stockAssetIDs[id] = struct{}{}
			}
		}
	}
	for _, segment := range segments {
		if _, stock := stockAssetIDs[segment.AssetID]; stock {
			assets.extendSourceExtent(segment.AssetID, segment.SourceInUS+segment.SourceDurationUS)
		}
	}
	// The remote Master treats plan.Assets as the authoritative materialization
	// manifest. Drop sources that no surviving segment or final audio binding uses.
	assets.retainReferenced(segments, audioAssetID)
	return stockAssetIDs
}

func finalJobEncodeCompiledPlan(plan *FinalJobCompiledPlan, scenes []map[string]any, stockAssetIDs map[string]struct{}, fpsNum, fpsDen int64) (string, string, error) {
	if err := AlignFinalJobSegmentSourceWindows(plan, stockAssetIDs); err != nil {
		return "", "", fmt.Errorf("align final_job source windows to the output frame grid: %w", err)
	}
	// The stock slot carries stock, clip and overlay scenes alike, so the
	// frame-coverage pass needs the immutable subset: only a "stock" scene's
	// bytes are packet-copied by the Master and cannot be padded locally.
	var err error
	plan.VideoTracks[0].Segments, err = finalJobCompleteVideoFrameCoverageWithStockAssets(plan.VideoTracks[0].Segments, plan.Assets, plan.DurationUS, fpsNum, fpsDen, finalJobSourceLockedAssetIDs(scenes))
	if err != nil {
		return "", "", err
	}
	if err := AlignFinalJobSegmentSourceWindows(plan, stockAssetIDs); err != nil {
		return "", "", fmt.Errorf("align final_job source windows after frame coverage: %w", err)
	}
	if err := FinalJobValidateVideoCoverage(*plan); err != nil {
		return "", "", fmt.Errorf("final_job V2 compiled-plan video coverage: %w", err)
	}
	if err := ValidateFinalJobPlanBindingRules(*plan); err != nil {
		return "", "", fmt.Errorf("final_job V2 compiled-plan asset bindings: %w", err)
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return "", "", fmt.Errorf("marshal final_job canonical V2 render plan: %w", err)
	}
	return string(canonical), digest.SHA256String(string(canonical)), nil
}

func finalJobOverlayWindows(overlayManifest map[string]any, overlayRows []any, assets *finalJobPlanAssets, timelineDurationUS, fpsNum, fpsDen int64, width, height int) ([]FinalJobOverlayWindow, []map[string]any, error) {
	manifests := make([]map[string]any, 0, 1)
	if overlayManifest != nil {
		if overlayManifest["schema_version"] == "pipelinegen.overlay-manifest-set.v1" {
			rawManifests, ok := overlayManifest["manifests"].([]any)
			if !ok || len(rawManifests) == 0 {
				return nil, nil, fmt.Errorf("final_job overlay manifest set has no manifests")
			}
			for _, raw := range rawManifests {
				manifest, ok := raw.(map[string]any)
				if !ok {
					return nil, nil, fmt.Errorf("final_job overlay manifest set contains an invalid manifest")
				}
				manifests = append(manifests, manifest)
			}
		} else {
			manifests = append(manifests, overlayManifest)
		}
	}
	windows := make([]FinalJobOverlayWindow, 0)
	for _, manifest := range manifests {
		asset, ok := manifest["asset"].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("final_job overlay manifest has no typed asset")
		}
		assetID := FinalJobStringValue(asset["asset_id"])
		sha := strings.ToLower(FinalJobStringValue(asset["sha256"]))
		entries, ok := manifest["entries"].([]any)
		if !ok {
			return nil, nil, fmt.Errorf("final_job overlay manifest entries have an invalid shape")
		}
		durationUS := finalJobOverlayAssetDurationUS(asset, entries, fpsNum, fpsDen)
		if durationUS <= 0 {
			return nil, nil, fmt.Errorf("final_job overlay manifest asset %q has no certifiable duration", assetID)
		}
		if err := assets.addRaw(asset, "video", durationUS, width, height, finalJobInt64(asset["frame_count"])); err != nil {
			return nil, nil, fmt.Errorf("final_job overlay timeline asset: %w", err)
		}
		for i, raw := range entries {
			entry, ok := raw.(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("final_job overlay manifest entry %d has an invalid shape", i)
			}
			startUS, endUS := finalJobInt64(entry["in_us"]), finalJobInt64(entry["out_us"])
			artifactInUS, artifactOutUS := startUS, endUS
			if value, present := entry["artifact_in_us"]; present {
				artifactInUS = finalJobInt64(value)
			}
			if value, present := entry["artifact_out_us"]; present {
				artifactOutUS = finalJobInt64(value)
			}
			if startUS < 0 || endUS <= startUS || artifactInUS < 0 || artifactOutUS-artifactInUS != endUS-startUS {
				return nil, nil, fmt.Errorf("final_job overlay manifest entry %d has a non-projectable timing window", i)
			}
			if startUS >= timelineDurationUS {
				continue
			}
			if endUS > timelineDurationUS {
				artifactOutUS -= endUS - timelineDurationUS
				endUS = timelineDurationUS
			}
			// The Master validates every projected window against the certified
			// artifact duration, so trim any tail that lies beyond the rendered file.
			if clipUS := artifactInUS + (endUS - startUS) - durationUS; clipUS > 0 {
				if endUS-startUS <= clipUS {
					continue
				}
				artifactOutUS -= clipUS
				endUS -= clipUS
			}
			if endUS > startUS {
				windows = append(windows, FinalJobOverlayWindow{StartUS: startUS, EndUS: endUS, ArtifactInUS: artifactInUS, ArtifactDurationUS: durationUS, AssetID: assetID, SHA256: sha})
			}
		}
	}
	dedicated, err := finalJobDedicatedOverlayWindows(overlayRows, assets, timelineDurationUS, fpsNum, fpsDen, width, height)
	if err != nil {
		return nil, nil, err
	}
	windows = append(windows, dedicated...)
	return windows, manifests, nil
}

func finalJobDedicatedOverlayWindows(overlayRows []any, assets *finalJobPlanAssets, timelineDurationUS, fpsNum, fpsDen int64, width, height int) ([]FinalJobOverlayWindow, error) {
	windows := make([]FinalJobOverlayWindow, 0, len(overlayRows))
	for i, raw := range overlayRows {
		row, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("final_job dedicated overlay row %d has an invalid shape", i)
		}
		assetID := FinalJobStringValue(row["asset_id"])
		sha := strings.ToLower(FinalJobStringValue(row["sha256"]))
		startFrame, endFrame := finalJobInt64(row["start_frame"]), finalJobInt64(row["end_frame"])
		if startFrame < 0 || endFrame <= startFrame {
			return nil, fmt.Errorf("final_job dedicated overlay row %d has an invalid frame window", i)
		}
		startUS := startFrame * 1_000_000 * fpsDen / fpsNum
		endUS := endFrame * 1_000_000 * fpsDen / fpsNum
		if startUS >= timelineDurationUS {
			continue
		}
		if endUS > timelineDurationUS {
			endUS = timelineDurationUS
		}
		durationUS := finalJobInt64(row["duration_ms"]) * 1000
		if durationUS <= 0 {
			durationUS = endUS - startUS
		}
		if endUS-startUS > durationUS {
			endUS = startUS + durationUS
		}
		if endUS <= startUS {
			continue
		}
		if err := assets.addRaw(row, "video", durationUS, width, height, finalJobInt64(row["frame_count"])); err != nil {
			return nil, fmt.Errorf("final_job dedicated overlay row %d: %w", i, err)
		}
		windows = append(windows, FinalJobOverlayWindow{StartUS: startUS, EndUS: endUS, ArtifactInUS: 0, ArtifactDurationUS: durationUS, AssetID: assetID, SHA256: sha})
	}
	return windows, nil
}

// finalJobPlanBaseSegments describes one source segment per scene and types
// every stock asset INTO the shared registry the caller already owns.
func finalJobPlanBaseSegments(scenes []map[string]any, assets *finalJobPlanAssets, stockBoundWindowsMS map[string]int64) ([]FinalJobBaseSegment, error) {
	base := make([]FinalJobBaseSegment, 0, len(scenes))
	var cursorUS int64
	for i, scene := range scenes {
		durationSeconds, ok := scene["duration_seconds"].(float64)
		if !ok || math.IsNaN(durationSeconds) || math.IsInf(durationSeconds, 0) || durationSeconds <= 0 {
			return nil, fmt.Errorf("final_job scene %d has an invalid duration", i)
		}
		durationUS := int64(math.Round(durationSeconds * 1_000_000))
		if poolSlot, _ := scene["stock_pool_slot"].(bool); poolSlot {
			base = append(base, FinalJobBaseSegment{StartUS: cursorUS, EndUS: cursorUS + durationUS, StockPool: true})
			cursorUS += durationUS
			continue
		}
		asset, ok := scene["stock"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("final_job scene %d has no video asset", i)
		}
		// Keep the actual saved-file duration in the asset registry. The source
		// segment below may reference a shorter binding window; the worker loops
		// that window to cover the full scene timeline.
		assetDurationUS := finalJobInt64(asset["duration_ms"]) * 1000
		if assetDurationUS <= 0 {
			return nil, fmt.Errorf("final_job scene %d stock asset has no positive saved duration", i)
		}
		if err := assets.addRaw(asset, "video", assetDurationUS, finalJobInt(asset["width"]), finalJobInt(asset["height"]), finalJobInt64(asset["frame_count"])); err != nil {
			return nil, fmt.Errorf("final_job scene %d: %w", i, err)
		}
		SourceInUS := finalJobInt64(asset["start_ms"]) * 1000
		SourceDurationUS := assetDurationUS
		SourceLocked := strings.EqualFold(FinalJobStringValue(scene["kind"]), "stock")
		if SourceLocked {
			// The stockpipeline owns and selects this exact source window. Keep it
			// unchanged here; the scene timeline is allowed to be longer because
			// the remote renderer loops the bound stock window.
			if boundMS := stockBoundWindowsMS[FinalJobStringValue(scene["scene_id"])]; boundMS > 0 {
				SourceDurationUS = boundMS * 1000
			} else {
				SourceDurationUS = assetDurationUS - SourceInUS
			}
			if SourceInUS < 0 || SourceDurationUS <= 0 || SourceInUS+SourceDurationUS > assetDurationUS {
				return nil, fmt.Errorf("final_job stock scene %d bound window %d+%d exceeds saved file duration %d", i, SourceInUS, SourceDurationUS, assetDurationUS)
			}
		}
		AssetID := FinalJobStringValue(asset["asset_id"])
		sha := strings.ToLower(FinalJobStringValue(asset["sha256"]))
		base = append(base, FinalJobBaseSegment{StartUS: cursorUS, EndUS: cursorUS + durationUS, SourceInUS: SourceInUS, SourceDurationUS: SourceDurationUS, SourceLocked: SourceLocked, AssetID: AssetID, SHA256: sha})
		cursorUS += durationUS
	}
	return base, nil
}

func finalJobMergeOverlayWindows(input []FinalJobOverlayWindow) ([]FinalJobOverlayWindow, error) {
	sort.SliceStable(input, func(i, j int) bool {
		if input[i].StartUS != input[j].StartUS {
			return input[i].StartUS < input[j].StartUS
		}
		return input[i].EndUS < input[j].EndUS
	})
	merged := make([]FinalJobOverlayWindow, 0, len(input))
	for _, current := range input {
		if current.EndUS <= current.StartUS || current.AssetID == "" || len(current.SHA256) != 64 {
			return nil, fmt.Errorf("final_job overlay timeline contains an invalid replacement window")
		}
		if len(merged) > 0 && current.StartUS < merged[len(merged)-1].EndUS {
			prior := &merged[len(merged)-1]
			if prior.AssetID != current.AssetID || prior.SHA256 != current.SHA256 || prior.ArtifactInUS-prior.StartUS != current.ArtifactInUS-current.StartUS {
				return nil, fmt.Errorf("final_job overlay replacement windows overlap with different artifact timing")
			}
			if current.EndUS > prior.EndUS {
				prior.EndUS = current.EndUS
			}
			continue
		}
		// Adjacent windows of the SAME artifact that are also contiguous inside
		// it are one continuous take: keeping them apart would pay two fragment
		// encodes (and two verifications, two uploads) for one piece of video.
		if len(merged) > 0 {
			prior := &merged[len(merged)-1]
			if prior.AssetID == current.AssetID && prior.SHA256 == current.SHA256 &&
				prior.EndUS == current.StartUS &&
				prior.ArtifactInUS+(prior.EndUS-prior.StartUS) == current.ArtifactInUS {
				prior.EndUS = current.EndUS
				continue
			}
		}
		merged = append(merged, current)
	}
	return merged, nil
}

func FinalJobComposePlanSegments(base []FinalJobBaseSegment, overlays []FinalJobOverlayWindow, durationUS, fpsNum, fpsDen int64) ([]FinalJobPlanSegment, error) {
	segments := make([]FinalJobPlanSegment, 0, len(base)+2*len(overlays))
	cursorUS := int64(0)
	// nextFrame is the output-frame cursor. Every segment starts where the
	// previous one ended, so partial-microsecond boundaries can never open a
	// one-frame gap or overlap between adjacent segments.
	nextFrame := int64(0)
	var err error
	for _, overlay := range overlays {
		if overlay.StartUS < cursorUS || overlay.EndUS > durationUS {
			return nil, fmt.Errorf("final_job overlay windows are not ordered within the output timeline")
		}
		segments, nextFrame, err = finalJobAppendBaseSegments(base, cursorUS, overlay.StartUS, segments, nextFrame, fpsNum, fpsDen)
		if err != nil {
			return nil, err
		}
		startFrame := nextFrame
		// Overlay artifacts are trimmed to their certified frame extent, not
		// expanded to cover a partial trailing frame: their source bytes end at
		// the artifact duration and a ceiling here would ask for a frame the
		// artifact does not contain.
		endFrame := FinalJobFrameAtUS(overlay.EndUS, fpsNum, fpsDen)
		if endFrame > startFrame {
			SourceInUS := overlay.ArtifactInUS
			SourceDurationUS := overlay.EndUS - overlay.StartUS
			if overlay.ArtifactDurationUS > 0 {
				if SourceDurationUS > overlay.ArtifactDurationUS {
					return nil, fmt.Errorf("final_job overlay window %q (%dus) exceeds artifact duration %dus", overlay.AssetID, SourceDurationUS, overlay.ArtifactDurationUS)
				}
				// Defensive shift, same as the base path: never emit a source
				// range the Master must reject, whichever caller built the window.
				if maxSourceInUS := overlay.ArtifactDurationUS - SourceDurationUS; SourceInUS > maxSourceInUS {
					SourceInUS = maxSourceInUS
				}
			}
			segments = append(segments, FinalJobPlanSegment{
				SegmentID: fmt.Sprintf("segment-%04d", len(segments)), AssetID: overlay.AssetID, SHA256: overlay.SHA256,
				TimelineStartFrame: startFrame, FrameCount: endFrame - startFrame,
				SourceInUS: SourceInUS, SourceDurationUS: SourceDurationUS,
			})
			nextFrame = endFrame
		}
		cursorUS = overlay.EndUS
	}
	segments, nextFrame, err = finalJobAppendBaseSegments(base, cursorUS, durationUS, segments, nextFrame, fpsNum, fpsDen)
	if err != nil {
		return nil, err
	}
	segments = finalJobMergeContiguousSegments(segments)
	for i, segment := range segments {
		if segment.FrameCount <= 0 || (!segment.StockPoolSlot && segment.SourceDurationUS <= 0) {
			return nil, fmt.Errorf("final_job V2 segment %d has an empty frame or source window (frames=%d source_duration_us=%d asset=%q)", i, segment.FrameCount, segment.SourceDurationUS, segment.AssetID)
		}
	}
	return segments, nil
}

func finalJobAppendBaseSegments(base []FinalJobBaseSegment, startUS, endUS int64, segments []FinalJobPlanSegment, nextFrame, fpsNum, fpsDen int64) ([]FinalJobPlanSegment, int64, error) {
	if endUS <= startUS {
		return segments, nextFrame, nil
	}
	for _, source := range base {
		inUS, outUS := max(startUS, source.StartUS), min(endUS, source.EndUS)
		if outUS <= inUS {
			continue
		}
		startFrame := nextFrame
		// The end boundary counts covered frames, including a partial trailing
		// frame, so rounding down could leave the certified audio one frame short.
		endFrame := finalJobFramesCoveringUS(outUS, fpsNum, fpsDen)
		if endFrame <= startFrame {
			continue
		}
		if source.StockPool {
			segments = append(segments, FinalJobPlanSegment{
				SegmentID: fmt.Sprintf("segment-%04d", len(segments)), StockPoolSlot: true,
				TimelineStartFrame: startFrame, FrameCount: endFrame - startFrame,
			})
			nextFrame = endFrame
			continue
		}
		sourceInUS := source.SourceInUS + (inUS - source.StartUS)
		sourceDurationUS := outUS - inUS
		if source.SourceDurationUS > 0 {
			if source.SourceLocked {
				// Overlay splits keep the immutable stock binding and let the worker
				// loop that range for each frame-counted segment.
				sourceInUS = source.SourceInUS
				sourceDurationUS = source.SourceDurationUS
			}
			if sourceDurationUS > source.SourceDurationUS {
				sourceDurationUS = source.SourceDurationUS
			}
			if source.SourceLocked {
				availableUS := source.SourceInUS + source.SourceDurationUS - sourceInUS
				if sourceDurationUS > availableUS {
					sourceDurationUS = availableUS
				}
			} else if maxSourceInUS := source.SourceDurationUS - sourceDurationUS; sourceInUS > maxSourceInUS {
				sourceInUS = maxSourceInUS
			}
		}
		if source.SourceLocked && sourceInUS+sourceDurationUS > source.SourceInUS+source.SourceDurationUS {
			return nil, nextFrame, fmt.Errorf("final_job stock segment window %d+%d exceeds the bound stock window %d+%d", sourceInUS, sourceDurationUS, source.SourceInUS, source.SourceDurationUS)
		}
		segments = append(segments, FinalJobPlanSegment{
			SegmentID: fmt.Sprintf("segment-%04d", len(segments)), AssetID: source.AssetID, SHA256: source.SHA256,
			TimelineStartFrame: startFrame, FrameCount: endFrame - startFrame,
			SourceInUS: sourceInUS, SourceDurationUS: sourceDurationUS,
		})
		nextFrame = endFrame
	}
	return segments, nextFrame, nil
}

// finalJobMergeContiguousSegments collapses neighbouring segments that are the
// same source window run end to End: same asset, contiguous on the timeline and
// contiguous inside the source. The picture is identical by construction — a
// fragment is just a window of one asset — but every fragment costs one local
// encode, one verification and one upload, so gluing them is pure savings.
// Anything that is not provably one continuous window is left untouched.
func finalJobMergeContiguousSegments(segments []FinalJobPlanSegment) []FinalJobPlanSegment {
	if len(segments) < 2 {
		return segments
	}
	merged := make([]FinalJobPlanSegment, 0, len(segments))
	for _, current := range segments {
		if len(merged) > 0 {
			prior := &merged[len(merged)-1]
			if prior.StockPoolSlot == current.StockPoolSlot && prior.AssetID == current.AssetID && prior.SHA256 == current.SHA256 &&
				prior.TimelineStartFrame+prior.FrameCount == current.TimelineStartFrame &&
				prior.SourceInUS+prior.SourceDurationUS == current.SourceInUS {
				prior.FrameCount += current.FrameCount
				prior.SourceDurationUS += current.SourceDurationUS
				continue
			}
		}
		merged = append(merged, current)
	}
	// Renumber so the segment ids stay dense and stable in the sealed plan.
	for i := range merged {
		merged[i].SegmentID = fmt.Sprintf("segment-%04d", i)
	}
	return merged
}

// finalJobOverlayAssetDurationUS is the length the plan may declare for an
// overlay artifact. duration_ms is the renderer's own value; when a renderer
// version omits it, the certified frame grid is the next authority (rounded UP
// so the declared duration never cuts a real frame). Last resort is the
// furthest point any entry claims inside the artifact: still the artifact's
// own evidence, never the whole timeline. A manifest that carries none of the
// three yields 0, which the caller rejects.
func finalJobOverlayAssetDurationUS(asset map[string]any, entries []any, fpsNum, fpsDen int64) int64 {
	if durationUS := finalJobInt64(asset["duration_ms"]) * 1000; durationUS > 0 {
		return durationUS
	}
	if frames := finalJobInt64(asset["frame_count"]); frames > 0 && fpsNum > 0 && fpsDen > 0 {
		numerator := frames * 1_000_000 * fpsDen
		if remainder := numerator % fpsNum; remainder != 0 {
			numerator += fpsNum - remainder
		}
		if durationUS := numerator / fpsNum; durationUS > 0 {
			return durationUS
		}
	}
	var maxOutUS int64
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		outUS := finalJobInt64(entry["artifact_out_us"])
		if outUS <= 0 {
			outUS = finalJobInt64(entry["out_us"])
		}
		if outUS > maxOutUS {
			maxOutUS = outUS
		}
	}
	return maxOutUS
}
