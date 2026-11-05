package scriptgeneration

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// alignFinalJobCompiledPlanAssetIDs binds the compiled plan to the exact
// runtime identities the worker will prefetch. Objectstore assets are keyed by
// their content SHA; the plan may still name the same fragment by its
// final-fragment-* work id. The copy-only assembler certifies the segment
// against the plan asset, so both the asset and every segment must carry the
// same canonical id and the asset's full prepared-fragment metadata.
func alignFinalJobCompiledPlanAssetIDs(rawPlan string, runtimeAssets []any) (string, error) {
	var plan finalJobCompiledPlan
	if err := json.Unmarshal([]byte(rawPlan), &plan); err != nil {
		return "", fmt.Errorf("decode final_job plan for runtime alignment: %w", err)
	}
	refs := make(map[string]map[string]any, len(runtimeAssets))
	for _, raw := range runtimeAssets {
		ref, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		sha := strings.ToLower(strings.TrimSpace(finalJobStringValue(ref["sha256"])))
		if len(sha) == 64 {
			refs[sha] = ref
		}
	}
	if len(refs) == 0 {
		return rawPlan, nil
	}
	seen := make(map[string]bool, len(plan.Assets))
	for i := range plan.Assets {
		asset := &plan.Assets[i]
		ref := refs[strings.ToLower(asset.SHA256)]
		if ref == nil {
			continue
		}
		id := strings.TrimSpace(finalJobStringValue(ref["asset_id"]))
		if id == "" {
			return "", fmt.Errorf("final_job runtime asset %s has no canonical id", asset.SHA256)
		}
		if seen[id] {
			return "", fmt.Errorf("final_job runtime asset id %q is repeated", id)
		}
		seen[id] = true
		asset.AssetID = id
		// The lease/cache identity must be the SAME string the worker derives
		// from the runtime reference. A "sha256:" alias here made the Master
		// prefetch one key while the worker reserved another.
		asset.AssetKey = id
		asset.SHA256 = strings.ToLower(finalJobStringValue(ref["sha256"]))
		if size := finalJobInt64(ref["size_bytes"]); size > 0 {
			asset.SizeBytes = size
		}
		if kind := finalJobStringValue(ref["kind"]); kind != "" {
			asset.Kind = kind
		}
		if mimeType := finalJobStringValue(ref["mime"]); mimeType != "" {
			asset.MIME = mimeType
		}
		if duration := finalJobInt64(ref["duration_us"]); duration > 0 {
			asset.DurationUS = duration
		}
		if width := finalJobInt(ref["width"]); width > 0 {
			asset.Width = width
		}
		if height := finalJobInt(ref["height"]); height > 0 {
			asset.Height = height
		}
		if profile := finalJobStringValue(ref["profile_id"]); profile != "" {
			asset.ProfileID = profile
		}
		if frames := finalJobInt64(ref["frame_count"]); frames > 0 {
			asset.FrameCount = frames
		}
		if revision := finalJobInt64(ref["timeline_revision"]); revision > 0 {
			asset.TimelineRevision = revision
		}
		if timelineSHA := strings.ToLower(finalJobStringValue(ref["timeline_sha256"])); timelineSHA != "" {
			asset.TimelineSHA256 = timelineSHA
		}
		asset.TimelineStartFrame = finalJobInt64(ref["timeline_start_frame"])
		asset.FirstFrameKeyframe = true
		asset.ClosedGOP = true
	}
	assetsByID := make(map[string]*finalJobPlanAsset, len(plan.Assets))
	for i := range plan.Assets {
		assetsByID[plan.Assets[i].AssetID] = &plan.Assets[i]
	}
	if len(plan.VideoTracks) > 0 {
		for i := range plan.VideoTracks[0].Segments {
			segment := &plan.VideoTracks[0].Segments[i]
			sha := strings.ToLower(strings.TrimSpace(segment.SHA256))
			ref := refs[sha]
			if ref == nil {
				return "", fmt.Errorf("final_job segment %q has no runtime asset for sha %s", segment.SegmentID, sha)
			}
			segment.AssetID = strings.TrimSpace(finalJobStringValue(ref["asset_id"]))
			segment.SHA256 = strings.ToLower(finalJobStringValue(ref["sha256"]))
			// SourceInUS stays the source window here: this plan still names raw
			// bytes. Segment preparation rewrites it to zero only after cutting
			// the window into its own fragment.
			// The plan segment is the placement's authority for the timeline
			// span. The runtime ref only supplies the bytes (id, SHA and size).
			// The copy-only gate certifies the fragment against the V1 stream
			// profile even when the renderer labelled the opaque overlay
			// artifact VELOX_OVERLAY_VIDEO_ONLY_V1 (r90).
			if asset := assetsByID[segment.AssetID]; asset != nil && asset.Kind == "video" {
				asset.ProfileID = kernelmedia.AssemblyMediaContractID
				asset.TimelineRevision = plan.TimelineRevision
				asset.TimelineSHA256 = strings.ToLower(plan.TimelineSHA256)
				asset.TimelineStartFrame = segment.TimelineStartFrame
				asset.FrameCount = segment.FrameCount
				asset.DurationUS = segment.SourceDurationUS
				asset.FirstFrameKeyframe = true
				asset.ClosedGOP = true
			}
		}
	}
	if audioRef := refs[strings.ToLower(plan.FinalAudio.SHA256)]; audioRef != nil {
		if id := strings.TrimSpace(finalJobStringValue(audioRef["asset_id"])); id != "" {
			plan.FinalAudio.AssetID = id
		}
		plan.FinalAudio.SHA256 = strings.ToLower(finalJobStringValue(audioRef["sha256"]))
		if size := finalJobInt64(audioRef["size_bytes"]); size > 0 {
			plan.FinalAudio.SizeBytes = size
		}
	}
	sort.Slice(plan.Assets, func(i, j int) bool { return plan.Assets[i].AssetID < plan.Assets[j].AssetID })
	aligned, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("marshal aligned final_job plan: %w", err)
	}
	return string(aligned), nil
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
	items []finalJobPlanAsset
}

