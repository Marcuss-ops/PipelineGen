package finaljob

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/finaljob"
)

// AlignFinalJobCompiledPlanAssetIDs binds the compiled plan to the exact
// runtime identities the worker will prefetch. Objectstore assets are keyed by
// their content SHA; the plan may still name the same fragment by its
// final-fragment-* work id. The copy-only assembler certifies the segment
// against the plan asset, so both the asset and every segment must carry the
// same canonical id and the asset's full prepared-fragment metadata.
// AlignFinalJobCompiledPlanAssetIDs is the string entry point: callers that
// only hold the sealed JSON (tests, diagnostics) still get a re-encoded plan.
// The payload path uses alignFinalJobCompiledPlanAssetIDs directly, so sealing
// a job never pays for a decode/encode per step.
func AlignFinalJobCompiledPlanAssetIDs(rawPlan string, runtimeAssets []any) (string, error) {
	var plan FinalJobCompiledPlan
	if err := json.Unmarshal([]byte(rawPlan), &plan); err != nil {
		return "", fmt.Errorf("decode final_job plan for runtime alignment: %w", err)
	}
	typedAssets, err := finalJobRuntimeAssetsFromValues(runtimeAssets)
	if err != nil {
		return "", err
	}
	if err := alignFinalJobCompiledPlanAssetIDs(&plan, typedAssets); err != nil {
		return "", err
	}
	aligned, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("marshal aligned final_job plan: %w", err)
	}
	return string(aligned), nil
}

