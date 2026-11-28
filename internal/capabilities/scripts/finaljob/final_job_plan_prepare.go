package finaljob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/finaljob"
	"go.uber.org/zap"
)

// PrepareFinalJobVideoSegments replaces source-video references with verified
// assembly-ready fragments. It keeps audio as a single copy asset and drops
// every unused source video from FINALIZE, so the remote worker's work is only
// packet-copy placement plus final audio mux.
// PrepareFinalJobVideoSegments is the string entry point kept for callers that
// only hold the sealed plan JSON. The payload path calls
// prepareFinalJobVideoSegments on the in-memory plan and serializes once, after
// this step, so preparation never pays for a decode/encode round-trip.
func PrepareFinalJobVideoSegments(ctx context.Context, rawPlan string, runtimeAssets []any, preparer FinalJobVideoSegmentPreparer) (string, []any, error) {
	if preparer == nil {
		return "", nil, fmt.Errorf("final_job requires a local video-segment preparer; refusing to send unprepared sources")
	}
	var plan FinalJobCompiledPlan
	if err := json.Unmarshal([]byte(rawPlan), &plan); err != nil {
		return "", nil, fmt.Errorf("decode final_job compiled plan before segment preparation: %w", err)
	}
	typedAssets, err := finalJobRuntimeAssetsFromValues(runtimeAssets)
	if err != nil {
		return "", nil, err
	}
	refs, err := prepareFinalJobVideoSegments(ctx, &plan, typedAssets, preparer)
	if err != nil {
		return "", nil, err
	}
	canonicalPlan, err := json.Marshal(plan)
	if err != nil {
		return "", nil, fmt.Errorf("marshal prepared final_job compiled plan: %w", err)
	}
	values, err := finalJobRuntimeAssetsToValues(refs)
	if err != nil {
		return "", nil, err
	}
	return string(canonicalPlan), values, nil
}

