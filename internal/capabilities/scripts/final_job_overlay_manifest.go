package scriptgeneration

// final_job_overlay_manifest.go builds the pure-manifest overlay handoff of
// the final-job PREPARE payload (OVERLAY-MANIFEST-CONTRACT.md §4 cutover):
// ONE certified timeline asset plus the typed manifest document
// (pipelinegen.overlay-manifest.v1) for every item the composition covers,
// instead of one Drive replace row per overlay. Independent replace rows
// survive ONLY for the dedicated-path items (maps, chain), whose renderers
// own separate artifacts and lifecycles.
//
// The wire is fail-closed exactly like the per-item handoff it replaces: a
// missing/unpublished timeline artifact, an uncertified digest, an entry that
// starts after the certified base ends, or a dedicated item without its own
// certified artifact all fail the whole handoff before any remote attempt.

import (
	"context"
	"fmt"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// normalizeFinalJobOverlayWindows applies the final-job placement policy to
// the plan BEFORE rendering. The pure-manifest wire freezes placement at
// render time (entries document the artifact's frames), so the policy that
// the per-item handoff applied at payload time must move upstream: the
// composition, the manifest and the payload then all agree on where every
// item sits. Idempotent — the moved window is computed absolutely from the
// clip scene's certified start — so resumed runs can re-apply it safely.
//
// The one placement rule that survives: a phrase callout on an intermediate
// clip scene (original clip audio + generated narration) must sit JUST BEFORE
// the clip, never over it — replace-mode frames would cover the source video
// that has to stay visible. Image-style cards on those scenes are dropped at
// handoff time instead (their frames are simply never placed).
func normalizeFinalJobOverlayWindows(plan *capoverlay.OverlayPlan, result *GenerateResult) error {
	if plan == nil {
		return nil
	}
	clipWindows := finalJobIntermediateClipWindows(result)
	if len(clipWindows) == 0 {
		return nil
	}
	fpsNum, fpsDen := plan.FPSNum, plan.FPSDen
	if fpsNum <= 0 || fpsDen <= 0 {
		fpsNum, fpsDen = 24, 1
	}
	frameGuardUS := (1_000_000*int64(fpsDen) + int64(fpsNum) - 1) / int64(fpsNum)
	for index := range plan.Items {
		item := &plan.Items[index]
		clipWindow, isIntermediateClip := clipWindows[item.SceneID]
		if !isIntermediateClip || item.Kind != "text_phrase" {
			continue
		}
		durationUS := item.EndUSValue() - item.StartUSValue()
		if durationUS <= 0 {
			return fmt.Errorf("final_job clip-scene phrase %q has an empty timing window", item.ID)
		}
		endUS := clipWindow.startUS - frameGuardUS
		startUS := endUS - durationUS
		if startUS < clipWindow.previousStartUS {
			return fmt.Errorf("final_job clip-scene phrase %q cannot fit before clip scene %q", item.ID, item.SceneID)
		}
		item.StartUS = startUS
		item.DurationUS = durationUS
		item.StartMs = startUS / 1000
		item.EndMs = (endUS + 999) / 1000
		item.RenderKey = ""
	}
	return nil
}

// finalJobOverlayHandoff returns the replace rows for dedicated-path items
// and the overlay_manifest wire document for the composition. Either side
// may be empty (a plan with no maps/chain emits no rows; a plan with only
// maps/chain emits no manifest), but never both when semantic overlays exist.
func finalJobOverlayHandoff(ctx context.Context, result *GenerateResult, consumed map[string]struct{}) ([]any, map[string]any, error) {
	if result == nil || result.OverlayPlan == nil || len(result.OverlayPlan.Items) == 0 {
		return []any{}, nil, nil
	}
	if result.OverlayRender == nil || (result.OverlayRender.Manifest == nil && len(result.OverlayRender.Items) == 0) {
		return nil, nil, fmt.Errorf("final_job has semantic overlays but no published overlay render artifacts")
	}
	fpsNum, fpsDen := result.OverlayPlan.FPSNum, result.OverlayPlan.FPSDen
	if fpsNum <= 0 || fpsDen <= 0 {
		return nil, nil, fmt.Errorf("final_job overlay plan has invalid frame rate %d/%d", fpsNum, fpsDen)
	}
	baseFrameCount := int64(0)
	if result.FinalAudio != nil && result.FinalAudio.DurationMS > 0 {
		// The remote composite's base is bounded by the certified final audio.
		// Semantic overlay timing can extend a little past that boundary (for
		// example, a final date card planned against a longer source scene).
		// The worker clamps the last visible window to the actual output
		// (contract §3), so an entry that STARTS past the base is the only
		// hard error here.
		baseFrameCount = int64(result.FinalAudio.DurationMS) * int64(fpsNum) / (1000 * int64(fpsDen))
	}
	// One SSOT admission surface for the finalize handoff: content identity
	// (one overlay per rendered asset — the 2026-09-30 duplicate-image
	// incident class) and the FRAME windows the worker's replace decoder will
	// enforce. Both are fail-closed: a payload that violates either never
	// reaches the remote and can never burn a worker attempt on a guaranteed
	// rejection.
	admittedContent := make(map[string]string)
	admittedFrames := make(map[string]finalJobFrameWindow)
	var manifestWire map[string]any
	manifestItems := make(map[string]struct{})
	if result.OverlayRender.Manifest != nil {
		for _, entry := range result.OverlayRender.Manifest.Entries {
			manifestItems[strings.TrimSpace(entry.ItemID)] = struct{}{}
		}
		var err error
		manifestWire, err = finalJobManifestWire(ctx, result, consumed, baseFrameCount, fpsNum, fpsDen, admittedContent, admittedFrames)
		if err != nil {
			return nil, nil, err
		}
	}
	rows, err := finalJobDedicatedOverlayRows(ctx, result, manifestItems, baseFrameCount, fpsNum, fpsDen, admittedContent, admittedFrames)
	if err != nil {
		return nil, nil, err
	}
	return rows, manifestWire, nil
}

// finalJobManifestWire projects the certified timeline artifact and its
// manifest onto the replace handoff. Entries keep their rendered placement
// (plan-absolute in_us/out_us, the same segment the composition rendered);
// the worker projects them onto frames with the §3 grid and replaces those
// windows with the SAME windows of the timeline artifact — every entry reads
// from one artifact, so overlapping entries are idempotent and the baked
// z-order (track) is authoritative.
//
// Two classes of entries are deliberately NOT emitted, and both are safe:
// their frames are simply never placed. Consumed phrase overlays become the
// scene clip itself (finalJobPhraseOverlayClip, referenced by timeline window
// instead of a standalone Drive clip); image-style cards on intermediate clip
// scenes are dropped for the same reason the per-item handoff dropped them —
// mode=replace would cover the source clip that must stay visible.
func finalJobManifestWire(ctx context.Context, result *GenerateResult, consumed map[string]struct{}, baseFrameCount int64, fpsNum, fpsDen int, admittedContent map[string]string, admittedFrames map[string]finalJobFrameWindow) (map[string]any, error) {
	manifest := result.OverlayRender.Manifest
	if manifest == nil {
		return nil, fmt.Errorf("final_job overlays are not on the manifest wire (missing overlay_manifest)")
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("final_job overlay manifest: %w", err)
	}
	artifact := result.OverlayRender.Artifact
	if artifact == nil && len(result.OverlayRender.Batches) == 1 && result.OverlayRender.Batches[0].Artifact != nil {
		artifact = result.OverlayRender.Batches[0].Artifact
	}
	if artifact == nil {
		return nil, fmt.Errorf("final_job overlay timeline has no published render artifact")
	}
	sha := strings.TrimSpace(artifact.SHA256)
	assetRef, refErr := finalJobRenderArtifactRef(ctx, artifact)
	if refErr != nil || len(sha) != 64 {
		return nil, fmt.Errorf("final_job overlay timeline is not a certified downloadable artifact: %w", refErr)
	}
	if prior, exists := admittedContent[sha]; exists {
		return nil, fmt.Errorf("final_job overlays %q and the overlay timeline render the same artifact %s; one image one overlay", prior, sha)
	}
	admittedContent[sha] = "overlay_timeline"

	itemsByID := make(map[string]capoverlay.OverlayItem, len(result.OverlayPlan.Items))
	for _, item := range result.OverlayPlan.Items {
		itemsByID[strings.TrimSpace(item.ID)] = item
	}
	clipWindows := finalJobIntermediateClipWindows(result)
	frameDenominator := int64(1_000_000 * fpsDen)
	entries := make([]any, 0, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		item, exists := itemsByID[strings.TrimSpace(entry.ItemID)]
		if !exists {
			return nil, fmt.Errorf("final_job overlay manifest references unknown overlay %q", entry.ItemID)
		}
		startFrame := (entry.InUS*int64(fpsNum) + frameDenominator - 1) / frameDenominator
		endFrame := (entry.OutUS*int64(fpsNum) + frameDenominator - 1) / frameDenominator
		if endFrame <= startFrame {
			return nil, fmt.Errorf("final_job overlay %q has an empty frame interval", entry.ItemID)
		}
		if baseFrameCount > 0 {
			if startFrame >= baseFrameCount {
				return nil, fmt.Errorf("final_job overlay %q starts at frame %d after the certified base ends at frame %d", entry.ItemID, startFrame, baseFrameCount)
			}
			if endFrame > baseFrameCount {
				endFrame = baseFrameCount
			}
		}
		if artifact.FrameCount > 0 && endFrame-startFrame > int64(artifact.FrameCount) {
			return nil, fmt.Errorf("final_job overlay %q has %d certified frames for a %d-frame replace window", entry.ItemID, artifact.FrameCount, endFrame-startFrame)
		}
		// The artifact owns these frames whether or not the entry is emitted:
		// seed the occupancy map for every entry so dedicated rows can never
		// schedule themselves onto composition frames.
		admittedFrames[entry.ItemID] = finalJobFrameWindow{start: startFrame, end: endFrame, sceneID: item.SceneID}
		if _, isConsumed := consumed[strings.TrimSpace(entry.ItemID)]; isConsumed {
			continue
		}
		if finalJobOverlayIsImageStyle(item) {
			if _, isClip := clipWindows[item.SceneID]; isClip {
				continue
			}
		}
		if entry.Placement != nil {
			return nil, fmt.Errorf("final_job overlay %q requires unsupported spatial placement", entry.ItemID)
		}
		wireEntry := map[string]any{
			"item_id": entry.ItemID, "track": entry.Track,
			"in_us": entry.InUS, "out_us": entry.OutUS,
			"content_hash": entry.ContentHash,
		}
		if entry.ArtifactInUS != nil && entry.ArtifactOutUS != nil {
			wireEntry["artifact_in_us"] = *entry.ArtifactInUS
			wireEntry["artifact_out_us"] = *entry.ArtifactOutUS
		}
		entries = append(entries, wireEntry)
	}
	asset := map[string]any{
		"asset_id": assetRef["asset_id"], "sha256": sha,
		"size_bytes": artifact.SizeBytes, "frame_count": artifact.FrameCount,
		"duration_ms": (artifact.DurationUS + 999) / 1000,
		"mode":        "replace", "audio_mode": "preserve_final_audio",
	}
	for key, value := range assetRef {
		asset[key] = value
	}
	return map[string]any{
		"schema_version":  manifest.SchemaVersion,
		"plan_id":         manifest.PlanID,
		"video_id":        manifest.VideoID,
		"timeline_sha256": strings.ToLower(strings.TrimSpace(manifest.TimelineSHA256)),
		"asset":           asset,
		"entries":         entries,
	}, nil
}

// finalJobDedicatedOverlayRows builds the independent replace rows for the
// dedicated-path items (maps, chain): their renderers publish one certified
// artifact per item and the worker keeps replacing those windows one clip at
// a time. Every other plan item must be covered by the manifest — a regular
// item without a manifest entry is a broken handoff, not a fallback.
func finalJobDedicatedOverlayRows(ctx context.Context, result *GenerateResult, manifestItems map[string]struct{}, baseFrameCount int64, fpsNum, fpsDen int, admittedContent map[string]string, admittedFrames map[string]finalJobFrameWindow) ([]any, error) {
	plan := result.OverlayPlan
	artifacts := make(map[string]RenderArtifact, len(result.OverlayRender.Items))
	for _, rendered := range result.OverlayRender.Items {
		if rendered.Artifact != nil && strings.TrimSpace(rendered.ItemID) != "" {
			artifacts[strings.TrimSpace(rendered.ItemID)] = *rendered.Artifact
		}
	}
	rows := make([]any, 0, len(plan.Items))
	frameDenominator := int64(1_000_000 * fpsDen)
	for index, item := range plan.Items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return nil, fmt.Errorf("final_job overlay plan has an item without an ID")
		}
		if _, covered := manifestItems[id]; covered {
			continue
		}
		artifact, ok := artifacts[id]
		if !ok {
			return nil, fmt.Errorf("final_job overlay %q has no manifest entry and no dedicated render artifact", id)
		}
		sha := strings.TrimSpace(artifact.SHA256)
		assetRef, refErr := finalJobRenderArtifactRef(ctx, &artifact)
		if refErr != nil || len(sha) != 64 {
			return nil, fmt.Errorf("final_job overlay %q is not a certified downloadable artifact: %w", id, refErr)
		}
		startUS, endUS := item.StartUSValue(), item.EndUSValue()
		startFrame := (startUS*int64(fpsNum) + frameDenominator - 1) / frameDenominator
		endFrame := (endUS*int64(fpsNum) + frameDenominator - 1) / frameDenominator
		if artifact.FrameCount > 0 && endFrame-startFrame > int64(artifact.FrameCount) {
			endFrame = startFrame + int64(artifact.FrameCount)
		}
		if baseFrameCount > 0 {
			if startFrame >= baseFrameCount {
				return nil, fmt.Errorf("final_job overlay %q starts at frame %d after the certified base ends at frame %d", id, startFrame, baseFrameCount)
			}
			if endFrame > baseFrameCount {
				endFrame = baseFrameCount
			}
		}
		if endFrame <= startFrame {
			return nil, fmt.Errorf("final_job overlay %q has an empty frame interval", id)
		}
		// Gate 1 — content identity: the same rendered artifact admitted twice
		// would render the same visual again.
		if prior, exists := admittedContent[sha]; exists {
			return nil, fmt.Errorf("final_job overlays %q and %q render the same artifact %s; one image one overlay", prior, id, sha)
		}
		admittedContent[sha] = id
		// Gate 2 — frame window: a dedicated row replaces its window with its
		// own clip, so it must never share a frame with another dedicated row
		// OR with composition frames (those belong to the timeline artifact).
		var conflictID string
		for otherID, prior := range admittedFrames {
			if startFrame < prior.end && prior.start < endFrame {
				conflictID = otherID
				break
			}
		}
		if conflictID != "" {
			prior := admittedFrames[conflictID]
			if prior.sceneID != item.SceneID {
				return nil, fmt.Errorf("final_job overlays %q and %q overlap across scenes (%q, %q); cannot preserve both in mode=replace", id, conflictID, item.SceneID, prior.sceneID)
			}
			scheduledStart, scheduledEnd, scheduleErr := scheduleFinalJobOverlayFrames(result, item, startFrame, endFrame, fpsNum, fpsDen, admittedFrames)
			if scheduleErr != nil {
				return nil, scheduleErr
			}
			startFrame, endFrame = scheduledStart, scheduledEnd
		}
		admittedFrames[id] = finalJobFrameWindow{start: startFrame, end: endFrame, sceneID: item.SceneID}
		row := map[string]any{
			"id": id, "asset_id": assetRef["asset_id"],
			"sha256": sha, "size_bytes": artifact.SizeBytes,
			"start_frame": startFrame, "end_frame": endFrame, "frame_count": endFrame - startFrame,
			"mode": "replace", "z_index": index + 1, "audio_mode": "preserve_final_audio",
		}
		for key, value := range assetRef {
			row[key] = value
		}
		rows = append(rows, row)
	}
	return rows, nil
}
