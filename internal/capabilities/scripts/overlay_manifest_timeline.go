package scriptgeneration

// overlay_manifest_timeline.go owns the overlay render cutover (P5 path (b),
// docs/operations/OVERLAY-MANIFEST-CONTRACT.md): ONE certified artifact per
// plan timeline plus the manifest — item_id → (track, in_us, out_us,
// content_hash) — instead of one intermediate MP4 per item. The composition is
// rendered once (single compositing, single encode) and the manifest digest is
// its content address. This is now the ONLY render path for ordinary overlay
// items: the per-item path survives exclusively for maps and chain items,
// which keep their dedicated render pipelines (dynamic-map admission and
// asset lifecycle; chain segment rebasing) and are merged into the same
// render reference as independent certified items.
//
// The artifact is anchored at plan t=0 (the contract's frame grid projects
// plan-absolute in_us with NO origin offset), so the plan's Source/Background
// is aligned with plan time inside the composition and the historical
// "source restarts at frame zero" defect of window-anchored batches cannot
// occur. Final-job plans elect this path too: the manifest is the wire
// document the final worker consumes (OVERLAY-MANIFEST-CONTRACT.md §4).
//
// Retry contract (gate 2) at this path: the composition cache key aggregates
// every voice's identity (item_id + content-hash + track/window/placement),
// which is stable across runs, so the same timeline resolves to the same
// composition cache row — no repeated work, and the cache's idempotent Store
// never produces a second artifact. The manifest digest remains the durable
// receipt identity (OverlayBatchRenderReference.BatchID) and the final-worker
// wire document; only the CACHE identity is row-nameable across runs.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// partitionOverlayRenderWork splits a plan's renderable items between the
// manifest timeline composition (everything the single certified artifact
// covers) and the dedicated per-item paths (maps and chain items keep their
// own render pipelines and their own certified artifacts).
func partitionOverlayRenderWork(plan capoverlay.OverlayPlan) (manifest, dedicated []overlayItemCandidate) {
	type captionWindow struct {
		inUS  int64
		outUS int64
	}
	var acceptedCaptions []captionWindow

	for _, candidate := range overlayRenderableCandidates(plan) {
		if candidate.source.Map != nil || capoverlay.IsChainItemKind(candidate.source.Kind) {
			dedicated = append(dedicated, candidate)
			continue
		}
		if overlayItemMayCompileLinkedCaption(candidate.source) {
			startUS, endUS := overlayCandidateRenderWindow(plan, candidate)
			collides := false
			for _, prior := range acceptedCaptions {
				if startUS < prior.outUS && prior.inUS < endUS {
					collides = true
					break
				}
			}
			if collides {
				dedicated = append(dedicated, candidate)
				continue
			}
			acceptedCaptions = append(acceptedCaptions, captionWindow{inUS: startUS, outUS: endUS})
		}
		manifest = append(manifest, candidate)
	}
	return manifest, dedicated
}

// admitManifestCaptionWindows enforces the fitted-caption gate at plan scale.
// The composition shares ONE frame timeline, where RenderingGen's fitted
// entity-caption gate compares every image-linked caption against every other
// and rejects pairs whose fitted canvas bounds overlap while they share a
// frame (golden v21). The planner cannot predict the fitted geometry (fitting
// runs on materialized aspect ratios inside the worker), but the gate's
// shared-frame half is exactly what the planner CAN decide: caption-bearing
// members whose (padded) render windows are pairwise disjoint in µs can never
// share a frame — ceil projection is monotone — so they can never collide,
// with any fitted geometry. Overlapping caption windows are refused here,
// deterministically and before any render.
func admitManifestCaptionWindows(candidates []overlayItemCandidate, plan capoverlay.OverlayPlan) error {
	type captionWindow struct {
		id    string
		inUS  int64
		outUS int64
	}
	var captions []captionWindow
	for _, candidate := range candidates {
		if !overlayItemMayCompileLinkedCaption(candidate.source) {
			continue
		}
		startUS, endUS := overlayCandidateRenderWindow(plan, candidate)
		for _, prior := range captions {
			if startUS < prior.outUS && prior.inUS < endUS {
				return fmt.Errorf("caption-bearing overlays %q and %q share frames on one timeline; render windows must be disjoint to pass the fitted-caption gate", prior.id, candidate.source.ID)
			}
		}
		captions = append(captions, captionWindow{id: candidate.source.ID, inUS: startUS, outUS: endUS})
	}
	return nil
}

