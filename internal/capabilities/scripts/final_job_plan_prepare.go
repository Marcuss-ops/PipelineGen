package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// prepareFinalJobVideoSegments seals the executable plan for the remote Master.
// Producer-owned overlay and clip placements are cut into certified fragments;
// stock is deliberately passed through as its stockpipeline Drive reference and
// source window. The Producer never downloads, probes, hashes, cuts, encodes or
// publishes stock bytes: the Master owns that processing. Audio remains a
// single copy asset.
func prepareFinalJobVideoSegments(ctx context.Context, rawPlan string, runtimeAssets []any, preparer FinalJobVideoSegmentPreparer) (string, []any, error) {
	var plan finalJobCompiledPlan
	if err := json.Unmarshal([]byte(rawPlan), &plan); err != nil {
		return "", nil, fmt.Errorf("decode final_job compiled plan before segment preparation: %w", err)
	}
	if len(plan.VideoTracks) != 1 || len(plan.VideoTracks[0].Segments) == 0 {
		return "", nil, fmt.Errorf("final_job segment preparation requires one non-empty video track")
	}
	refs := make(map[string]map[string]any, len(runtimeAssets))
	for index, raw := range runtimeAssets {
		asset, ok := raw.(map[string]any)
		if !ok {
			return "", nil, fmt.Errorf("final_job runtime asset %d has an invalid shape", index)
		}
		id := finalJobStringValue(asset["asset_id"])
		if id == "" {
			continue
		}
		if prior := refs[id]; prior != nil && !sameFinalJobAssetIdentity(prior, asset) {
			return "", nil, fmt.Errorf("final_job runtime asset %q has conflicting source references", id)
		}
		refs[id] = asset
	}

	if err := alignFinalJobSegmentSourceWindows(&plan); err != nil {
		return "", nil, err
	}
	preparedAssets := make(map[string]finalJobPlanAsset, len(plan.VideoTracks[0].Segments)+1)
	preparedRefs := make([]any, 0, len(plan.VideoTracks[0].Segments)+1)
	planAssets := make(map[string]finalJobPlanAsset, len(plan.Assets))
	for _, asset := range plan.Assets {
		planAssets[asset.AssetID] = asset
	}
	// Phase 1 — validate every binding and describe the work. This is cheap and
	// sequential, and it fails before any expensive preparation starts. Stock
	// is not work: it keeps the exact Drive reference and source window the
	// stockpipeline already certified, for the Master to process remotely.
	work := make([]finalJobSegmentWork, 0, len(plan.VideoTracks[0].Segments))
	workByIndex := make(map[int]finalJobSegmentWork, len(plan.VideoTracks[0].Segments))
	stockIndex := make(map[int]bool, len(plan.VideoTracks[0].Segments))
	stockAssets := make(map[string]finalJobPlanAsset, len(plan.VideoTracks[0].Segments))
	for index := range plan.VideoTracks[0].Segments {
		segment := &plan.VideoTracks[0].Segments[index]
		source := refs[segment.AssetID]
		if source == nil {
			return "", nil, fmt.Errorf("final_job segment %q has no runtime source asset %q", segment.SegmentID, segment.AssetID)
		}
		if !strings.EqualFold(finalJobStringValue(source["sha256"]), segment.SHA256) {
			return "", nil, fmt.Errorf("final_job segment %q source SHA-256 does not match the compiled plan", segment.SegmentID)
		}
		if isFinalJobStockSource(source) {
			if finalJobStringValue(source["drive_file_id"]) == "" {
				return "", nil, fmt.Errorf("final_job stock segment %q must remain a Drive reference; local stock processing is disabled", segment.SegmentID)
			}
			asset, ok := planAssets[segment.AssetID]
			if !ok {
				return "", nil, fmt.Errorf("final_job stock segment %q has no compiled plan asset %q", segment.SegmentID, segment.AssetID)
			}
			stockIndex[index] = true
			stockAssets[asset.AssetID] = asset
			continue
		}
		item := finalJobSegmentWork{
			index:  index,
			source: source,
			request: FinalJobVideoSegmentRequest{
				SegmentID: segment.SegmentID, SourceAssetID: segment.AssetID, SourceSHA256: segment.SHA256,
				SourceInUS: segment.SourceInUS, SourceDurationUS: segment.SourceDurationUS,
				TimelineStartFrame: segment.TimelineStartFrame, FrameCount: segment.FrameCount,
				TimelineRevision: plan.TimelineRevision, TimelineSHA256: plan.TimelineSHA256,
			},
		}
		work = append(work, item)
		workByIndex[index] = item
	}
	// Phase 2 — prepare only the Producer-owned fragments. They are independent,
	// so they run on the preparer's lanes; the result is indexed by plan
	// position, which keeps the binding deterministic whatever the completion
	// order is. A stock-only plan performs no local work and needs no preparer.
	if len(work) > 0 && preparer == nil {
		return "", nil, fmt.Errorf("final_job requires a local video-segment preparer for overlay and clip sources")
	}
	prepared, err := finalJobPrepareSegments(ctx, preparer, work)
	if err != nil {
		return "", nil, err
	}
	// Phase 3 — bind in plan order. Producer-owned placements get ONE fragment
	// each; stock keeps its stockpipeline asset and source window untouched.
	// The assembler certifies each prepared placement against the manifest of
	// its OWN asset (`segment "…" manifest binding is not exact` — r66), so two
	// placements can never share one object even when their pixels are identical.
	// The collision is resolved on the OBJECT, never on the binding: the later
	// placement is re-prepared with a placement tag (same pictures, different
	// container, different bytes), which satisfies both the plan's unique
	// canonical identity and the assembler's per-placement certificate.
	usedSHA := make(map[string]string, len(work))
	usedURI := make(map[string]string, len(work))
	stockRefs := make(map[string]bool, len(stockAssets))
	for index := range plan.VideoTracks[0].Segments {
		segment := &plan.VideoTracks[0].Segments[index]
		if stockIndex[index] {
			asset, ok := stockAssets[segment.AssetID]
			if !ok {
				return "", nil, fmt.Errorf("final_job stock segment %q lost its compiled plan asset", segment.SegmentID)
			}
			preparedAssets[asset.AssetID] = asset
			if !stockRefs[asset.AssetID] {
				source := refs[segment.AssetID]
				// A stockpipeline reference is Drive metadata plus the remote
				// locator. Any local path would invite the Producer to process the
				// source again, so it must never cross FINALIZE.
				delete(source, "local_path")
				delete(source, "local_source_path")
				delete(source, "local_only")
				// The runtime reference must describe a video source; the Master
				// downloads it from Drive and performs the cut itself.
				if finalJobStringValue(source["kind"]) == "" {
					source["kind"] = "video"
				}
				if finalJobStringValue(source["mime"]) == "" {
					source["mime"] = "video/mp4"
				}
				preparedRefs = append(preparedRefs, source)
				stockRefs[asset.AssetID] = true
			}
			continue
		}
		item, ok := workByIndex[index]
		if !ok {
			return "", nil, fmt.Errorf("final_job segment %q has no preparation work", segment.SegmentID)
		}
		source := item.source
		asset, ref, err := finalJobPreparedSegmentAsset(prepared[item.index], item.request)
		if err != nil {
			return "", nil, fmt.Errorf("prepared final_job segment %q: %w", segment.SegmentID, err)
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
			tagged, tagErr := preparer.PrepareFinalJobVideoSegment(ctx, source, item.request)
			if tagErr != nil {
				return "", nil, finalJobSegmentPrepareError(item, tagErr)
			}
			asset, ref, err = finalJobPreparedSegmentAsset(tagged, item.request)
			if err != nil {
				return "", nil, fmt.Errorf("prepared final_job segment %q: %w", segment.SegmentID, err)
			}
			if other, stillShared := usedSHA[asset.SHA256]; stillShared {
				return "", nil, fmt.Errorf("final_job segment %q still certifies the bytes of another placement (%q); refusing to send a plan the assembler must reject", segment.SegmentID, other)
			}
		}
		usedSHA[asset.SHA256] = segment.SegmentID
		usedURI[asset.SHA256] = finalJobStringValue(ref["source_uri"])
		preparedAssets[asset.AssetID] = asset
		if strings.EqualFold(finalJobStringValue(source["role"]), "overlay_timeline") {
			ref["role"] = "overlay"
		}
		// One runtime reference per asset: every placement owns its own object,
		// so FINALIZE receives unique ids without duplicating work.
		preparedRefs = append(preparedRefs, ref)
		segment.AssetID = asset.AssetID
		segment.SHA256 = asset.SHA256
		segment.SourceInUS = 0
		segment.SourceDurationUS = asset.DurationUS
	}

	// Preserve the one final audio asset, then add the prepared fragments and
	// the untouched stockpipeline references the executable plan still names.
	var audioAsset *finalJobPlanAsset
	for index := range plan.Assets {
		if plan.Assets[index].AssetID == plan.FinalAudio.AssetID {
			asset := plan.Assets[index]
			audioAsset = &asset
			break
		}
	}
	if audioAsset == nil {
		return "", nil, fmt.Errorf("final_job compiled plan has no final audio asset")
	}
	// The producer re-binds the plan through the SAME asset registry the
	// payload builder filled: one identity rule, one dedupe rule, one order.
	assets := &finalJobPlanAssets{}
	if err := assets.set([]finalJobPlanAsset{*audioAsset}); err != nil {
		return "", nil, fmt.Errorf("final_job compiled plan final audio: %w", err)
	}
	for _, asset := range preparedAssets {
		if err := assets.add(asset); err != nil {
			return "", nil, fmt.Errorf("prepared final_job asset: %w", err)
		}
	}
	plan.Assets = assets.sorted()
	// Every video reference in the plan must bind to the asset it names. Prepared
	// placements carry the exact frame identity; stock keeps the source extent
	// and remains addressable by the Master for remote processing. The timeline
	// position belongs to the segment (and may be shared by several placements of
	// one asset), so it is not compared against the asset.
	for index, segment := range plan.VideoTracks[0].Segments {
		asset, ok := preparedAssets[segment.AssetID]
		if !ok || asset.SHA256 != segment.SHA256 {
			return "", nil, fmt.Errorf("final_job segment %q lost its exact manifest binding", segment.SegmentID)
		}
		if !stockIndex[index] && asset.FrameCount != segment.FrameCount {
			return "", nil, fmt.Errorf("final_job prepared segment %q lost its exact manifest binding", segment.SegmentID)
		}
	}

	// Keep FINALIZE minimal: prepared Producer fragments, untouched stock Drive
	// references and the certified master audio. Timeline/overlay placement is
	// represented by the compiled plan.
	for _, raw := range runtimeAssets {
		asset, _ := raw.(map[string]any)
		if asset == nil || !strings.EqualFold(finalJobStringValue(asset["role"]), "final_audio") {
			continue
		}
		preparedRefs = append(preparedRefs, asset)
	}
	// The video role is preserved from its source (stock/clip/overlay) because
	// Master validates that vocabulary while the immutable compiled plan uses
	// kind=video. Stock stays role=stock with its Drive locator; it is the only
	// video role the Master downloads and cuts itself. Final audio remains the
	// last role-tagged asset above.

	// The remote applies these exact rules; running them here means a plan that
	// cannot be accepted dies in this process, not in a live run.
	if err := validateFinalJobPlanBindingRules(plan); err != nil {
		return "", nil, err
	}
	canonicalPlan, err := json.Marshal(plan)
	if err != nil {
		return "", nil, fmt.Errorf("marshal prepared final_job compiled plan: %w", err)
	}
	return string(canonicalPlan), preparedRefs, nil
}