// prepareFinalJobVideoSegments rewrites the plan's segments to the certified
// local fragments IN MEMORY and returns the runtime refs that survive.
func prepareFinalJobVideoSegments(ctx context.Context, plan *FinalJobCompiledPlan, runtimeAssets []finaljob.RuntimeAssetV1, preparer FinalJobVideoSegmentPreparer) ([]finaljob.RuntimeAssetV1, error) {
	if preparer == nil {
		return nil, fmt.Errorf("final_job requires a local video-segment preparer; refusing to send unprepared sources")
	}
	if len(plan.VideoTracks) != 1 || len(plan.VideoTracks[0].Segments) == 0 {
		return nil, fmt.Errorf("final_job segment preparation requires one non-empty video track")
	}
	refs := make(map[string]map[string]any, len(runtimeAssets))
	for index, runtimeAsset := range runtimeAssets {
		asset, err := runtimeAsset.Map()
		if err != nil {
			return nil, fmt.Errorf("encode final_job runtime asset %d for segment preparation: %w", index, err)
		}
		id := FinalJobStringValue(asset["asset_id"])
		if id == "" {
			continue
		}
		if prior := refs[id]; prior != nil && !sameFinalJobAssetIdentity(prior, asset) {
			return nil, fmt.Errorf("final_job runtime asset %q has conflicting source references", id)
		}
		refs[id] = asset
	}

	preparedAssets := make(map[string]FinalJobPlanAsset, len(plan.VideoTracks[0].Segments)+1)
	preparedRefs := make([]finaljob.RuntimeAssetV1, 0, len(plan.VideoTracks[0].Segments)+1)
	// Phase 1 — validate every binding and describe the work. This is cheap and
	// sequential, and it fails before any expensive preparation starts.
	work := make([]finalJobSegmentWork, 0, len(plan.VideoTracks[0].Segments))
	// stockBacked marks placements whose bytes stay Master-owned. A concrete
	// stock binding is not a stock_pool_slot: the stockpipeline picked one file
	// and the Master downloads, cuts and loops it itself. The Producer has no
	// certified fragment for those bytes and must not create one (r14: every
	// scene carried a stock binding, so refusing them failed the whole handoff
	// after a successful render).
	stockBacked := make(map[int]bool, len(plan.VideoTracks[0].Segments))
	for index := range plan.VideoTracks[0].Segments {
		segment := &plan.VideoTracks[0].Segments[index]
		// The stock pool is an opaque Master-owned source. It has no producer
		// asset identity and must never enter local download, probe, trim,
		// encode, or upload code.
		if segment.StockPoolSlot {
			continue
		}
		source := refs[segment.AssetID]
		if source == nil {
			return nil, fmt.Errorf("final_job segment %q has no runtime source asset %q", segment.SegmentID, segment.AssetID)
		}
		if strings.EqualFold(FinalJobStringValue(source["role"]), "stock") || boolValue(source["stock_pool_member"]) {
			stockBacked[index] = true
			continue
		}
		if !strings.EqualFold(FinalJobStringValue(source["sha256"]), segment.SHA256) {
			return nil, fmt.Errorf("final_job segment %q source SHA-256 does not match the compiled plan", segment.SegmentID)
		}
		work = append(work, finalJobSegmentWork{
			index:  index,
			source: source,
			request: FinalJobVideoSegmentRequest{
				SegmentID: segment.SegmentID, SourceAssetID: segment.AssetID, SourceSHA256: segment.SHA256,
				SourceInUS: segment.SourceInUS, SourceDurationUS: segment.SourceDurationUS,
				TimelineStartFrame: segment.TimelineStartFrame, FrameCount: segment.FrameCount,
				TimelineRevision: plan.TimelineRevision, TimelineSHA256: plan.TimelineSHA256,
			},
		})
	}
	// PERFLIVE-04: record why each compiled segment exists. The remote report
	// counts encodes, but a count alone cannot answer "which of these had to be
	// prepared and why". This summary is Producer-owned and is written before
	// any fragment is prepared, so it survives a preparation failure.
	reasons := finalJobSegmentReasonSummary(*plan, refs)
	zap.L().Info("final_job segment plan",
		zap.Int("segments", len(plan.VideoTracks[0].Segments)),
		zap.Int("prepared_locally", len(work)),
		zap.Any("reasons", reasons))

	// Phase 2 — prepare the fragments. They are independent, so they run on the
	// preparer's lanes; the result is indexed by plan position, which keeps the
	// binding deterministic whatever the completion order is.
	prepared, err := finalJobPrepareSegments(ctx, preparer, work)
	if err != nil {
		return nil, err
	}
	// Phase 3 — bind in plan order, ONE asset per placement. The assembler
	// certifies each placement against the manifest of its OWN asset
	// (`segment "…" manifest binding is not exact` — r66), so two placements can
	// never share one object even when their pixels are identical. The
	// collision is resolved on the OBJECT, never on the binding: the later
	// placement is re-prepared with a placement tag (same pictures, different
	// container, different bytes), which satisfies both the plan's unique
	// canonical identity and the assembler's per-placement certificate.
	usedSHA := make(map[string]string, len(work))
	usedURI := make(map[string]string, len(work))
	for _, item := range work {
		segment := &plan.VideoTracks[0].Segments[item.index]
		source := item.source
		asset, ref, err := finalJobPreparedSegmentAsset(prepared[item.index], item.request)
		if err != nil {
			return nil, fmt.Errorf("prepared final_job segment %q: %w", segment.SegmentID, err)
		}
		if _, exists := usedSHA[asset.SHA256]; exists {
			// Identical bytes for a second placement. Derive THIS placement from
			// the certified object already produced above: the same bitstream in
			// a distinct container is a naming problem, and re-encoding the same
			// window would pay for pixels that already exist. The tag stays in
			// the work identity, so the tagged object can never be answered with
			// the untagged bytes.
			item.request.PlacementTag = segment.SegmentID
			item.request.ReuseFromSHA256 = asset.SHA256
			item.request.ReuseFromURI = usedURI[asset.SHA256]
			tagged, tagErr := prepareFinalJobSegmentMap(ctx, preparer, source, item.request)
			if tagErr != nil {
				return nil, finalJobSegmentPrepareError(item, tagErr)
			}
			asset, ref, err = finalJobPreparedSegmentAsset(tagged, item.request)
			if err != nil {
				return nil, fmt.Errorf("prepared final_job segment %q: %w", segment.SegmentID, err)
			}
			if other, stillShared := usedSHA[asset.SHA256]; stillShared {
				return nil, fmt.Errorf("final_job segment %q still certifies the bytes of another placement (%q); refusing to send a plan the assembler must reject", segment.SegmentID, other)
			}
		}
		usedSHA[asset.SHA256] = segment.SegmentID
		usedURI[asset.SHA256] = FinalJobStringValue(ref["source_uri"])
		preparedAssets[asset.AssetID] = asset
		// The preparer returns a content-addressed video reference without a
		// producer role. Keep the source's worker-facing role when replacing the
		// source bytes; otherwise Master defaults the new video fragment to its
		// audio asset namespace (and copy-only mux rejects the decoder config).
		role, roleErr := finalJobPreparedSegmentRole(segment.SegmentID, source)
		if roleErr != nil {
			return nil, roleErr
		}
		ref["role"] = role
		// One runtime reference per asset: every placement owns its own object,
		// so FINALIZE receives unique ids without duplicating work.
		typedRef, err := finaljob.RuntimeAssetFromMap(ref)
		if err != nil {
			return nil, fmt.Errorf("encode prepared final_job segment %q runtime asset: %w", segment.SegmentID, err)
		}
		preparedRefs = append(preparedRefs, typedRef)
		segment.AssetID = asset.AssetID
		segment.SHA256 = asset.SHA256
		segment.SourceInUS = 0
		segment.SourceDurationUS = asset.DurationUS
	}

	// Preserve the one final audio asset, then add only the fragments referenced
	// by the executable plan. Original stock, clip, and overlay sources no
	// longer cross the FINALIZE boundary.
	var audioAsset *FinalJobPlanAsset
	for index := range plan.Assets {
		if plan.Assets[index].AssetID == plan.FinalAudio.AssetID {
			asset := plan.Assets[index]
			audioAsset = &asset
			break
		}
	}
	if audioAsset == nil {
		return nil, fmt.Errorf("final_job compiled plan has no final audio asset")
	}
	// The producer re-binds the plan through the SAME asset registry the
	// payload builder filled: one identity rule, one dedupe rule, one order.
	// Stock placements keep their original plan asset and runtime reference:
	// their bytes never become a producer fragment.
	originalAssets := make(map[string]FinalJobPlanAsset, len(plan.Assets))
	for _, asset := range plan.Assets {
		originalAssets[asset.AssetID] = asset
	}
	assets := &finalJobPlanAssets{}
	if err := assets.set([]FinalJobPlanAsset{*audioAsset}); err != nil {
		return nil, fmt.Errorf("final_job compiled plan final audio: %w", err)
	}
	for _, asset := range preparedAssets {
		if err := assets.add(asset); err != nil {
			return nil, fmt.Errorf("prepared final_job asset: %w", err)
		}
	}
	seenStockRef := make(map[string]bool, len(stockBacked))
	for index := range plan.VideoTracks[0].Segments {
		if !stockBacked[index] {
			continue
		}
		segment := plan.VideoTracks[0].Segments[index]
		asset, ok := originalAssets[segment.AssetID]
		if !ok {
			return nil, fmt.Errorf("final_job stock segment %q has no plan asset %q", segment.SegmentID, segment.AssetID)
		}
		if err := assets.add(asset); err != nil {
			return nil, fmt.Errorf("final_job stock asset %q: %w", asset.AssetID, err)
		}
		if ref := refs[asset.AssetID]; ref != nil && !seenStockRef[asset.AssetID] {
			seenStockRef[asset.AssetID] = true
			typedRef, err := finaljob.RuntimeAssetFromMap(ref)
			if err != nil {
				return nil, fmt.Errorf("encode final_job stock asset %q: %w", asset.AssetID, err)
			}
			preparedRefs = append(preparedRefs, typedRef)
		}
	}
	plan.Assets = assets.sorted()
	// Every video reference in the plan must now bind to one of the freshly
	// prepared fragments with the exact frame identity. The timeline position
	// belongs to the segment (and may be shared by several placements of one
	// asset), so it is not compared against the asset.
	for index, segment := range plan.VideoTracks[0].Segments {
		if segment.StockPoolSlot || stockBacked[index] {
			continue
		}
		asset, ok := preparedAssets[segment.AssetID]
		if !ok || asset.SHA256 != segment.SHA256 || asset.FrameCount != segment.FrameCount {
			return nil, fmt.Errorf("final_job prepared segment %q lost its exact manifest binding", segment.SegmentID)
		}
	}

	// Keep FINALIZE minimal: prepared video fragments and the certified master
	// audio. Timeline/overlay placement is represented by the compiled plan.
	for _, asset := range runtimeAssets {
		if !strings.EqualFold(asset.Role, "final_audio") {
			continue
		}
		preparedRefs = append(preparedRefs, asset)
	}
	// The video role is preserved from its source (stock/clip/overlay) because
	// Master validates that vocabulary while the immutable compiled plan uses
	// kind=video. Final audio remains the last role-tagged asset above.

	// The remote applies these exact rules; running them here means a plan that
	// cannot be accepted dies in this process, not in a live run.
	if err := ValidateFinalJobPlanBindingRules(*plan); err != nil {
		return nil, err
	}
	return preparedRefs, nil
}

