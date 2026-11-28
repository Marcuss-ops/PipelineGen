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
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/finaljob"
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

func FinalJobCompiledPlanPayload(scenes []FinalJobRemoteScene, overlayRows []any, overlayManifest map[string]any, audio contract.FinalAudioReference, audioRef map[string]any, timelineDurationUS int64) (string, string, error) {
	return FinalJobCompiledPlanPayloadWithStockPool(scenes, overlayRows, overlayManifest, audio, audioRef, timelineDurationUS, "", nil)
}

// FinalJobCompiledPlanPayloadWithStockPool builds and seals the V2 plan, then
// serializes it once. It is the string API; the hand-off chain uses
// buildFinalJobCompiledPlan + finalJobSealCompiledPlan so the plan is sealed in
// memory and marshaled exactly once, after every step has rewritten it.
func FinalJobCompiledPlanPayloadWithStockPool(scenes []FinalJobRemoteScene, overlayRows []any, overlayManifest map[string]any, audio contract.FinalAudioReference, audioRef map[string]any, timelineDurationUS int64, stockPoolFolderID string, stockBoundWindowsMS map[string]int64) (string, string, error) {
	plan, stockAssetIDs, err := buildFinalJobCompiledPlan(scenes, overlayRows, overlayManifest, audio, audioRef, timelineDurationUS, stockPoolFolderID, stockBoundWindowsMS)
	if err != nil {
		return "", "", err
	}
	if err := finalJobSealCompiledPlan(plan, scenes, stockAssetIDs); err != nil {
		return "", "", err
	}
	return marshalFinalJobCompiledPlan(plan)
}

// buildFinalJobCompiledPlan builds the V2 plan. The stock binding windows are
// keyed by scene id and stay Producer-local: the remote Master only receives
// the compiled plan and runtime asset refs.
func buildFinalJobCompiledPlan(scenes []FinalJobRemoteScene, overlayRows []any, overlayManifest map[string]any, audio contract.FinalAudioReference, audioRef map[string]any, timelineDurationUS int64, stockPoolFolderID string, stockBoundWindowsMS map[string]int64) (*FinalJobCompiledPlan, map[string]struct{}, error) {
	typedRows, typedManifest, err := FinalJobOverlayRuntimeDTO(overlayRows, overlayManifest)
	if err != nil {
		return nil, nil, err
	}
	return buildFinalJobCompiledPlanTyped(scenes, typedRows, typedManifest, audio, audioRef, timelineDurationUS, stockPoolFolderID, stockBoundWindowsMS)
}