// alignFinalJobSegmentSourceWindows converts every source window from the
// timeline's arbitrary microsecond boundaries to the delivery frame grid. The
// compiled plan already quantizes the TIMELINE; without this step the SOURCE
// side still asks for ranges such as 4,475,000us for 13 frames at 24 fps, which
// no file can satisfy exactly (12.6 frames). Producer-owned windows are cut
// locally into a zero-based fragment, while stock keeps this frame-aligned
// source window for the remote Master to cut and normalize itself.
func alignFinalJobSegmentSourceWindows(plan *finalJobCompiledPlan) error {
	if plan == nil || len(plan.VideoTracks) == 0 {
		return nil
	}
	assets := make(map[string]finalJobPlanAsset, len(plan.Assets))
	for _, asset := range plan.Assets {
		assets[asset.AssetID] = asset
	}
	delivery := kernelmedia.DefaultAssemblyMediaContract()
	fpsNum, fpsDen := int64(delivery.FPS.Num), int64(delivery.FPS.Den)
	for trackIndex := range plan.VideoTracks {
		for segmentIndex := range plan.VideoTracks[trackIndex].Segments {
			segment := &plan.VideoTracks[trackIndex].Segments[segmentIndex]
			if segment.FrameCount <= 0 {
				continue
			}
			asset, ok := assets[segment.AssetID]
			if !ok {
				return fmt.Errorf("final_job segment %q has no plan asset %q for frame alignment", segment.SegmentID, segment.AssetID)
			}
			totalFrames := asset.FrameCount
			if totalFrames <= 0 && asset.DurationUS > 0 {
				totalFrames = finalJobFrameAtUS(asset.DurationUS, fpsNum, fpsDen)
				// A millisecond-rounded duration can round UP to a frame whose
				// start lies past the asset's real end. The frame itself does not
				// exist, so the last admissible frame is the one before it.
				if totalFrames > 0 && finalJobFrameStartUS(totalFrames, fpsNum, fpsDen) > asset.DurationUS {
					totalFrames--
				}
			}
			if totalFrames <= 0 {
				return fmt.Errorf("final_job segment %q asset %q has no frame-addressable duration", segment.SegmentID, asset.AssetID)
			}
			// The declared duration can be one frame shorter than the bytes: a
			// 5.000s/120-frame stock source is often recorded as 4.990s, and the
			// conservative rounding below then reports 119. The local fragment
			// preparer (or the remote stock cutter) is the frame authority and
			// pads a measured tail shortfall when the bytes really are short, so
			// admit exactly that one-frame boundary case only when the window
			// starts at the source origin. Any larger mismatch still fails here.
			shortfall := segment.FrameCount - totalFrames
			startFrame := finalJobFrameAtUS(segment.SourceInUS, fpsNum, fpsDen)
			if shortfall > 1 || (shortfall == 1 && startFrame != 0) {
				return fmt.Errorf("final_job segment %q asks for %d frames from asset %q, which carries %d", segment.SegmentID, segment.FrameCount, asset.AssetID, totalFrames)
			}
			maxStart := totalFrames - segment.FrameCount
			if maxStart < 0 {
				maxStart = 0
			}
			if startFrame > maxStart {
				startFrame = maxStart
			}
			if startFrame < 0 {
				startFrame = 0
			}
			segment.SourceInUS = finalJobFrameStartUS(startFrame, fpsNum, fpsDen)
			segment.SourceDurationUS = finalJobFrameDurationUS(segment.FrameCount, fpsNum, fpsDen)
			if overrun := segment.SourceInUS + segment.SourceDurationUS - asset.DurationUS; overrun > 0 {
				if shortfall <= 0 || overrun > finalJobFrameDurationUS(shortfall, fpsNum, fpsDen) {
					return fmt.Errorf("final_job segment %q aligned source window exceeds asset %q (%d + %d > %d)", segment.SegmentID, asset.AssetID, segment.SourceInUS, segment.SourceDurationUS, asset.DurationUS)
				}
			}
		}
	}
	return nil
}

