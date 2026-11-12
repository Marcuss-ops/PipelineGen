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
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"sort"
	"strings"
)

// applyFinalJobOverlayWindowPolicy applies the final-job placement policy to a
// plan the caller already OWNS (see FrozenFinalJobOverlayPlan, which applies it
// to a copy). It must never be called on the durable result's plan directly:
// the render path normalizing its own snapshot while the checkpoint kept the
// un-normalized plan is exactly the divergence the binding digest closes.
//
// The policy runs BEFORE rendering because the pure-manifest wire freezes
// placement at render time (entries document the artifact's frames): the
// composition, the manifest and the payload then all agree on where every item
// sits. Idempotent — the moved window is computed absolutely from the clip
// scene's certified start — so resumed runs can re-apply it safely.
//
// The one placement rule that survives: a phrase callout on an intermediate
// clip scene (original clip audio + generated narration) must sit JUST BEFORE
// the clip, never over it — replace-mode frames would cover the source video
// that has to stay visible. Image-style cards on those scenes are dropped at
// handoff time instead (their frames are simply never placed).
func applyFinalJobOverlayWindowPolicy(plan *capmodel.OverlayPlan, result *GenerateResult) error {
	if plan == nil {
		return nil
	}
	// Final artifacts r79/r82 showed fully white frames for text_3d_* phrase
	// overlays: the opaque white canvas replaced the stock picture while the
	// phrase itself was not visible. Normalize every 3D phrase motion here, at
	// the Producer boundary, before the render plan is sealed.
	for index := range plan.Items {
		item := &plan.Items[index]
		if item.Kind == "text_phrase" && strings.HasPrefix(item.MotionID, "text_3d_") {
			item.PresetID = "static_text_smoke"
			item.MotionID = ""
			item.MotionParams = nil
			item.RenderKey = ""
		}
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

// certifyFinalJobOverlayBatch enforces the deliverable certificate of ONE
// per-scene batch. It is the single authority for that decision: the final-job
// handoff calls it per batch, and the split render calls it before accepting a
// batch set, so the two can never drift.
//
// A batch is deliverable when its bytes are certified and its manifest names
// them:
//   - the manifest is a valid overlay-manifest document (one content one
//     overlay, unique item ids, canonical timeline digest), and
//   - the artifact carries a canonical SHA-256 and a positive size, and
//   - when the manifest declares a timeline digest it names THIS artifact,
//     because the artifact's bytes ARE the certified timeline, and
//   - when the artifact reports a duration it covers every placement — read
//     at the entry's ARTIFACT window (`artifact_in_us/artifact_out_us`, §3
//     packed timeline), because on a packed canvas the plan receipt is a
//     plan-absolute time that legitimately sits far past the artifact's own
//     length. Comparing the plan receipt against the artifact duration is what
//     rejected the r69 per-scene batch ("places ... at 28020500us but the
//     artifact lasts 3000000us" on a 3 s packed scene canvas whose frames the
//     entry addresses at 1275000us..3012500us), and
//   - the frame count, when reported, is a count — `-1` is the wire's
//     "unknown" for a packed scene composite whose frames are only addressable
//     through the manifest windows. Requiring a positive frame count rejected
//     exactly that artifact shape at r63, although the assembler places it by
//     in_us/out_us and never reads a frame count.
//
// One frame of overhang is not a defect (see finalJobArtifactWindowTolerance).
func certifyFinalJobOverlayBatch(batch OverlayBatchRenderReference, fpsNum, fpsDen int) error {
	if batch.Manifest == nil || batch.Artifact == nil {
		return fmt.Errorf("final_job overlay batch %q lacks a certified manifest or artifact", batch.BatchID)
	}
	sha := strings.TrimSpace(batch.Artifact.SHA256)
	if !digest.IsCanonicalSHA256(sha) || batch.Artifact.SizeBytes <= 0 {
		return fmt.Errorf("final_job overlay batch %q is not a certified artifact (sha=%q size=%d)", batch.BatchID, sha, batch.Artifact.SizeBytes)
	}
	if err := batch.Manifest.Validate(); err != nil {
		return fmt.Errorf("final_job overlay batch %q: %w", batch.BatchID, err)
	}
	if timelineSHA := strings.TrimSpace(batch.Manifest.TimelineSHA256); timelineSHA != "" && !strings.EqualFold(timelineSHA, sha) {
		return fmt.Errorf("final_job overlay batch %q certifies %s but its manifest names %s", batch.BatchID, sha, timelineSHA)
	}
	if batch.Artifact.FrameCount < -1 {
		return fmt.Errorf("final_job overlay batch %q reports an impossible frame count %d", batch.BatchID, batch.Artifact.FrameCount)
	}
	if batch.Artifact.DurationUS < 0 {
		return fmt.Errorf("final_job overlay batch %q reports a negative duration %dus", batch.BatchID, batch.Artifact.DurationUS)
	}
	if batch.Artifact.DurationUS > 0 {
		// A manifest window is rounded UP to a frame boundary while the
		// artifact's duration is a frame COUNT, so the last placement can end
		// up to one frame past it: r70 asked for 3,012,500us on a 3,000,000us
		// packed canvas (12,5 ms at 24 fps) and the bytes to serve it exist.
		// Beyond one frame the window names frames the artifact cannot hold.
		tolerance := finalJobArtifactWindowTolerance(fpsNum, fpsDen)
		for _, entry := range batch.Manifest.Entries {
			artifactIn, artifactOut := manifestEntryArtifactWindow(entry)
			if artifactIn < 0 {
				return fmt.Errorf("final_job overlay batch %q places %q at a negative artifact time %dus", batch.BatchID, entry.ItemID, artifactIn)
			}
			if artifactOut > batch.Artifact.DurationUS+tolerance {
				return fmt.Errorf("final_job overlay batch %q places %q at %dus on the artifact but the artifact lasts %dus", batch.BatchID, entry.ItemID, artifactOut, batch.Artifact.DurationUS)
			}
		}
	}
	return nil
}

// manifestEntryArtifactWindow is the entry's span on the CERTIFIED artifact.
// A packed entry (artifact_in_us/artifact_out_us set, §3 packed timeline)
// addresses its frames there; an unpacked artifact is the plan canvas anchored
// at 0, so the plan window IS the artifact window (§3.1).
func manifestEntryArtifactWindow(entry capmodel.OverlayManifestEntry) (int64, int64) {
	if entry.ArtifactInUS != nil && entry.ArtifactOutUS != nil {
		return *entry.ArtifactInUS, *entry.ArtifactOutUS
	}
	return entry.InUS, entry.OutUS
}

// certifiableFinalJobOverlayBatches is the gate the per-scene SPLIT must pass
// before its artifacts are accepted. Only final jobs consume the batch
// certificate set; every other run keeps the reference untouched.
func certifiableFinalJobOverlayBatches(finalJob bool, ref RenderReference, fpsNum, fpsDen int) error {
	if !finalJob || len(ref.Batches) == 0 {
		return nil
	}
	for _, batch := range ref.Batches {
		if err := certifyFinalJobOverlayBatch(batch, fpsNum, fpsDen); err != nil {
			return err
		}
	}
	return nil
}

// finalJobOverlayHandoff returns the replace rows for dedicated-path items
// and the overlay_manifest wire document for the composition. Either side
// may be empty (a plan with no maps/chain emits no rows; a plan with only
// maps/chain emits no manifest), but never both when semantic overlays exist.
func finalJobOverlayHandoff(ctx context.Context, result *GenerateResult, consumed map[string]struct{}, imageRequested bool) ([]any, map[string]any, error) {
	if result == nil || result.OverlayPlan == nil || len(result.OverlayPlan.Items) == 0 {
		return []any{}, nil, nil
	}
	if result.OverlayRender == nil || (result.OverlayRender.Manifest == nil && len(result.OverlayRender.Items) == 0) {
		return nil, nil, fmt.Errorf("final_job has semantic overlays but no published overlay render artifacts")
	}
	// Identity gate, upstream of every wire decision below: the composition the
	// handoff is about to describe must be the render of THIS sealed plan. Until
	// the digest was bound this check did not exist, so a retry could hand a
	// stale artifact to the compositor and only the unknown-ID check — much
	// later, inside finalJobManifestWire — noticed, by which point the payload
	// was already semantically wrong.
	if strings.TrimSpace(result.OverlayRender.PlanDigest) != "" {
		if err := validateOverlayPlanRenderDigest(result.OverlayPlan, result.OverlayRender); err != nil {
			return nil, nil, fmt.Errorf("final_job overlay render does not bind to the sealed plan: %w", err)
		}
	}
	// Image preflight (the r10 class): promised entity-image overlays must be
	// backed by a verified content address BEFORE a worker attempt exists.
	clipScenes := make(map[string]struct{})
	for sceneID := range finalJobIntermediateClipWindows(result) {
		clipScenes[sceneID] = struct{}{}
	}
	if err := finalJobImagePreflight(result.OverlayPlan.Items, clipScenes, imageRequested); err != nil {
		return nil, nil, err
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
	} else if len(result.OverlayRender.Batches) > 0 {
		batchWires := make([]any, 0, len(result.OverlayRender.Batches))
		owners := make(map[string]string)
		contentOwners := make(map[string]string)
		batchFrames := make(map[string]finalJobFrameWindow)
		for _, batch := range result.OverlayRender.Batches {
			fpsNum, fpsDen := 0, 0
			if result.OverlayPlan != nil {
				fpsNum, fpsDen = result.OverlayPlan.FPSNum, result.OverlayPlan.FPSDen
			}
			if err := certifyFinalJobOverlayBatch(batch, fpsNum, fpsDen); err != nil {
				return nil, nil, err
			}
			for _, entry := range batch.Manifest.Entries {
				id := strings.TrimSpace(entry.ItemID)
				if prior, exists := owners[id]; exists {
					return nil, nil, fmt.Errorf("final_job overlay %q belongs to batches %q and %q", id, prior, batch.BatchID)
				}
				if prior, exists := contentOwners[entry.ContentHash]; exists {
					return nil, nil, fmt.Errorf("final_job overlays %q and %q share content; one content one overlay", prior, id)
				}
				owners[id], contentOwners[entry.ContentHash] = batch.BatchID, id
				manifestItems[id] = struct{}{}
			}
			for _, entry := range batch.Manifest.Entries {
				start := entry.InUS * int64(fpsNum) / (1_000_000 * int64(fpsDen))
				end := (entry.OutUS*int64(fpsNum) + 1_000_000*int64(fpsDen) - 1) / (1_000_000 * int64(fpsDen))
				for priorID, prior := range batchFrames {
					if start < prior.end && prior.start < end && owners[priorID] != batch.BatchID {
						return nil, nil, fmt.Errorf("final_job overlay %q overlaps %q across artifacts", entry.ItemID, priorID)
					}
				}
				batchFrames[entry.ItemID] = finalJobFrameWindow{start: start, end: end}
			}
			copyResult := *result
			copyResult.OverlayRender = &RenderReference{Manifest: batch.Manifest, Artifact: batch.Artifact}
			wire, err := finalJobManifestWire(ctx, &copyResult, consumed, baseFrameCount, fpsNum, fpsDen, admittedContent, admittedFrames)
			if err != nil {
				return nil, nil, err
			}
			batchWires = append(batchWires, wire)
		}
		manifestWire = map[string]any{"schema_version": "pipelinegen.overlay-manifest-set.v1", "manifests": batchWires}
	}
	rows, err := finalJobDedicatedOverlayRows(ctx, result, consumed, manifestItems, baseFrameCount, fpsNum, fpsDen, admittedContent, admittedFrames)
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
	// The asset facts are a contract, not decoration: a multi-artifact
	// manifest set is refused by the worker unless frame_count is a positive
	// count of the artifact it names. A packed canvas reports frame_count=-1
	// in the render receipt (its frames are only addressable through the
	// manifest windows), so the handoff derives the honest grid count from the
	// certified duration — the same round(duration·fps) the worker probes on
	// that very file.
	assetFrames := int64(artifact.FrameCount)
	if assetFrames <= 0 && artifact.DurationUS > 0 && fpsNum > 0 && fpsDen > 0 {
		assetFrames = (artifact.DurationUS*int64(fpsNum) + 500_000*int64(fpsDen)) / (1_000_000 * int64(fpsDen))
	}
	if prior, exists := admittedContent[sha]; exists {
		return nil, fmt.Errorf("final_job overlays %q and the overlay timeline render the same artifact %s; one image one overlay", prior, sha)
	}
	admittedContent[sha] = "overlay_timeline"

	itemsByID := make(map[string]capmodel.OverlayItem, len(result.OverlayPlan.Items))
	for _, item := range result.OverlayPlan.Items {
		itemsByID[strings.TrimSpace(item.ID)] = item
	}
	clipWindows := finalJobIntermediateClipWindows(result)
	frameDenominator := int64(1_000_000 * fpsDen)
	entries := make([]any, 0, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		item, exists := itemsByID[strings.TrimSpace(entry.ItemID)]
		if !exists {
			// Review IDs include the selected motion, which may be reselected
			// after a cached/parallel render has frozen its manifest. Reconcile
			// year cards by their stable semantic value and time window; require
			// exactly one match so a stale manifest can never bind ambiguously.
			item, exists = finalJobManifestYearItem(entry, result.OverlayPlan.Items)
			if !exists {
				known := make([]string, 0, len(itemsByID))
				for id := range itemsByID {
					known = append(known, id)
				}
				sort.Strings(known)
				return nil, fmt.Errorf("final_job overlay manifest references unknown overlay %q (plan item ids: %s)", entry.ItemID, strings.Join(known, ","))
			}
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
		// The artifact must own the frames the entry asks for. A packed entry
		// reads its frames at its artifact window, so THAT is the span the
		// artifact's frame count bounds — its plan receipt may legitimately sit
		// far later on the timeline (the r69 batch: a 3 s canvas carrying a
		// 28,020,500us plan receipt).
		windowFrames := endFrame - startFrame
		if artifactInUS, artifactOutUS, packed := manifestEntryArtifactPositions(entry); packed {
			windowFrames = (artifactOutUS*int64(fpsNum)+frameDenominator-1)/frameDenominator - (artifactInUS * int64(fpsNum) / frameDenominator)
		}
		// One frame of slack, for the same reason the artifact certificate has
		// it: the window is ceil'd on both edges while the certified count is a
		// rounded frame count, so the same span can legitimately measure one
		// frame wider than the artifact's frame budget (r71: 85 measured against
		// 84 certified on a 24 fps grid). Two frames past the budget still name
		// frames the artifact does not contain.
		if assetFrames > 0 && windowFrames > assetFrames+1 {
			return nil, fmt.Errorf("final_job overlay %q has %d certified frames for a %d-frame replace window", entry.ItemID, assetFrames, windowFrames)
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
	mode := manifest.OverlayMode()
	asset := map[string]any{
		"asset_id": assetRef["asset_id"], "sha256": sha,
		"size_bytes": artifact.SizeBytes, "frame_count": assetFrames,
		"duration_ms": (artifact.DurationUS + 999) / 1000,
		"mode":        mode, "audio_mode": "preserve_final_audio",
		"codec": artifact.Codec, "container": artifact.Container,
		"pixel_format": artifact.PixelFormat, "audio_streams": artifact.AudioStreams,
		"width": artifact.Width, "height": artifact.Height,
		"fps_num": artifact.FPSNum, "fps_den": artifact.FPSDen,
	}
	stream := finalJobOverlayStreamWire(manifest, artifact)
	if stream != nil {
		asset["stream"] = stream
	}
	for key, value := range assetRef {
		asset[key] = value
	}
	// The ref carries the render receipt's facts, including the -1 a packed
	// canvas reports for frame_count. The handoff keeps the DERIVED count:
	// the worker both validates (a set needs a positive count) and projects
	// with it, and the derived grid is the one it probes on the same file.
	if assetFrames > 0 {
		asset["frame_count"] = assetFrames
	}
	wire := map[string]any{
		"schema_version":  manifest.SchemaVersion,
		"plan_id":         manifest.PlanID,
		"video_id":        manifest.VideoID,
		"mode":            mode,
		"timeline_sha256": strings.ToLower(strings.TrimSpace(manifest.TimelineSHA256)),
		"asset":           asset,
		"entries":         entries,
	}
	if stream != nil {
		wire["stream"] = stream
	}
	return wire, nil
}

// finalJobOverlayStreamWire transports the certified video-stream identity
// without inventing it. A V3 artifact was finalize-validated against the
// canonical timebase and avcC, so its canonical facts are safe to relay. Any
// other profile is a legacy source: it keeps its profile id but makes no
// canonical bitstream claim, and the fragment producer must normalize it.
func finalJobOverlayStreamWire(manifest *capmodel.OverlayManifest, artifact *RenderArtifact) map[string]any {
	if artifact == nil {
		return nil
	}
	profileID := strings.TrimSpace(artifact.ProfileID)
	if profileID == "" {
		return nil
	}
	if manifest != nil && manifest.Stream != nil {
		declared := manifest.Stream
		// A declared stream is a CLAIM, not proof. Relay the canonical facts
		// only when the manifest's claim matches both the artifact's lane and
		// every canonical value; otherwise downgrade to copy_ready=false and
		// do not transport an extradata/timebase that would invite a copy.
		if strings.TrimSpace(declared.ProfileID) == profileID &&
			declared.ProfileID == kernelmedia.OverlayVideoOnlyAssemblyProfileID &&
			declared.ExtradataSHA256 == kernelmedia.CanonicalVideoExtradataSHA256 &&
			declared.TimeBaseNum == 1 && declared.TimeBaseDen == 90000 &&
			declared.EncoderRevision == kernelmedia.CanonicalVideoProfileRevision {
			return map[string]any{
				"profile_id": declared.ProfileID, "extradata_sha256": declared.ExtradataSHA256,
				"time_base_num": declared.TimeBaseNum, "time_base_den": declared.TimeBaseDen,
				"encoder_revision": declared.EncoderRevision, "copy_ready": true,
			}
		}
		return map[string]any{"profile_id": profileID, "copy_ready": false}
	}
	if profileID != kernelmedia.OverlayVideoOnlyAssemblyProfileID {
		return map[string]any{"profile_id": profileID, "copy_ready": false}
	}
	return map[string]any{
		"profile_id": profileID, "extradata_sha256": kernelmedia.CanonicalVideoExtradataSHA256,
		"time_base_num": 1, "time_base_den": 90000,
		"encoder_revision": kernelmedia.CanonicalVideoProfileRevision, "copy_ready": true,
	}
}

// finalJobDedicatedOverlayRows builds the independent replace rows for the
// dedicated-path items (maps, chain): their renderers publish one certified
// artifact per item and the worker keeps replacing those windows one clip at
// a time. Every other plan item must be covered by the manifest — a regular
// item without a manifest entry is a broken handoff, not a fallback.
func finalJobDedicatedOverlayRows(ctx context.Context, result *GenerateResult, consumed, manifestItems map[string]struct{}, baseFrameCount int64, fpsNum, fpsDen int, admittedContent map[string]string, admittedFrames map[string]finalJobFrameWindow) ([]any, error) {
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
		if _, isConsumed := consumed[id]; isConsumed {
			continue
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