func buildFinalJobCompiledPlanTyped(scenes []FinalJobRemoteScene, overlayRows []kerneljob.OverlayRowV1, overlayManifest *kerneljob.OverlayManifestV1, audio contract.FinalAudioReference, audioRef map[string]any, timelineDurationUS int64, stockPoolFolderID string, stockBoundWindowsMS map[string]int64) (*FinalJobCompiledPlan, map[string]struct{}, error) {
	if len(scenes) == 0 {
		return nil, nil, fmt.Errorf("final_job V2 render plan requires at least one visual scene")
	}
	if audio.DurationUS <= 0 && audio.DurationMS > 0 {
		audio.DurationUS = audio.DurationMS * 1000
	}
	if audio.DurationUS <= 0 || audio.DurationMS <= 0 || !audio.CopyEligible || !audio.FinalMix {
		return nil, nil, fmt.Errorf("final_job V2 render plan requires certified final audio")
	}
	if timelineDurationUS <= 0 {
		return nil, nil, fmt.Errorf("final_job V2 render plan requires a positive canonical duration")
	}
	// The plan's output duration must match the certified media duration. The
	// canonical scene/audio timeline can differ by a few milliseconds because
	// the final AAC container reports packet-rounded duration; keep that drift
	// within the existing 40 ms alignment allowance, then use the actual final
	// audio duration as the worker timeline SSOT. Video coverage is completed
	// on the output frame grid below without changing this certified duration.
	if delta := audio.DurationUS - timelineDurationUS; delta < -capabilityaudio.FinalAudioDurationToleranceUS || delta > capabilityaudio.FinalAudioDurationToleranceUS {
		return nil, nil, fmt.Errorf("final_job V2 final audio duration %dus does not match canonical timeline %dus", audio.DurationUS, timelineDurationUS)
	}
	timelineDurationUS = audio.DurationUS
	delivery := finalJobPlanDelivery()
	fpsNum, fpsDen := int64(delivery.FPS.Num), int64(delivery.FPS.Den)
	assets := &finalJobPlanAssets{}
	base, err := finalJobPlanBaseSegments(scenes, assets, stockBoundWindowsMS)
	if err != nil {
		return nil, nil, err
	}
	if err := assets.addRaw(audioRef, "final_audio", audio.DurationUS, 0, 0, 0); err != nil {
		return nil, nil, fmt.Errorf("final_job V2 final audio: %w", err)
	}
	baseEnd := base[len(base)-1].EndUS
	delta := timelineDurationUS - baseEnd
	if delta < -capabilityaudio.FinalAudioDurationToleranceUS || delta > capabilityaudio.FinalAudioDurationToleranceUS {
		return nil, nil, fmt.Errorf("final_job V2 scene duration %dus does not match certified final audio %dus", baseEnd, timelineDurationUS)
	}
	base[len(base)-1].EndUS = timelineDurationUS
	if base[len(base)-1].EndUS <= base[len(base)-1].StartUS {
		return nil, nil, fmt.Errorf("final_job V2 final scene has no positive duration after audio alignment")
	}

	windows, manifests, err := finalJobOverlayWindows(overlayManifest, overlayRows, assets, timelineDurationUS, fpsNum, fpsDen, delivery.Width, delivery.Height)
	if err != nil {
		return nil, nil, err
	}
	windows, err = finalJobMergeOverlayWindows(windows)
	if err != nil {
		return nil, nil, err
	}
	segments, err := FinalJobComposePlanSegments(base, windows, timelineDurationUS, fpsNum, fpsDen)
	if err != nil {
		return nil, nil, err
	}
	if len(segments) == 0 {
		return nil, nil, fmt.Errorf("final_job V2 render plan has no video segments")
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
		return nil, nil, err
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
	return &plan, stockAssetIDs, nil
}

func finalJobTimelineIdentity(durationUS int64, audioPlanSHA string, manifests []kerneljob.OverlayManifestV1) (string, error) {
	material := map[string]any{
		"duration_us":             durationUS,
		"audio_plan_sha256":       strings.ToLower(strings.TrimSpace(audioPlanSHA)),
		"overlay_timeline_sha256": "",
	}
	if len(manifests) == 1 {
		if asset := manifests[0].Asset; asset != nil {
			material["overlay_timeline_sha256"] = strings.ToLower(asset.SHA256)
		}
	} else if len(manifests) > 1 {
		hashes := make([]string, 0, len(manifests))
		for _, manifest := range manifests {
			if manifest.Asset == nil {
				return "", fmt.Errorf("final_job overlay manifest has no typed asset")
			}
			hashes = append(hashes, strings.ToLower(manifest.Asset.SHA256))
		}
		material["overlay_timeline_sha256"] = digest.SHA256String(strings.Join(hashes, "\x00"))
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal final_job V2 timeline identity: %w", err)
	}
	return digest.SHA256String(string(encoded)), nil
}

func finalJobRetainPlanAssets(scenes []FinalJobRemoteScene, segments []FinalJobPlanSegment, assets *finalJobPlanAssets, audioAssetID string) map[string]struct{} {
	stockAssetIDs := finalJobSourceLockedAssetIDs(scenes)
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

// finalJobSealCompiledPlan aligns and validates the plan IN MEMORY. It is the
// last step that may reorder or reject the plan; the caller serializes once
// afterwards, so sealing a job never costs a JSON round-trip.
func finalJobSealCompiledPlan(plan *FinalJobCompiledPlan, scenes []FinalJobRemoteScene, stockAssetIDs map[string]struct{}) error {
	if err := AlignFinalJobSegmentSourceWindows(plan, stockAssetIDs); err != nil {
		return fmt.Errorf("align final_job source windows to the output frame grid: %w", err)
	}
	// The stock slot carries stock, clip and overlay scenes alike, so the
	// frame-coverage pass needs the immutable subset: only a "stock" scene's
	// bytes are packet-copied by the Master and cannot be padded locally.
	fpsNum, fpsDen := int64(plan.Output.FPSNum), int64(plan.Output.FPSDen)
	var err error
	plan.VideoTracks[0].Segments, err = finalJobCompleteVideoFrameCoverageWithStockAssets(plan.VideoTracks[0].Segments, plan.Assets, plan.DurationUS, fpsNum, fpsDen, finalJobSourceLockedAssetIDs(scenes))
	if err != nil {
		return err
	}
	if err := AlignFinalJobSegmentSourceWindows(plan, stockAssetIDs); err != nil {
		return fmt.Errorf("align final_job source windows after frame coverage: %w", err)
	}
	if err := FinalJobValidateVideoCoverage(*plan); err != nil {
		return fmt.Errorf("final_job V2 compiled-plan video coverage: %w", err)
	}
	if err := ValidateFinalJobPlanBindingRules(*plan); err != nil {
		return fmt.Errorf("final_job V2 compiled-plan asset bindings: %w", err)
	}
	return nil
}

// marshalFinalJobCompiledPlan is the ONE serialization of a finished hand-off:
// the canonical bytes and the digest the Master verifies are produced together.
func marshalFinalJobCompiledPlan(plan *FinalJobCompiledPlan) (string, string, error) {
	canonical, err := json.Marshal(plan)
	if err != nil {
		return "", "", fmt.Errorf("marshal final_job canonical V2 render plan: %w", err)
	}
	return string(canonical), digest.SHA256String(string(canonical)), nil
}

func finalJobOverlayWindows(overlayManifest *kerneljob.OverlayManifestV1, overlayRows []kerneljob.OverlayRowV1, assets *finalJobPlanAssets, timelineDurationUS, fpsNum, fpsDen int64, width, height int) ([]FinalJobOverlayWindow, []kerneljob.OverlayManifestV1, error) {
	manifests := make([]kerneljob.OverlayManifestV1, 0, 1)
	if overlayManifest != nil {
		if overlayManifest.SchemaVersion == "pipelinegen.overlay-manifest-set.v1" {
			if len(overlayManifest.Manifests) == 0 {
				return nil, nil, fmt.Errorf("final_job overlay manifest set has no manifests")
			}
			manifests = append(manifests, overlayManifest.Manifests...)
		} else {
			manifests = append(manifests, *overlayManifest)
		}
	}
	windows := make([]FinalJobOverlayWindow, 0)
	for _, manifest := range manifests {
		asset := manifest.Asset
		if asset == nil {
			return nil, nil, fmt.Errorf("final_job overlay manifest has no typed asset")
		}
		assetID := asset.AssetID
		sha := strings.ToLower(asset.SHA256)
		entries := manifest.Entries
		durationUS := finalJobOverlayAssetDurationUS(asset, entries, fpsNum, fpsDen)
		if durationUS <= 0 {
			return nil, nil, fmt.Errorf("final_job overlay manifest asset %q has no certifiable duration", assetID)
		}
		if err := assets.add(FinalJobPlanAsset{
			AssetID: assetID, SHA256: sha, SizeBytes: asset.SizeBytes,
			Kind: "video", MIME: finalJobPlanAssetMIME("video"), DurationUS: durationUS,
			Width: width, Height: height, FrameCount: asset.FrameCount,
		}); err != nil {
			return nil, nil, fmt.Errorf("final_job overlay timeline asset: %w", err)
		}
		for i, entry := range entries {
			startUS, endUS := entry.InUS, entry.OutUS
			artifactInUS, artifactOutUS := startUS, endUS
			if entry.ArtifactInUS != nil {
				artifactInUS = *entry.ArtifactInUS
			}
			if entry.ArtifactOutUS != nil {
				artifactOutUS = *entry.ArtifactOutUS
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

func finalJobDedicatedOverlayWindows(overlayRows []kerneljob.OverlayRowV1, assets *finalJobPlanAssets, timelineDurationUS, fpsNum, fpsDen int64, width, height int) ([]FinalJobOverlayWindow, error) {
	windows := make([]FinalJobOverlayWindow, 0, len(overlayRows))
	for i, row := range overlayRows {
		assetID := row.AssetID
		sha := strings.ToLower(row.SHA256)
		startFrame, endFrame := row.StartFrame, row.EndFrame
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
		durationUS := row.DurationMS * 1000
		if durationUS <= 0 {
			durationUS = endUS - startUS
		}
		if endUS-startUS > durationUS {
			endUS = startUS + durationUS
		}
		if endUS <= startUS {
			continue
		}
		if err := assets.add(FinalJobPlanAsset{
			AssetID: assetID, SHA256: sha, SizeBytes: row.SizeBytes,
			Kind: "video", MIME: finalJobPlanAssetMIME("video"), DurationUS: durationUS,
			Width: width, Height: height, FrameCount: row.FrameCount,
		}); err != nil {
			return nil, fmt.Errorf("final_job dedicated overlay row %d: %w", i, err)
		}
		windows = append(windows, FinalJobOverlayWindow{StartUS: startUS, EndUS: endUS, ArtifactInUS: 0, ArtifactDurationUS: durationUS, AssetID: assetID, SHA256: sha})
	}
	return windows, nil
}

// finalJobPlanBaseSegments describes one source segment per scene and types
// every stock asset INTO the shared registry the caller already owns.
func finalJobPlanBaseSegments(scenes []FinalJobRemoteScene, assets *finalJobPlanAssets, stockBoundWindowsMS map[string]int64) ([]FinalJobBaseSegment, error) {
	base := make([]FinalJobBaseSegment, 0, len(scenes))
	var cursorUS int64
	for i, scene := range scenes {
		durationSeconds := scene.DurationSeconds
		if math.IsNaN(durationSeconds) || math.IsInf(durationSeconds, 0) || durationSeconds <= 0 {
			return nil, fmt.Errorf("final_job scene %d has an invalid duration", i)
		}
		durationUS := int64(math.Round(durationSeconds * 1_000_000))
		if scene.StockPoolSlot {
			base = append(base, FinalJobBaseSegment{StartUS: cursorUS, EndUS: cursorUS + durationUS, StockPool: true})
			cursorUS += durationUS
			continue
		}
		asset := scene.Stock
		if asset == nil {
			return nil, fmt.Errorf("final_job scene %d has no video asset", i)
		}
		// Keep the actual saved-file duration in the asset registry. Stock refs
		// may carry a microsecond value because rounding a saved Drive duration
		// down to milliseconds can make a valid final millisecond disappear.
		assetDurationUS := finalJobInt64(asset["duration_us"])
		if assetDurationUS <= 0 {
			assetDurationUS = finalJobInt64(asset["duration_ms"]) * 1000
		}
		if assetDurationUS <= 0 {
			return nil, fmt.Errorf("final_job scene %d stock asset has no positive saved duration", i)
		}
		if err := assets.addRaw(asset, "video", assetDurationUS, finalJobInt(asset["width"]), finalJobInt(asset["height"]), finalJobInt64(asset["frame_count"])); err != nil {
			return nil, fmt.Errorf("final_job scene %d: %w", i, err)
		}
		SourceInUS := finalJobInt64(asset["start_ms"]) * 1000
		SourceDurationUS := assetDurationUS
		SourceLocked := strings.EqualFold(scene.Kind, "stock")
		if SourceLocked {
			// The stockpipeline owns and selects this source window. The Producer
			// may describe its frame boundaries, but never opens or alters bytes.
			if boundMS := stockBoundWindowsMS[scene.SceneID]; boundMS > 0 {
				SourceDurationUS = boundMS * 1000
			} else {
				SourceDurationUS = assetDurationUS - SourceInUS
			}
			if overrun := SourceInUS + SourceDurationUS - assetDurationUS; overrun > 0 && overrun <= 1_000 {
				// Some saved-file durations were serialized to whole milliseconds.
				// Preserve the exact microsecond extent and trim only that rounding
				// residue; a larger overrun is a real invalid source window.
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
	if fpsNum <= 0 || fpsDen <= 0 {
		return nil, nextFrame, fmt.Errorf("final_job base segments require a positive output frame rate")
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
		if source.SourceLocked {
			// video.assemble.copy.v1 accepts only frame-exact source windows.
			// Emit one packet-copy placement per pass over the selected window;
			// when speech outlasts the stock window, repeat that same immutable
			// window declaratively in the plan for the Master to materialize.
			windowFrames := source.SourceDurationUS * fpsNum / (1_000_000 * fpsDen)
			// FrameDurationUSAtFPS floors to integer microseconds. Its exact
			// frame span can therefore be one microsecond tick shorter than the
			// source duration; recover that frame when the encoded source window
			// still contains its complete floored span.
			if kerneljob.FrameDurationUSAtFPS(windowFrames+1, fpsNum, fpsDen) <= source.SourceDurationUS {
				windowFrames++
			}
			if windowFrames <= 0 {
				return nil, nextFrame, fmt.Errorf("final_job stock segment %q has no complete source frame in its bound window", source.AssetID)
			}
			remaining := endFrame - startFrame
			for remaining > 0 {
				chunkFrames := min(remaining, windowFrames)
				chunkDurationUS := kerneljob.FrameDurationUSAtFPS(chunkFrames, fpsNum, fpsDen)
				if source.SourceInUS < 0 || source.SourceInUS+chunkDurationUS > source.SourceInUS+source.SourceDurationUS {
					return nil, nextFrame, fmt.Errorf("final_job stock segment %q frame-exact window exceeds the selected stock range", source.AssetID)
				}
				segments = append(segments, FinalJobPlanSegment{
					SegmentID: fmt.Sprintf("segment-%04d", len(segments)), AssetID: source.AssetID, SHA256: source.SHA256,
					TimelineStartFrame: startFrame, FrameCount: chunkFrames,
					SourceInUS: source.SourceInUS, SourceDurationUS: chunkDurationUS,
				})
				startFrame += chunkFrames
				remaining -= chunkFrames
			}
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
func finalJobOverlayAssetDurationUS(asset *kerneljob.OverlayAssetV1, entries []kerneljob.OverlayRowV1, fpsNum, fpsDen int64) int64 {
	if asset == nil {
		return 0
	}
	if durationUS := asset.DurationMS * 1000; durationUS > 0 {
		return durationUS
	}
	if frames := asset.FrameCount; frames > 0 && fpsNum > 0 && fpsDen > 0 {
		numerator := frames * 1_000_000 * fpsDen
		if remainder := numerator % fpsNum; remainder != 0 {
			numerator += fpsNum - remainder
		}
		if durationUS := numerator / fpsNum; durationUS > 0 {
			return durationUS
		}
	}
	var maxOutUS int64
	for _, entry := range entries {
		var outUS int64
		if entry.ArtifactOutUS != nil {
			outUS = *entry.ArtifactOutUS
		}
		if outUS <= 0 {
			outUS = entry.OutUS
		}
		if outUS > maxOutUS {
			maxOutUS = outUS
		}
	}
	return maxOutUS
}