func finalJobPreparedSegmentAsset(raw map[string]any, request FinalJobVideoSegmentRequest) (finalJobPlanAsset, map[string]any, error) {
	if raw == nil {
		return finalJobPlanAsset{}, nil, fmt.Errorf("preparer returned an empty reference")
	}
	id := finalJobStringValue(raw["asset_id"])
	sha := strings.ToLower(finalJobStringValue(raw["sha256"]))
	source := strings.TrimSpace(finalJobStringValue(raw["source_uri"]))
	if source == "" {
		source = strings.TrimSpace(finalJobStringValue(raw["url"]))
	}
	size := finalJobInt64(raw["size_bytes"])
	durationUS := finalJobInt64(raw["duration_us"])
	if durationUS <= 0 {
		durationUS = finalJobInt64(raw["duration_ms"]) * 1000
	}
	frameCount := finalJobInt64(raw["frame_count"])
	if id == "" || len(sha) != 64 || size <= 0 || !strings.HasPrefix(strings.ToLower(source), "https://") {
		return finalJobPlanAsset{}, nil, fmt.Errorf("preparer must return an asset id, SHA-256, positive size, and HTTPS source_uri")
	}
	if finalJobStringValue(raw["profile_id"]) != kernelmedia.AssemblyMediaContractID || !boolValue(raw["copy_eligible"]) || !boolValue(raw["first_frame_keyframe"]) || !boolValue(raw["closed_gop"]) {
		return finalJobPlanAsset{}, nil, fmt.Errorf("preparer did not certify the assembly-ready V1 stream")
	}
	if frameCount != request.FrameCount || finalJobInt64(raw["timeline_revision"]) != request.TimelineRevision ||
		!strings.EqualFold(finalJobStringValue(raw["timeline_sha256"]), request.TimelineSHA256) ||
		finalJobInt64(raw["timeline_start_frame"]) != request.TimelineStartFrame {
		return finalJobPlanAsset{}, nil, fmt.Errorf("preparer returned frame/timeline metadata that does not match the requested segment")
	}
	// Frame math and geometry come from the assembly contract, not from a
	// second copy of 24/1 and 1920x1080 in the client plan builder.
	delivery := kernelmedia.DefaultAssemblyMediaContract()
	expectedDurationUS := finalJobFrameDurationUS(request.FrameCount, int64(delivery.FPS.Num), int64(delivery.FPS.Den))
	if durationUS != expectedDurationUS {
		return finalJobPlanAsset{}, nil, fmt.Errorf("prepared duration is %dus, expected %dus for %d frames", durationUS, expectedDurationUS, request.FrameCount)
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
	delete(ref, "local_source_path")
	delete(ref, "local_only")
	asset := finalJobPlanAsset{
		AssetID: id, AssetKey: id, SHA256: sha, SizeBytes: size,
		Kind: "video", MIME: "video/mp4", DurationUS: durationUS, Width: delivery.Width, Height: delivery.Height, ProfileID: delivery.ID, FrameCount: frameCount,
		TimelineRevision: request.TimelineRevision, TimelineSHA256: strings.ToLower(request.TimelineSHA256),
		TimelineStartFrame: request.TimelineStartFrame, FirstFrameKeyframe: true, ClosedGOP: true,
	}
	return asset, ref, nil
}

func isFinalJobStockSource(source map[string]any) bool {
	return source != nil && strings.EqualFold(finalJobStringValue(source["role"]), "stock")
}

func finalJobFrameDurationUS(frameCount, fpsNum, fpsDen int64) int64 {
	if frameCount <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	numerator := frameCount * 1_000_000 * fpsDen
	return (numerator + fpsNum/2) / fpsNum
}

func sameFinalJobAssetIdentity(a, b map[string]any) bool {
	return strings.EqualFold(finalJobStringValue(a["sha256"]), finalJobStringValue(b["sha256"])) &&
		finalJobStringValue(a["source_uri"]) == finalJobStringValue(b["source_uri"]) &&
		finalJobInt64(a["size_bytes"]) == finalJobInt64(b["size_bytes"])
}

func boolValue(value any) bool {
	b, _ := value.(bool)
	return b
}

func validateFinalJobHTTPSRuntimeAssets(assets []any) error {
	for i, raw := range assets {
		asset, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("final_job runtime asset %d has an invalid shape", i)
		}
		source := strings.TrimSpace(finalJobStringValue(asset["source_uri"]))
		if source == "" {
			source = strings.TrimSpace(finalJobStringValue(asset["url"]))
		}
		if !strings.HasPrefix(strings.ToLower(source), "https://") {
			return fmt.Errorf("final_job runtime asset %q has no HTTPS source_uri", finalJobStringValue(asset["asset_id"]))
		}
		assetID := strings.TrimSpace(finalJobStringValue(asset["asset_id"]))
		if assetID == "" {
			return fmt.Errorf("final_job runtime asset %d has no immutable asset_id", i)
		}
		delete(asset, "drive_file_id")
		// The Master uses `source_uri` to construct the signed HTTPS future-asset
		// plan, while the worker's runtime asset resolver requires `url` to stay
		// an internal asset reference. Sending the raw signed URL in `url` fails
		// closed in that resolver before the worker can consume the HTTPS plan.
		asset["source_uri"] = source
		asset["url"] = "velox-asset://" + assetID
	}
	return nil
}