// finalJobPlanDelivery is the delivery identity the plan describes. It is the
// kernel assembly contract, not a copy: geometry, cadence, GOP, timebase and
// audio layout move together when the contract moves.
func finalJobPlanDelivery() kernelmedia.AssemblyMediaContract {
	return kernelmedia.DefaultAssemblyMediaContract()
}

// finalJobPlanOutputBlock is the plan's output block, derived from that contract.
func finalJobPlanOutputBlock() finalJobPlanOutput {
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
// bytes, size and kind: the plan is an immutable document, so a second
// reference that disagrees is a producer bug, not an update.
func (a *finalJobPlanAssets) add(asset finalJobPlanAsset) error {
	if asset.AssetID == "" || len(asset.SHA256) != 64 || asset.SizeBytes <= 0 || asset.DurationUS <= 0 {
		return fmt.Errorf("asset requires id, 64-character SHA-256, positive size, and duration")
	}
	for _, prior := range a.items {
		if prior.AssetID != asset.AssetID {
			continue
		}
		if prior.SHA256 != asset.SHA256 || prior.SizeBytes != asset.SizeBytes || prior.Kind != asset.Kind {
			return fmt.Errorf("asset %q was referenced with conflicting identity", asset.AssetID)
		}
		return nil
	}
	a.items = append(a.items, asset)
	return nil
}

// addRaw types a map-shaped source reference (stock scene, overlay artifact or
// final audio) into the same registry.
func (a *finalJobPlanAssets) addRaw(raw map[string]any, kind string, durationUS int64, width, height int, frameCount int64) error {
	assetID := finalJobStringValue(raw["master_asset_id"])
	if assetID == "" {
		assetID = finalJobStringValue(raw["asset_id"])
	}
	assetKey := finalJobStringValue(raw["master_reference"])
	if assetKey == "" {
		assetKey = finalJobStringValue(raw["reference"])
	}
	mimeType := finalJobStringValue(raw["mime_type"])
	if mimeType == "" {
		mimeType = finalJobPlanAssetMIME(kind)
	}
	return a.add(finalJobPlanAsset{
		AssetID: assetID, AssetKey: assetKey,
		SHA256: strings.ToLower(finalJobStringValue(raw["sha256"])),
		// SizeBytes is checked by add; the raw map spells it in bytes already.
		SizeBytes: finalJobInt64(raw["size_bytes"]),
		Kind:      kind, MIME: mimeType,
		DurationUS: durationUS, Width: width, Height: height, FrameCount: frameCount,
	})
}

// set replaces the registry contents (the producer re-binds every source to a
// prepared fragment, so its result is the complete list, not an increment).
func (a *finalJobPlanAssets) set(assets []finalJobPlanAsset) error {
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
func (a *finalJobPlanAssets) sorted() []finalJobPlanAsset {
	sort.Slice(a.items, func(i, j int) bool { return a.items[i].AssetID < a.items[j].AssetID })
	return a.items
}