// finalJobPreparedSegmentRole maps the source role onto the role the Master
// expects on a prepared video fragment. The vocabulary is closed: an unknown
// role would leave the fragment in the audio asset namespace, where the
// copy-only mux rejects it.
func finalJobPreparedSegmentRole(segmentID string, source map[string]any) (string, error) {
	role := strings.ToLower(strings.TrimSpace(FinalJobStringValue(source["role"])))
	switch role {
	case "overlay_timeline":
		return "overlay", nil
	case "clip", "overlay":
		return role, nil
	default:
		return "", fmt.Errorf("final_job segment %q has unsupported source role %q", segmentID, FinalJobStringValue(source["role"]))
	}
}

// FinalJob segment reasons. They name the authority that owns a placement's
// bytes, which is the only honest answer to "why is this an encode?": the
// Producer prepares locally only what the Master cannot take as a stock
// reference.
const (
	FinalJobSegmentReasonStockPool       = "stock_pool_slot"
	FinalJobSegmentReasonStockBound      = "stock_bound"
	FinalJobSegmentReasonOverlayTimeline = "overlay_timeline"
	FinalJobSegmentReasonOverlay         = "overlay"
	FinalJobSegmentReasonClip            = "clip"
	FinalJobSegmentReasonScene           = "scene"
)