func finalJobSceneDurationMS(scenes []map[string]any) int64 {
	var total int64
	for _, scene := range scenes {
		seconds, ok := scene["duration_seconds"].(float64)
		if ok && !math.IsNaN(seconds) && !math.IsInf(seconds, 0) && seconds > 0 {
			total += int64(math.Round(seconds * 1000))
		}
	}
	return total
}

// trimFinalJobSceneTail removes a small rounding surplus from the end of the
// video timeline while preserving the minimum duration of every scene. The
// packet-copy renderer requires its video timeline not to exceed final audio.
func trimFinalJobSceneTail(scenes []map[string]any, surplusMS, minimumMS int64) error {
	if surplusMS <= 0 {
		return nil
	}
	for i := len(scenes) - 1; i >= 0 && surplusMS > 0; i-- {
		seconds, ok := scenes[i]["duration_seconds"].(float64)
		if !ok || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return fmt.Errorf("final_job scene %d has an invalid duration while aligning audio", i)
		}
		durationMS := int64(math.Round(seconds * 1000))
		available := durationMS - minimumMS
		if available <= 0 {
			continue
		}
		trim := min(available, surplusMS)
		scenes[i]["duration_seconds"] = float64(durationMS-trim) / 1000
		surplusMS -= trim
	}
	if surplusMS > 0 {
		return fmt.Errorf("final_job cannot trim %dms to fit the certified audio without violating minimum scene duration", surplusMS)
	}
	return nil
}