// enqueueOverlayManifestTimeline renders one certified artifact for the whole
// plan timeline and returns it with its manifest. A retry of the same manifest
// reuses the certified artifact and never renders nor stores again. Map and
// chain items render through their dedicated per-item paths and merge into
// the same reference.
func (e *QueueRenderEnqueuer) enqueueOverlayManifestTimeline(ctx context.Context, plan capoverlay.OverlayPlan, candidates, dedicated []overlayItemCandidate) (RenderReference, error) {
	if len(candidates) == 0 {
		return RenderReference{}, fmt.Errorf("overlay manifest timeline has no composition candidates")
	}
	if err := admitManifestCaptionWindows(candidates, plan); err != nil {
		return RenderReference{}, err
	}
	works, err := buildManifestEntryWorks(plan, candidates, e.renderCacheEpochValue())
	if err != nil {
		return RenderReference{}, err
	}
	// P0 packed composition (TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE §3.4):
	// when the plan carries no time-aligned background/source layer the
	// artifact renounces the plan's empty spans — the canvas covers only the
	// packed union of the candidates' literal windows, and every entry
	// records where its frames actually live via
	// artifact_in_us/artifact_out_us (§3 packed timeline). The plan-absolute
	// InUS/OutUS receipt stays verbatim in both modes.
	packedSupported := planSupportsPackedComposition(plan)
	packedPlacements := map[string]packedPlacement(nil)
	var packedEnd int64 = -1
	if packedSupported {
		placements, end, placementsErr := computePackedPlacements(plan, candidates)
		if placementsErr != nil {
			return RenderReference{}, fmt.Errorf("overlay manifest timeline packed composition: %w", placementsErr)
		}
		if stampErr := stampPackedPlacements(works, placements); stampErr != nil {
			return RenderReference{}, fmt.Errorf("overlay manifest timeline packed composition: %w", stampErr)
		}
		packedPlacements = placements
		packedEnd = end
	}
	manifest := capoverlay.OverlayManifest{
		SchemaVersion: capoverlay.SchemaVersionManifest,
		PlanID:        strings.TrimSpace(plan.PlanID),
		VideoID:       strings.TrimSpace(plan.VideoID),
		Entries:       make([]capoverlay.OverlayManifestEntry, 0, len(works)),
	}
	ordered := make([]overlayItemCandidate, 0, len(works))
	for _, work := range works {
		manifest.Entries = append(manifest.Entries, work.Entry)
		ordered = append(ordered, overlayItemCandidate{index: work.Index, source: work.Item})
	}
	if err := manifest.Validate(); err != nil {
		return RenderReference{}, err
	}
	workEndUS := int64(0)
	if packedSupported {
		workEndUS = overlayBatchWireEndUS(0, packedEnd)
	} else {
		_, firstEnd := overlayCandidateRenderWindow(plan, ordered[0])
		workEndUS = firstEnd
		for _, candidate := range ordered[1:] {
			_, itemEnd := overlayCandidateRenderWindow(plan, candidate)
			if itemEnd > workEndUS {
				workEndUS = itemEnd
			}
		}
	}
	// The manifest projects plan-absolute in_us directly onto the artifact's
	// frames — start_frame = ceil(in_us · fps_num / (1e6 · fps_den)) with NO
	// origin offset (OVERLAY-MANIFEST-CONTRACT.md §3, the same grid as the
	// final handoff) — so the timeline artifact MUST begin at plan t=0.
	// Anchoring the fused window at the first render window (the batch-path
	// convention) shifted every entry by that offset: on the live A/B
	// (2026-10-24) entries landed 6 frames early on a 30 fps plan while
	// their manifest in_us stayed plan-absolute.
	work := overlayBatchWork{
		sceneID:    strings.TrimSpace(ordered[0].source.SceneID),
		startUS:    0,
		endUS:      overlayBatchWireEndUS(0, workEndUS),
		candidates: ordered,
		packed:     packedPlacements,
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
	var artifact *RenderArtifact
	var jobID, status string
	if cached, hit, _ := e.lookupOverlayRenderCacheTimed(ctx, key); hit {
		artifact, jobID, status = cached, work.id, "completed"
	} else {
		metadata := &overlayItemPublicationMetadata{
			ManifestOwnedCache: true,
			JobID:              textutil.FirstNonEmpty(plan.DriveJobID, plan.PlanID),
			ResultJobID:        textutil.FirstNonEmpty(plan.ResultJobID, plan.DriveJobID),
			ItemID:             "manifest:" + manifest.Digest(),
			BatchID:            manifest.Digest(),
			ItemKind:           "overlay_manifest_timeline",
			SourceStartUS:      work.startUS,
			SourceEndUS:        work.endUS,
			TargetDurationUS:   work.endUS - work.startUS,
			BatchEndUS:         work.endUS,
		}
		for _, candidate := range ordered {
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
		artifact, jobID, status = ref.Artifact, ref.JobID, ref.Status
	}
	reference := assembleManifestTimelineReference(jobID, status, artifact, work, manifest)
	if len(dedicated) == 0 {
		return reference, nil
	}
	dedicatedRef, err := e.enqueueDedicatedOverlayItems(ctx, plan, dedicated)
	if err != nil {
		return RenderReference{}, err
	}
	reference.Items = mergeRenderItemsByPlanOrder(reference.Items, dedicatedRef.Items, plan)
	return reference, nil
}

// mergeRenderItemsByPlanOrder folds the dedicated-path item rows into the
// manifest reference's rows and restores the parent plan's editorial order
// (child plan identity depends on the original plan index).
func mergeRenderItemsByPlanOrder(manifestItems, dedicatedItems []OverlayItemRenderReference, plan capoverlay.OverlayPlan) []OverlayItemRenderReference {
	order := make(map[string]int, len(plan.Items))
	for index, item := range plan.Items {
		order[item.ID] = index
	}
	merged := append(append([]OverlayItemRenderReference(nil), manifestItems...), dedicatedItems...)
	sortRenderItemsByPlanOrder(merged, order)
	return merged
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
			JobID: jobID, Status: status, Artifact: artifact, Manifest: &manifest,
		}},
		Manifest: &manifest,
	}
}