// finalJobSegmentReasonSummary groups the compiled plan's segments by the
// reason they exist. It reads only plan positions and runtime refs, so it
// never depends on how preparation scheduled or failed.
func finalJobSegmentReasonSummary(plan FinalJobCompiledPlan, refs map[string]map[string]any) map[string]int {
	summary := make(map[string]int)
	if len(plan.VideoTracks) == 0 {
		return summary
	}
	for _, segment := range plan.VideoTracks[0].Segments {
		summary[finalJobSegmentReason(segment, refs)]++
	}
	return summary
}

func finalJobSegmentReason(segment FinalJobPlanSegment, refs map[string]map[string]any) string {
	if segment.StockPoolSlot {
		return FinalJobSegmentReasonStockPool
	}
	source := refs[segment.AssetID]
	if strings.EqualFold(FinalJobStringValue(source["role"]), "stock") || boolValue(source["stock_pool_member"]) {
		return FinalJobSegmentReasonStockBound
	}
	switch strings.ToLower(strings.TrimSpace(FinalJobStringValue(source["role"]))) {
	case "overlay_timeline":
		return FinalJobSegmentReasonOverlayTimeline
	case "overlay":
		return FinalJobSegmentReasonOverlay
	case "clip":
		return FinalJobSegmentReasonClip
	}
	return FinalJobSegmentReasonScene
}