// finalJobIdempotencyKey stays stable when the same submitted request is
// retried under a new local run id. The remote Master uses this key to return
// the already-prepared render instead of starting a duplicate video job.
// Older/internal callers without a request key retain the run-id behavior.
func finalJobIdempotencyKey(requestKey, runID string) string {
	requestKey = strings.TrimSpace(requestKey)
	if requestKey == "" {
		return "creator-77-" + strings.TrimSpace(runID)
	}
	// 2026-09-28: the digest SSOT (godlike/06) owns the algorithm. This call
	// used to import crypto/sha256 directly, which the archcheck
	// digest_sha256_import_outside_ssot gate rejects; SHA256String returns the
	// same lowercase hex, so the first 32 characters are the exact 16 bytes the
	// previous hex.EncodeToString(sum[:16]) produced.
	return "creator-77-request-" + digest.SHA256String(requestKey)[:32]
}

// finalJobAttemptIdempotencyKey is stable while reconciling one PREPARE
// attempt, and distinct after Master reports a terminal lease.
func finalJobAttemptIdempotencyKey(requestKey, runID string, attempt int) string {
	if attempt < 1 {
		attempt = 1
	}
	return finalJobIdempotencyKey(requestKey, runID) + fmt.Sprintf("-attempt-%d", attempt)
}