// alignFinalJobCompiledPlanAssetIDs binds the runtime identities to the plan
// IN MEMORY and mutates it: the caller serializes once, at the end of the
// hand-off chain.
func alignFinalJobCompiledPlanAssetIDs(plan *FinalJobCompiledPlan, runtimeAssets []finaljob.RuntimeAssetV1) error {
	refs := make(map[string]map[string]any, len(runtimeAssets))
	stockSHAs := make(map[string]struct{}, len(runtimeAssets))
	for index, runtimeAsset := range runtimeAssets {
		ref, err := runtimeAsset.Map()
		if err != nil {
			return fmt.Errorf("encode final_job runtime asset %d for plan alignment: %w", index, err)
		}
		sha := strings.ToLower(strings.TrimSpace(FinalJobStringValue(ref["sha256"])))
		if len(sha) == 64 {
			refs[sha] = ref
			if strings.EqualFold(FinalJobStringValue(ref["role"]), "stock") {
				stockSHAs[sha] = struct{}{}
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(plan.Assets))
	for i := range plan.Assets {
		asset := &plan.Assets[i]
		ref := refs[strings.ToLower(asset.SHA256)]
		if ref == nil {
			continue
		}
		id := strings.TrimSpace(FinalJobStringValue(ref["asset_id"]))
		if id == "" {
			return fmt.Errorf("final_job runtime asset %s has no canonical id", asset.SHA256)
		}
		if seen[id] {
			return fmt.Errorf("final_job runtime asset id %q is repeated", id)
		}
		seen[id] = true
		asset.AssetID = id
		// The lease/cache identity must be the SAME string the worker derives
		// from the runtime reference. A "sha256:" alias here made the Master
		// prefetch one key while the worker reserved another.
		asset.AssetKey = id
		asset.SHA256 = strings.ToLower(FinalJobStringValue(ref["sha256"]))
		if size := finalJobInt64(ref["size_bytes"]); size > 0 {
			asset.SizeBytes = size
		}
		if kind := FinalJobStringValue(ref["kind"]); kind != "" {
			asset.Kind = kind
		}
		if mimeType := FinalJobStringValue(ref["mime"]); mimeType != "" {
			asset.MIME = mimeType
		}
		isStock := strings.EqualFold(FinalJobStringValue(ref["role"]), "stock")
		// Stock Drive references carry duration_us (with duration_ms retained for
		// compatibility); prepared fragments also carry duration_us. Read the
		// precise source extent even when the plan repeats a shorter stock window.
		duration := finalJobInt64(ref["duration_us"])
		if duration <= 0 {
			duration = finalJobInt64(ref["duration_ms"]) * 1000
		}
		if duration > 0 {
			asset.DurationUS = duration
		}
		if width := finalJobInt(ref["width"]); width > 0 {
			asset.Width = width
		}
		if height := finalJobInt(ref["height"]); height > 0 {
			asset.Height = height
		}
		// Stock is a raw Master input, not a certified Producer fragment. Keep
		// its real metadata instead of claiming the assembly-ready profile the
		// Master itself will establish after downloading and cutting it.
		if isStock {
			asset.ProfileID = ""
		} else if profile := FinalJobStringValue(ref["profile_id"]); profile != "" {
			asset.ProfileID = profile
		}
		if frames := finalJobInt64(ref["frame_count"]); frames > 0 {
			asset.FrameCount = frames
		}
		if revision := finalJobInt64(ref["timeline_revision"]); revision > 0 {
			asset.TimelineRevision = revision
		}
		if timelineSHA := strings.ToLower(FinalJobStringValue(ref["timeline_sha256"])); timelineSHA != "" {
			asset.TimelineSHA256 = timelineSHA
		}
		asset.TimelineStartFrame = finalJobInt64(ref["timeline_start_frame"])
		asset.FirstFrameKeyframe = !isStock
		asset.ClosedGOP = !isStock
	}
	assetsByID := make(map[string]*FinalJobPlanAsset, len(plan.Assets))
	for i := range plan.Assets {
		assetsByID[plan.Assets[i].AssetID] = &plan.Assets[i]
	}
	if len(plan.VideoTracks) > 0 {
		for i := range plan.VideoTracks[0].Segments {
			segment := &plan.VideoTracks[0].Segments[i]
			if segment.StockPoolSlot {
				continue
			}
			sha := strings.ToLower(strings.TrimSpace(segment.SHA256))
			ref := refs[sha]
			if ref == nil {
				return fmt.Errorf("final_job segment %q has no runtime asset for sha %s", segment.SegmentID, sha)
			}
			segment.AssetID = strings.TrimSpace(FinalJobStringValue(ref["asset_id"]))
			segment.SHA256 = strings.ToLower(FinalJobStringValue(ref["sha256"]))
			// SourceInUS stays the source window here: this plan still names raw
			// bytes. Segment preparation rewrites it to zero only after cutting
			// the window into its own fragment.
			// The plan segment is the placement's authority for the timeline
			// span. The runtime ref only supplies the bytes (id, SHA and size).
			// The copy-only gate certifies the fragment against the V1 stream
			// profile even when the renderer labelled the opaque overlay
			// artifact VELOX_OVERLAY_VIDEO_ONLY_V1 (r90).
			if asset := assetsByID[segment.AssetID]; asset != nil && asset.Kind == "video" {
				if strings.EqualFold(FinalJobStringValue(ref["role"]), "stock") {
					// Stock remains a raw Drive source for remote processing. The
					// Master establishes the delivery profile and keyframe facts
					// after it downloads the bytes; the Producer must not certify
					// facts it never inspected.
					asset.ProfileID = ""
					asset.FirstFrameKeyframe = false
					asset.ClosedGOP = false
					continue
				}
				asset.ProfileID = kernelmedia.AssemblyMediaContractID
				asset.TimelineRevision = plan.TimelineRevision
				asset.TimelineSHA256 = strings.ToLower(plan.TimelineSHA256)
				asset.TimelineStartFrame = segment.TimelineStartFrame
				if asset.FrameCount <= 0 && asset.DurationUS > 0 {
					fpsNum, fpsDen := int64(plan.Output.FPSNum), int64(plan.Output.FPSDen)
					frames := FinalJobFrameAtUS(asset.DurationUS, fpsNum, fpsDen)
					if frames > 0 && FinalJobFrameStartUS(frames, fpsNum, fpsDen) > asset.DurationUS {
						frames--
					}
					asset.FrameCount = frames
				}
				// Do NOT copy DurationUS/FrameCount from the placement. One raw
				// source can back several windows, so the LAST segment would shrink
				// the source to its own window and every earlier window would look
				// out of bounds or be cut from the wrong in-point. The source extent
				// is the asset's own duration/frame count, not the placement's span.
				asset.FirstFrameKeyframe = true
				asset.ClosedGOP = true
			}
		}
	}
	for _, track := range plan.VideoTracks {
		for _, segment := range track.Segments {
			if _, stock := stockSHAs[strings.ToLower(segment.SHA256)]; !stock {
				continue
			}
			asset, ok := assetsByID[segment.AssetID]
			if !ok {
				return fmt.Errorf("final_job stock segment %q has no aligned plan asset %q", segment.SegmentID, segment.AssetID)
			}
			if asset.DurationUS <= 0 || segment.SourceInUS < 0 || segment.SourceDurationUS <= 0 || segment.SourceInUS+segment.SourceDurationUS > asset.DurationUS {
				return fmt.Errorf("final_job stock segment %q source window %d+%d exceeds saved file duration %d", segment.SegmentID, segment.SourceInUS, segment.SourceDurationUS, asset.DurationUS)
			}
		}
	}
	if audioRef := refs[strings.ToLower(plan.FinalAudio.SHA256)]; audioRef != nil {
		if id := strings.TrimSpace(FinalJobStringValue(audioRef["asset_id"])); id != "" {
			plan.FinalAudio.AssetID = id
		}
		plan.FinalAudio.SHA256 = strings.ToLower(FinalJobStringValue(audioRef["sha256"]))
		if size := finalJobInt64(audioRef["size_bytes"]); size > 0 {
			plan.FinalAudio.SizeBytes = size
		}
	}
	if err := clampFinalJobRuntimeSourceWindows(plan); err != nil {
		return fmt.Errorf("align final_job runtime source windows: %w", err)
	}
	if err := ValidateFinalJobPlanBindingRules(*plan); err != nil {
		return fmt.Errorf("validate final_job runtime plan bindings: %w", err)
	}
	sort.Slice(plan.Assets, func(i, j int) bool { return plan.Assets[i].AssetID < plan.Assets[j].AssetID })
	return nil
}

// --- the asset registry both plan builders share ---

// finalJobPlanAssets is the ONE registry a CompiledRenderPlanV2 is built
// through. The payload builder (scenes + overlay manifests) and the producer
// (prepared fragments replacing those sources) both add their assets here, so
// the identity rule, the duplicate rule, the MIME mapping, the sort order and
// the output block exist once instead of in two builders that must agree.
//
// Adding a raw (map-shaped) asset and adding an already-typed asset are the
// same operation on the same identity; only the parsing differs.
type finalJobPlanAssets struct {
	items []FinalJobPlanAsset
}

// finalJobPlanDelivery is the delivery identity the plan describes. It is the
// kernel assembly contract, not a copy: geometry, cadence, GOP, timebase and
// audio layout move together when the contract moves.
func finalJobPlanDelivery() kernelmedia.AssemblyMediaContract {
	return kernelmedia.DefaultAssemblyMediaContract()
}

// FinalJobPlanOutputBlock is the plan's output block, derived from that contract.
func FinalJobPlanOutputBlock() finalJobPlanOutput {
	delivery := finalJobPlanDelivery()
	return finalJobPlanOutput{
		Container: delivery.Container, VideoCodec: delivery.VideoCodec,
		Width: delivery.Width, Height: delivery.Height,
		FPSNum: delivery.FPS.Num, FPSDen: delivery.FPS.Den, PixelFormat: delivery.PixelFormat,
		ProfileID: delivery.ID, CodecProfile: delivery.VideoProfile, CodecLevel: delivery.VideoLevel,
		GOPSize: delivery.KeyframeInterval, BFrames: delivery.BFrames, ClosedGOP: delivery.ClosedGOP,
		TimeBaseNum: delivery.VideoTimeBase.Num, TimeBaseDen: delivery.VideoTimeBase.Den,
	}
}

func finalJobPlanAssetMIME(kind string) string {
	switch kind {
	case "final_audio":
		return "audio/mp4"
	default:
		return "video/mp4"
	}
}

// add types one asset into the plan. A repeated asset id must describe the same
// bytes, size and kind. References can carry a scene-local duration, so retain
// the longest declared source span for the shared byte identity; retaining the
// first reference made later valid source cuts fail source range validation.
func (a *finalJobPlanAssets) add(asset FinalJobPlanAsset) error {
	if asset.AssetID == "" || len(asset.SHA256) != 64 || asset.SizeBytes <= 0 || asset.DurationUS <= 0 {
		return fmt.Errorf("asset requires id, 64-character SHA-256, positive size, and duration")
	}
	for i, prior := range a.items {
		if prior.AssetID != asset.AssetID {
			continue
		}
		if prior.SHA256 != asset.SHA256 || prior.SizeBytes != asset.SizeBytes || prior.Kind != asset.Kind {
			return fmt.Errorf("asset %q was referenced with conflicting identity", asset.AssetID)
		}
		if asset.DurationUS > prior.DurationUS {
			a.items[i].DurationUS = asset.DurationUS
		}
		return nil
	}
	a.items = append(a.items, asset)
	return nil
}

// addRaw types a map-shaped source reference (stock scene, overlay artifact or
// final audio) into the same registry.
func (a *finalJobPlanAssets) addRaw(raw map[string]any, kind string, durationUS int64, width, height int, frameCount int64) error {
	AssetID := FinalJobStringValue(raw["master_asset_id"])
	if AssetID == "" {
		AssetID = FinalJobStringValue(raw["asset_id"])
	}
	assetKey := FinalJobStringValue(raw["master_reference"])
	if assetKey == "" {
		assetKey = FinalJobStringValue(raw["reference"])
	}
	mimeType := FinalJobStringValue(raw["mime_type"])
	if mimeType == "" {
		mimeType = finalJobPlanAssetMIME(kind)
	}
	return a.add(FinalJobPlanAsset{
		AssetID: AssetID, AssetKey: assetKey,
		SHA256: strings.ToLower(FinalJobStringValue(raw["sha256"])),
		// SizeBytes is checked by add; the raw map spells it in bytes already.
		SizeBytes: finalJobInt64(raw["size_bytes"]),
		Kind:      kind, MIME: mimeType,
		DurationUS: durationUS, Width: width, Height: height, FrameCount: frameCount,
	})
}

// set replaces the registry contents (the producer re-binds overlay/clip sources
// to prepared fragments and retains stock sources as Drive references, so its
// result is the complete list, not an increment).
func (a *finalJobPlanAssets) set(assets []FinalJobPlanAsset) error {
	a.items = nil
	for _, asset := range assets {
		if err := a.add(asset); err != nil {
			return err
		}
	}
	return nil
}

// sorted returns the plan's asset list in the canonical order (asset id), which
// is what the plan digest covers.
func (a *finalJobPlanAssets) sorted() []FinalJobPlanAsset {
	sort.Slice(a.items, func(i, j int) bool { return a.items[i].AssetID < a.items[j].AssetID })
	return a.items
}

func (a *finalJobPlanAssets) extendSourceExtent(AssetID string, sourceEndUS int64) {
	if sourceEndUS <= 0 {
		return
	}
	for i := range a.items {
		if a.items[i].AssetID == AssetID && sourceEndUS > a.items[i].DurationUS {
			a.items[i].DurationUS = sourceEndUS
		}
	}
}

// retainReferenced removes registry entries that the sealed plan cannot
// consume. Runtime asset manifests drive remote prefetch, so keeping an unused
// source here would still pay its download/preparation cost.
func (a *finalJobPlanAssets) retainReferenced(segments []FinalJobPlanSegment, finalAudioID string) {
	if a == nil {
		return
	}
	used := make(map[string]struct{}, len(segments)+1)
	for _, segment := range segments {
		if id := strings.TrimSpace(segment.AssetID); id != "" {
			used[id] = struct{}{}
		}
	}
	if id := strings.TrimSpace(finalAudioID); id != "" {
		used[id] = struct{}{}
	}
	kept := a.items[:0]
	for _, asset := range a.items {
		if _, ok := used[asset.AssetID]; ok {
			kept = append(kept, asset)
		}
	}
	a.items = kept
}

// RetainFinalJobRuntimeAssets keeps only transport bindings named by the
// compiled plan. The Master includes runtime_assets in its queue dependency
// set, so leaving an unused source there would still cause a prefetch even if
// the plan no longer contains a segment for it.
// RetainFinalJobRuntimeAssets is the string entry point; the payload path calls
// retainFinalJobRuntimeAssets with the in-memory plan so no step re-encodes it.
func RetainFinalJobRuntimeAssets(planJSON string, runtimeAssets []any) ([]any, error) {
	var plan FinalJobCompiledPlan
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		return nil, fmt.Errorf("decode final_job plan while pruning runtime bindings: %w", err)
	}
	typedAssets, err := finalJobRuntimeAssetsFromValues(runtimeAssets)
	if err != nil {
		return nil, err
	}
	retained, err := retainFinalJobRuntimeAssets(&plan, typedAssets)
	if err != nil {
		return nil, err
	}
	return finalJobRuntimeAssetsToValues(retained)
}

// retainFinalJobRuntimeAssets prunes the runtime refs to the assets the sealed
// plan actually names. It only reads the plan, so it takes it by pointer.
func retainFinalJobRuntimeAssets(plan *FinalJobCompiledPlan, runtimeAssets []finaljob.RuntimeAssetV1) ([]finaljob.RuntimeAssetV1, error) {
	used := make(map[string]struct{}, len(plan.Assets)*5+1)
	add := func(values ...string) {
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				used[value] = struct{}{}
			}
		}
	}
	for _, asset := range plan.Assets {
		add(asset.AssetID, asset.AssetKey, asset.SHA256)
	}
	add(plan.FinalAudio.AssetID, plan.FinalAudio.SHA256)

	filtered := make([]finaljob.RuntimeAssetV1, 0, len(runtimeAssets))
	for index, asset := range runtimeAssets {
		fields, err := asset.Map()
		if err != nil {
			return nil, fmt.Errorf("encode final_job runtime asset %d while pruning bindings: %w", index, err)
		}
		keep := false
		for _, key := range []string{"asset_id", "asset_key", "sha256", "asset_sha256", "drive_file_id", "master_asset_id", "master_reference", "reference"} {
			value := strings.TrimSpace(FinalJobStringValue(fields[key]))
			if _, ok := used[value]; value != "" && ok {
				keep = true
				break
			}
		}
		if keep {
			filtered = append(filtered, asset)
		}
	}
	return filtered, nil
}
