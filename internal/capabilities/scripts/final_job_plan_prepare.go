package scriptgeneration

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// prepareFinalJobVideoSegments replaces source-video references with verified
// assembly-ready fragments. It keeps audio as a single copy asset and drops
// every unused source video from FINALIZE, so the remote worker's work is only
// packet-copy placement plus final audio mux.
func prepareFinalJobVideoSegments(ctx context.Context, rawPlan string, runtimeAssets []any, preparer FinalJobVideoSegmentPreparer) (string, []any, error) {
	if preparer == nil {
		return "", nil, fmt.Errorf("final_job requires a local video-segment preparer; refusing to send unprepared sources")
	}
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

	preparedAssets := make(map[string]finalJobPlanAsset, len(plan.VideoTracks[0].Segments)+1)
	preparedRefs := make([]any, 0, len(plan.VideoTracks[0].Segments)+1)
	seenPrepared := make(map[string]struct{}, len(plan.VideoTracks[0].Segments))
	for index := range plan.VideoTracks[0].Segments {
		segment := &plan.VideoTracks[0].Segments[index]
		source := refs[segment.AssetID]
		if source == nil {
			return "", nil, fmt.Errorf("final_job segment %q has no runtime source asset %q", segment.SegmentID, segment.AssetID)
		}
		if !strings.EqualFold(finalJobStringValue(source["sha256"]), segment.SHA256) {
			return "", nil, fmt.Errorf("final_job segment %q source SHA-256 does not match the compiled plan", segment.SegmentID)
		}
		request := FinalJobVideoSegmentRequest{
			SegmentID: segment.SegmentID, SourceAssetID: segment.AssetID, SourceSHA256: segment.SHA256,
			SourceInUS: segment.SourceInUS, SourceDurationUS: segment.SourceDurationUS,
			TimelineStartFrame: segment.TimelineStartFrame, FrameCount: segment.FrameCount,
			TimelineRevision: plan.TimelineRevision, TimelineSHA256: plan.TimelineSHA256,
		}
		prepared, err := preparer.PrepareFinalJobVideoSegment(ctx, source, request)
		if err != nil {
			return "", nil, fmt.Errorf("prepare final_job segment %q: %w", segment.SegmentID, err)
		}
		asset, ref, err := finalJobPreparedSegmentAsset(prepared, request)
		if err != nil {
			return "", nil, fmt.Errorf("prepared final_job segment %q: %w", segment.SegmentID, err)
		}
		if _, exists := seenPrepared[asset.AssetID]; exists {
			return "", nil, fmt.Errorf("final_job preparer reused segment asset id %q; each timeline binding must be unique", asset.AssetID)
		}
		seenPrepared[asset.AssetID] = struct{}{}
		preparedAssets[asset.AssetID] = asset
		if strings.EqualFold(finalJobStringValue(source["role"]), "overlay_timeline") {
			ref["role"] = "overlay"
		}
		preparedRefs = append(preparedRefs, ref)
		segment.AssetID = asset.AssetID
		segment.SHA256 = asset.SHA256
		segment.SourceInUS = 0
		segment.SourceDurationUS = asset.DurationUS
	}

	// Preserve the one final audio asset, then add only the fragments referenced
	// by the executable plan. Original stock, clip, and overlay sources no
	// longer cross the FINALIZE boundary.
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
	assets := make([]finalJobPlanAsset, 0, len(preparedAssets)+1)
	assets = append(assets, *audioAsset)
	for _, asset := range preparedAssets {
		assets = append(assets, asset)
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].AssetID < assets[j].AssetID })
	plan.Assets = assets
	// Every video reference in the plan must now bind to one of the freshly
	// prepared fragments with the exact frame and timeline identity.
	for _, segment := range plan.VideoTracks[0].Segments {
		asset, ok := preparedAssets[segment.AssetID]
		if !ok || asset.SHA256 != segment.SHA256 || asset.FrameCount != segment.FrameCount || asset.TimelineStartFrame != segment.TimelineStartFrame {
			return "", nil, fmt.Errorf("final_job prepared segment %q lost its exact manifest binding", segment.SegmentID)
		}
	}

	// Keep FINALIZE minimal: prepared video fragments and the certified master
	// audio. Timeline/overlay placement is represented by the compiled plan.
	for _, raw := range runtimeAssets {
		asset, _ := raw.(map[string]any)
		if asset == nil || !strings.EqualFold(finalJobStringValue(asset["role"]), "final_audio") {
			continue
		}
		preparedRefs = append(preparedRefs, asset)
	}
	// The video role is preserved from its source (stock/clip/overlay) because
	// Master validates that vocabulary while the immutable compiled plan uses
	// kind=video. Final audio remains the last role-tagged asset above.

	canonical, err := json.Marshal(plan)
	if err != nil {
		return "", nil, fmt.Errorf("marshal prepared final_job compiled plan: %w", err)
	}
	return string(canonical), preparedRefs, nil
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
	if finalJobStringValue(raw["profile_id"]) != "VELOX_ASSEMBLY_READY_V1" || !boolValue(raw["copy_eligible"]) || !boolValue(raw["first_frame_keyframe"]) || !boolValue(raw["closed_gop"]) {
		return finalJobPlanAsset{}, nil, fmt.Errorf("preparer did not certify the assembly-ready V1 stream")
	}
	if frameCount != request.FrameCount || finalJobInt64(raw["timeline_revision"]) != request.TimelineRevision ||
		!strings.EqualFold(finalJobStringValue(raw["timeline_sha256"]), request.TimelineSHA256) ||
		finalJobInt64(raw["timeline_start_frame"]) != request.TimelineStartFrame {
		return finalJobPlanAsset{}, nil, fmt.Errorf("preparer returned frame/timeline metadata that does not match the requested segment")
	}
	expectedDurationUS := finalJobFrameDurationUS(request.FrameCount, 24, 1)
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
	asset := finalJobPlanAsset{
		AssetID: id, AssetKey: "sha256:" + sha, SHA256: sha, SizeBytes: size,
		Kind: "video", MIME: "video/mp4", DurationUS: durationUS, Width: 1920, Height: 1080,
		ProfileID: "VELOX_ASSEMBLY_READY_V1", FrameCount: frameCount,
		TimelineRevision: request.TimelineRevision, TimelineSHA256: strings.ToLower(request.TimelineSHA256),
		TimelineStartFrame: request.TimelineStartFrame, FirstFrameKeyframe: true, ClosedGOP: true,
	}
	return asset, ref, nil
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