// compositeVideoScene preserves source identity in the remote contract. Stock
// footage and caller-provided clips both carry silent video in the `stock`
// media slot, but `kind` determines which pipeline policy owns that video.
// Keep it explicit so stock can never inherit clip subtitle/transcript work.
func compositeVideoScene(id string, index int, kind string, asset map[string]any, durationMS int64, text string) map[string]any {
	ref := make(map[string]any, len(asset))
	for k, v := range asset {
		// Materialized local paths are only for the Creator-side compositor. The
		// worker fetches the certified source_uri over HTTPS.
		if k == "local_path" {
			continue
		}
		ref[k] = v
	}
	if driveID := finalJobStringValue(ref["drive_file_id"]); driveID != "" {
		ref["url"] = driveFileWebLink(driveID)
	}
	scene := map[string]any{"scene_id": id, "index": index, "kind": kind, "duration_seconds": float64(durationMS) / 1000, "stock": ref}
	// PREPARE requires non-empty text on every scene, but stock scene text is
	// rendered as subtitles. Stock footage is intentionally caption-free, so
	// discard any narration/title and satisfy the schema with an invisible
	// separator. Clip scenes retain their real caption when provided.
	if kind == "stock" || strings.TrimSpace(text) == "" {
		text = "\u200b"
	}
	scene["text"] = text
	return scene
}