func finalJobPreparedSegmentAsset(raw map[string]any, request FinalJobVideoSegmentRequest) (FinalJobPlanAsset, map[string]any, error) {
	if raw == nil {
		return FinalJobPlanAsset{}, nil, fmt.Errorf("preparer returned an empty reference")
	}
	id := FinalJobStringValue(raw["asset_id"])
	sha := strings.ToLower(FinalJobStringValue(raw["sha256"]))
	source := strings.TrimSpace(FinalJobStringValue(raw["source_uri"]))
	if source == "" {
		source = strings.TrimSpace(FinalJobStringValue(raw["url"]))
	}
	size := finalJobInt64(raw["size_bytes"])
	durationUS := finalJobInt64(raw["duration_us"])
	if durationUS <= 0 {
		durationUS = finalJobInt64(raw["duration_ms"]) * 1000
	}
	frameCount := finalJobInt64(raw["frame_count"])
	if id == "" || len(sha) != 64 || size <= 0 || !strings.HasPrefix(strings.ToLower(source), "https://") {
		return FinalJobPlanAsset{}, nil, fmt.Errorf("preparer must return an asset id, SHA-256, positive size, and HTTPS source_uri")
	}
	if FinalJobStringValue(raw["profile_id"]) != kernelmedia.AssemblyMediaContractID || !boolValue(raw["copy_eligible"]) || !boolValue(raw["first_frame_keyframe"]) || !boolValue(raw["closed_gop"]) {
		return FinalJobPlanAsset{}, nil, fmt.Errorf("preparer did not certify the assembly-ready V1 stream")
	}
	if frameCount != request.FrameCount || finalJobInt64(raw["timeline_revision"]) != request.TimelineRevision ||
		!strings.EqualFold(FinalJobStringValue(raw["timeline_sha256"]), request.TimelineSHA256) ||
		finalJobInt64(raw["timeline_start_frame"]) != request.TimelineStartFrame {
		return FinalJobPlanAsset{}, nil, fmt.Errorf("preparer returned frame/timeline metadata that does not match the requested segment")
	}
	// Frame math and geometry come from the assembly contract, not from a
	// second copy of 24/1 and 1920x1080 in the client plan builder.
	delivery := kernelmedia.DefaultAssemblyMediaContract()
	expectedDurationUS := finalJobFrameDurationUS(request.FrameCount, int64(delivery.FPS.Num), int64(delivery.FPS.Den))
	if durationUS != expectedDurationUS {
		return FinalJobPlanAsset{}, nil, fmt.Errorf("prepared duration is %dus, expected %dus for %d frames", durationUS, expectedDurationUS, request.FrameCount)
	}
	ref := make(map[string]any, len(raw)+1)
	for key, value := range raw {
		if key == "url" || key == "drive_file_id" {
			continue
		}
		ref[key] = value
	}
	ref["asset_id"], ref["sha256"], ref["size_bytes"], ref["source_uri"] = id, sha, size, source
	delete(ref, "local_path")
	asset := FinalJobPlanAsset{
		AssetID: id, AssetKey: "SHA256:" + sha, SHA256: sha, SizeBytes: size,
		Kind: "video", MIME: "video/mp4", DurationUS: durationUS, Width: delivery.Width, Height: delivery.Height,
		ProfileID: delivery.ID, FrameCount: frameCount,
		TimelineRevision: request.TimelineRevision, TimelineSHA256: strings.ToLower(request.TimelineSHA256),
		TimelineStartFrame: request.TimelineStartFrame, FirstFrameKeyframe: true, ClosedGOP: true,
	}
	return asset, ref, nil
}

func finalJobFrameDurationUS(frameCount, fpsNum, fpsDen int64) int64 {
	return kernelmedia.FrameDurationUSAtFPS(frameCount, fpsNum, fpsDen)
}

func sameFinalJobAssetIdentity(a, b map[string]any) bool {
	return strings.EqualFold(FinalJobStringValue(a["sha256"]), FinalJobStringValue(b["sha256"])) &&
		FinalJobStringValue(a["source_uri"]) == FinalJobStringValue(b["source_uri"]) &&
		finalJobInt64(a["size_bytes"]) == finalJobInt64(b["size_bytes"])
}

