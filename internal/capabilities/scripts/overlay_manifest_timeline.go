package scriptgeneration

// overlay_manifest_timeline.go is path (b) of the P5 A/B measurement
// (docs/operations/TICKET-MANIFEST-IDENTITY.md): ONE certified artifact per
// timeline plus the manifest — item_id → (track, in_us, out_us, content-hash)
// — instead of one intermediate MP4 per item. The composition is rendered
// once (single compositing, single encode) and the manifest digest is its
// content address.
//
// Retry contract (gate 2) at this path: the manifest digest aggregates every
// voice's identity (item_id + content-hash + placement), so the same manifest
// resolves to the same composition cache row — no repeated work, and the
// cache's idempotent Store never produces a second artifact. The per-voice
// clip ledger stays on the per-item path (classifyManifestEntries).
//
// The path is behind an explicit opt-in flag (default OFF): the per-item path
// remains the production default until the A/B parity gate is certified, and
// the flag is the rollback lever. Plans the composition cannot cover fail
// closed to the per-item path.

import (
	"context"
	"fmt"
	"os"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// OverlayManifestTimelineEnv enables the manifest+timeline render path for
// eligible plans. Default is OFF (per-item path); set to 1/true/yes/on to
// measure or adopt path (b).
const OverlayManifestTimelineEnv = "VELOX_SCRIPTS_OVERLAY_MANIFEST_TIMELINE"

// overlayManifestTimelineEnabled reports the explicit opt-in state.
func overlayManifestTimelineEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(OverlayManifestTimelineEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// manifestTimelineEligible reports whether a plan may render as ONE manifest
// timeline artifact. The composition shares a single frame timeline, so it
// must obey the fused-window admission rules at plan scale, and final-job
// overlays keep their Drive-per-item handoff until the manifest contract is
// renegotiated with the final worker. The second return value is the refusal
// reason for operators and tests.
func manifestTimelineEligible(plan capoverlay.OverlayPlan) (bool, string) {
	if plan.RequireDriveBeforeReturn {
		return false, "final-job overlays keep the Drive-per-item handoff until the manifest contract is renegotiated"
	}
	if plan.Source != nil {
		return false, "source-bound composites stay on the per-item path"
	}
	candidates := overlayRenderableCandidates(plan)
	if len(candidates) < 2 {
		return false, "manifest timeline needs at least two overlay items"
	}
	scene := ""
	captions := 0
	for _, candidate := range candidates {
		item := candidate.source
		if item.Map != nil || capoverlay.IsChainItemKind(item.Kind) {
			return false, "maps and chain items keep their dedicated render paths"
		}
		if id := strings.TrimSpace(item.SceneID); id != "" {
			if scene == "" {
				scene = id
			} else if id != scene {
				return false, "manifest timeline is scene-local"
			}
		}
		if overlayItemMayCompileLinkedCaption(item) {
			captions++
		}
	}
	if captions > 1 {
		return false, "the fitted-caption gate admits one caption-bearing member per shared frame timeline"
	}
	return true, ""
}

// overlayManifestCompositionCacheKey is the content address of the timeline
// artifact: the manifest digest (every voice's identity and placement, in
// canonical order) plus the rendered child plan's context and the deployment
// epoch. Two runs that build the same manifest compose the same artifact and
// must share one cache row and one stored artifact.
func overlayManifestCompositionCacheKey(child capoverlay.OverlayPlan, manifest capoverlay.OverlayManifest, epoch string) (OverlayRenderCacheKey, bool) {
	renderKey := manifest.Digest()
	if len(renderKey) != 64 || !isLowerHex(renderKey) {
		return OverlayRenderCacheKey{}, false
	}
	contextHash := overlayPlanRenderContextDigest(child)
	if contextHash == "" {
		return OverlayRenderCacheKey{}, false
	}
	key := OverlayRenderCacheKey{RenderKey: renderKey, PlanContext: contextHash, Epoch: strings.TrimSpace(epoch)}
	if key.Epoch == "" {
		key.Epoch = OverlayRenderCacheEpoch()
	}
	if err := key.Validate(); err != nil {
		return OverlayRenderCacheKey{}, false
	}
	return key, true
}

// enqueueOverlayManifestTimeline renders one certified artifact for the whole
// plan timeline and returns it with its manifest. A retry of the same manifest
// reuses the certified artifact and never renders nor stores again.
func (e *QueueRenderEnqueuer) enqueueOverlayManifestTimeline(ctx context.Context, plan capoverlay.OverlayPlan) (RenderReference, error) {
	works, err := buildOverlayManifestEntries(plan, e.renderCacheEpochValue())
	if err != nil {
		return RenderReference{}, err
	}
	manifest := capoverlay.OverlayManifest{
		SchemaVersion: capoverlay.SchemaVersionManifest,
		PlanID:        strings.TrimSpace(plan.PlanID),
		VideoID:       strings.TrimSpace(plan.VideoID),
		Entries:       make([]capoverlay.OverlayManifestEntry, 0, len(works)),
	}
	candidates := make([]overlayItemCandidate, 0, len(works))
	for _, work := range works {
		manifest.Entries = append(manifest.Entries, work.Entry)
		candidates = append(candidates, overlayItemCandidate{index: work.Index, source: work.Item})
	}
	if err := manifest.Validate(); err != nil {
		return RenderReference{}, err
	}
	_, endUS := overlayCandidateRenderWindow(plan, candidates[0])
	for _, candidate := range candidates[1:] {
		_, itemEnd := overlayCandidateRenderWindow(plan, candidate)
		if itemEnd > endUS {
			endUS = itemEnd
		}
	}
	// The manifest projects plan-absolute in_us directly onto the artifact's
	// frames — start_frame = ceil(in_us · fps_num / (1e6 · fps_den)) with NO
	// origin offset (OVERLAY-MANIFEST-CONTRACT.md §3, the same grid as
	// finalJobOverlayAssetsExcluding) — so the timeline artifact MUST begin at
	// plan t=0. Anchoring the fused window at the first render window (the
	// batch-path convention) shifted every entry by that offset: on the live
	// A/B (2026-10-24) entries landed 6 frames early on a 30 fps plan while
	// their manifest in_us stayed plan-absolute.
	work := overlayBatchWork{
		sceneID:    strings.TrimSpace(candidates[0].source.SceneID),
		startUS:    0,
		endUS:      overlayBatchWireEndUS(0, endUS),
		candidates: candidates,
	}
	work.id = overlayBatchIdentity(plan, work)
	child, err := fusedOverlayBatchPlan(plan, work)
	if err != nil {
		return RenderReference{}, err
	}
	key, ok := overlayManifestCompositionCacheKey(child, manifest, e.renderCacheEpochValue())
	if !ok {
		return RenderReference{}, fmt.Errorf("overlay manifest timeline has no content-addressed identity")
	}
	if artifact, hit, _ := e.lookupOverlayRenderCacheTimed(ctx, key); hit {
		return assembleManifestTimelineReference(work.id, "completed", artifact, work, manifest), nil
	}
	metadata := &overlayItemPublicationMetadata{
		JobID:            textutil.FirstNonEmpty(plan.DriveJobID, plan.PlanID),
		ResultJobID:      textutil.FirstNonEmpty(plan.ResultJobID, plan.DriveJobID),
		ItemID:           "manifest:" + manifest.Digest(),
		BatchID:          manifest.Digest(),
		ItemKind:         "overlay_manifest_timeline",
		SourceStartUS:    work.startUS,
		SourceEndUS:      work.endUS,
		TargetDurationUS: work.endUS - work.startUS,
		BatchEndUS:       work.endUS,
	}
	for _, candidate := range candidates {
		metadata.BatchItemIDs = append(metadata.BatchItemIDs, candidate.source.ID)
	}
	ref, err := e.enqueueChrononPlan(ctx, child, metadata)
	if err != nil {
		return RenderReference{}, fmt.Errorf("render overlay manifest timeline: %w", err)
	}
	if ref.Artifact == nil {
		return RenderReference{}, fmt.Errorf("overlay manifest timeline completed without a certified artifact")
	}
	e.storeOverlayRenderCacheTimed(ctx, key, ref.Artifact)
	return assembleManifestTimelineReference(ref.JobID, ref.Status, ref.Artifact, work, manifest), nil
}

// assembleManifestTimelineReference binds one certified artifact to the
// manifest and the per-voice identity rows. The manifest digest is the
// receipt: entries keep their editorial identity and all point at the single
// timeline artifact.
func assembleManifestTimelineReference(jobID, status string, artifact *RenderArtifact, work overlayBatchWork, manifest capoverlay.OverlayManifest) RenderReference {
	manifest.TimelineSHA256 = strings.ToLower(strings.TrimSpace(artifact.SHA256))
	batchID := manifest.Digest()
	items := make([]OverlayItemRenderReference, 0, len(work.candidates))
	itemIDs := make([]string, 0, len(work.candidates))
	for _, candidate := range work.candidates {
		items = append(items, OverlayItemRenderReference{
			ItemID: candidate.source.ID, JobID: jobID, Status: status, BatchID: batchID,
		})
		itemIDs = append(itemIDs, candidate.source.ID)
	}
	return RenderReference{
		JobID:    jobID,
		Status:   status,
		Artifact: artifact,
		Items:    items,
		Batches: []OverlayBatchRenderReference{{
			BatchID: batchID, SceneID: work.sceneID, ItemIDs: itemIDs,
			StartUS: work.startUS, EndUS: work.endUS,
			JobID: jobID, Status: status, Artifact: artifact,
		}},
		Manifest: &manifest,
	}
}