// overlayManifestCompositionCacheKey is the content address of the timeline
// artifact: every entry's composition identity (slot, layer, window, content
// address and placement, in canonical manifest order) plus the rendered child
// plan's context and the deployment epoch. Two runs that compose the same
// timeline must share one cache row and one stored artifact.
//
// The RenderKey deliberately does NOT hash OverlayManifest.Digest(): that
// document embeds the per-run PlanID/VideoID, so keying on it would name the
// identical pixels differently on every retry/run — rows were only ever
// written and never re-read. Entry identities are stable slot identities
// (OverlayManifestEntry.ItemID is reused verbatim across regenerations), and
// the run-scoped recipients stay in the publication path, not in the cache
// identity. The packed positions stay part of the key so a packed timeline
// (different artifact frame grid) can never alias a plan-absolute row.
func overlayManifestCompositionCacheKey(child capoverlay.OverlayPlan, manifest capoverlay.OverlayManifest, epoch string) (OverlayRenderCacheKey, bool) {
	// A distinct digest domain keeps these rows from aliasing the legacy
	// manifest-digest rows (TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE) or the
	// fused batch rows: same tag philosophy as overlayBatchIdentity.
	parts := []string{"overlay-render-manifest-timeline/v1"}
	for _, entry := range manifest.Entries {
		// Fail closed on an incomplete identity: an entry whose content hash is
		// not a canonical 64-hex address cannot honestly name a cache row (the
		// single-item path applies the same gate to its render keys). Only a
		// certified composition identity may share rows across runs.
		content := strings.ToLower(strings.TrimSpace(entry.ContentHash))
		if len(content) != 64 || !isLowerHex(content) {
			return OverlayRenderCacheKey{}, false
		}
		parts = append(parts,
			entry.ItemID,
			strconv.Itoa(entry.Track),
			strconv.FormatInt(entry.InUS, 10),
			strconv.FormatInt(entry.OutUS, 10),
			content,
			manifestEntryPlacementDigest(entry.Placement),
			manifestEntryPackedDigest(entry),
		)
	}
	renderKey := digest.SHA256String(strings.Join(parts, "\x00"))
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

// manifestEntryPlacementDigest is the deterministic digest of the OPTIONAL
// spatial projection of a manifest entry. encoding/json marshals
// ManifestPlacement fields in declaration order with no map ambiguity, so the
// same placement composes one digest and a different rect composes another
// (two rects are two compositions — the pixels differ).
func manifestEntryPlacementDigest(placement *capoverlay.ManifestPlacement) string {
	if placement == nil {
		return ""
	}
	raw, err := json.Marshal(placement)
	if err != nil {
		return ""
	}
	return digest.SHA256String(string(raw))
}

// manifestEntryPackedDigest carries the entry's OPTIONAL packed positions into
// the cache identity. Nil stays distinct from zero (both may be 0 on a packed
// timeline), so a packed composition and an absent projection never alias.
func manifestEntryPackedDigest(entry capoverlay.OverlayManifestEntry) string {
	if entry.ArtifactInUS == nil && entry.ArtifactOutUS == nil {
		return ""
	}
	inUS, outUS := "<nil>", "<nil>"
	if entry.ArtifactInUS != nil {
		inUS = strconv.FormatInt(*entry.ArtifactInUS, 10)
	}
	if entry.ArtifactOutUS != nil {
		outUS = strconv.FormatInt(*entry.ArtifactOutUS, 10)
	}
	return inUS + "-" + outUS
}