// finalJobSegmentWork is one independent fragment preparation: the validated
// source reference, its exact request, and its position in the compiled plan.
type finalJobSegmentWork struct {
	index   int
	source  map[string]any
	request FinalJobVideoSegmentRequest
}

// FinalJobSegmentParallelism is the optional parallelism a preparer declares. A
// preparer that does not implement it is driven one fragment at a time.
type FinalJobSegmentParallelism interface {
	Parallelism() int
}

func finalJobSegmentPrepareError(item finalJobSegmentWork, err error) error {
	return fmt.Errorf(
		"prepare final_job segment %q from asset %q role %q at %dus for %dus: %w",
		item.request.SegmentID,
		FinalJobStringValue(item.source["asset_id"]),
		FinalJobStringValue(item.source["role"]),
		item.request.SourceInUS,
		item.request.SourceDurationUS,
		err,
	)
}

// prepareFinalJobSegmentMap is the only map boundary of the typed preparer
// port: the source enters as a typed asset, the result leaves as a typed
// asset, and the legacy plan builder receives a map only at this seam.
func prepareFinalJobSegmentMap(ctx context.Context, preparer FinalJobVideoSegmentPreparer, source map[string]any, request FinalJobVideoSegmentRequest) (map[string]any, error) {
	sourceAsset, err := finaljob.RuntimeAssetFromMap(source)
	if err != nil {
		return nil, err
	}
	prepared, err := preparer.PrepareFinalJobVideoSegment(ctx, sourceAsset, request)
	if err != nil {
		return nil, err
	}
	if prepared.AssetID == "" && prepared.SHA256 == "" {
		return nil, nil
	}
	values, err := prepared.Map()
	if err != nil {
		return nil, err
	}
	return values, nil
}

// finalJobPrepareSegments prepares the fragments concurrently and returns them
// indexed by plan position, so the binding stays deterministic whatever order
// they complete in. Fail-closed: a failure cancels the remaining work and is
// reported by plan position, never by scheduling luck, and a mere cancellation
// is not reported over a real cause.
func finalJobPrepareSegments(ctx context.Context, preparer FinalJobVideoSegmentPreparer, work []finalJobSegmentWork) ([]map[string]any, error) {
	resultSize := 0
	for _, item := range work {
		if item.index >= resultSize {
			resultSize = item.index + 1
		}
	}
	prepared := make([]map[string]any, resultSize)
	if len(work) == 0 {
		return prepared, nil
	}
	lanes := 1
	if declared, ok := preparer.(FinalJobSegmentParallelism); ok {
		if lanes = declared.Parallelism(); lanes < 1 {
			lanes = 1
		}
	}
	if lanes > len(work) {
		lanes = len(work)
	}
	if lanes == 1 {
		for _, item := range work {
			result, err := prepareFinalJobSegmentMap(ctx, preparer, item.source, item.request)
			if err != nil {
				return nil, finalJobSegmentPrepareError(item, err)
			}
			prepared[item.index] = result
		}
		return prepared, nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	failures := make([]error, resultSize)
	gates := make(chan struct{}, lanes)
	var wg sync.WaitGroup
	for _, item := range work {
		wg.Add(1)
		go func(item finalJobSegmentWork) {
			defer wg.Done()
			select {
			case gates <- struct{}{}:
				defer func() { <-gates }()
			case <-runCtx.Done():
				return
			}
			if runCtx.Err() != nil {
				return
			}
			result, err := prepareFinalJobSegmentMap(runCtx, preparer, item.source, item.request)
			if err != nil {
				failures[item.index] = finalJobSegmentPrepareError(item, err)
				cancel()
				return
			}
			prepared[item.index] = result
		}(item)
	}
	wg.Wait()
	var cancelled error
	for _, err := range failures {
		if err == nil {
			continue
		}
		if errors.Is(err, context.Canceled) {
			if cancelled == nil {
				cancelled = err
			}
			continue
		}
		return nil, err
	}
	if cancelled != nil {
		return nil, cancelled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return prepared, nil
}