// The remote renderer requires each scene to be at least 100 ms. Audio and
// source timing can leave a short final remainder (for example, 16 ms) after
// millisecond rounding. Borrow that remainder from preceding scenes while
// keeping every scene above the limit and preserving the total runtime.
func enforceFinalJobMinimumSceneDuration(scenes []map[string]any, minimumMS int64) error {
	if minimumMS <= 0 {
		return fmt.Errorf("final_job minimum scene duration must be positive")
	}
	for i, scene := range scenes {
		seconds, ok := scene["duration_seconds"].(float64)
		if !ok || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
			return fmt.Errorf("final_job scene %d has an invalid duration", i)
		}
		durationMS := int64(math.Round(seconds * 1000))
		if durationMS >= minimumMS {
			continue
		}
		deficit := minimumMS - durationMS
		for previous := i - 1; previous >= 0 && deficit > 0; previous-- {
			previousSeconds, valid := scenes[previous]["duration_seconds"].(float64)
			if !valid || math.IsNaN(previousSeconds) || math.IsInf(previousSeconds, 0) {
				return fmt.Errorf("final_job scene %d has an invalid duration", previous)
			}
			previousMS := int64(math.Round(previousSeconds * 1000))
			available := previousMS - minimumMS
			if available <= 0 {
				continue
			}
			take := min(available, deficit)
			scenes[previous]["duration_seconds"] = float64(previousMS-take) / 1000
			deficit -= take
		}
		if deficit > 0 {
			return fmt.Errorf("final_job scene %d is shorter than %dms with no preceding duration to borrow", i, minimumMS)
		}
		scene["duration_seconds"] = float64(minimumMS) / 1000
	}
	return nil
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
		finalJobStringValue(item.source["asset_id"]),
		finalJobStringValue(item.source["role"]),
		item.request.SourceInUS,
		item.request.SourceDurationUS,
		err,
	)
}

// finalJobPrepareSegments prepares the fragments concurrently and returns them
// indexed by plan position, so the binding stays deterministic whatever order
// they complete in. Fail-closed: a failure cancels the remaining work and the
// FIRST observed failure is reported, because that is the one that triggered the
// cancellation. Reporting by plan position lets a collateral `signal: killed`
// from an earlier segment hide the real encoder error that cancelled the lanes.
func finalJobPrepareSegments(ctx context.Context, preparer FinalJobVideoSegmentPreparer, work []finalJobSegmentWork) ([]map[string]any, error) {
	// Work contains every video placement, stock included. Results are consumed by
	// plan position, so the binding never depends on completion order.
	resultCount := 0
	for _, item := range work {
		if item.index < 0 {
			return nil, fmt.Errorf("final_job segment %q has a negative plan index %d", item.request.SegmentID, item.index)
		}
		if item.index >= resultCount {
			resultCount = item.index + 1
		}
	}
	prepared := make([]map[string]any, resultCount)
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
			result, err := preparer.PrepareFinalJobVideoSegment(ctx, item.source, item.request)
			if err != nil {
				return nil, finalJobSegmentPrepareError(item, err)
			}
			prepared[item.index] = result
		}
		return prepared, nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	failures := make([]error, resultCount)
	failureAt := make([]time.Time, resultCount)
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
			result, err := preparer.PrepareFinalJobVideoSegment(runCtx, item.source, item.request)
			if err != nil {
				failureAt[item.index] = time.Now()
				failures[item.index] = finalJobSegmentPrepareError(item, err)
				cancel()
				return
			}
			prepared[item.index] = result
		}(item)
	}
	wg.Wait()
	var cancelled error
	var earliest error
	var earliestAt time.Time
	for index, err := range failures {
		if err == nil {
			continue
		}
		if errors.Is(err, context.Canceled) {
			if cancelled == nil {
				cancelled = err
			}
			continue
		}
		if earliest == nil || failureAt[index].Before(earliestAt) {
			earliest, earliestAt = err, failureAt[index]
		}
	}
	if earliest != nil {
		return nil, earliest
	}
	if cancelled != nil {
		return nil, cancelled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return prepared, nil
}
