package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/metrics"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// vidRushMaterializationRun carries the per-segment state shared by the
// acquisition/finalization loop. Extracted from materializeOne so the
// orchestration and the per-candidate policy remain separately readable.
type vidRushMaterializationRun struct {
	processor               *VidRushMaterializationProcessor
	plan                    *scriptpkg.ResolvedGenerationPlan
	segment                 scriptpkg.VidRushSegmentResult
	perSceneImageScope      bool
	warnings                []string
	newInternetImageUploads int
}

// vidRushMaterializationState is the loop-scoped bookkeeping shared by the
// three candidate stages. Keeping it in one value makes the stage boundaries
// explicit without changing the order of any decision.
type vidRushMaterializationState struct {
	attempts         map[string]int
	readyImageGroups map[string]struct{}
	readyByProvider  map[string]int
	targetImages     int
	minPerSource     int
}

// vidRushMaterializationCandidate carries the candidate plus the immutable
// per-candidate facts computed before hydration. Later stages must see the
// same classification even when hydration enriches the candidate.
type vidRushMaterializationCandidate struct {
	asset               scriptpkg.SegmentAssetCandidate
	catalogImage        bool
	isImage             bool
	refreshMaterialized bool
	catalogStarted      time.Time
}

func newVidRushMaterializationState(targetImages int, plan *scriptpkg.ResolvedGenerationPlan) *vidRushMaterializationState {
	state := &vidRushMaterializationState{
		attempts:         make(map[string]int, 3),
		readyImageGroups: make(map[string]struct{}, targetImages),
		readyByProvider:  make(map[string]int, 2),
		targetImages:     targetImages,
	}
	if plan != nil {
		state.minPerSource = plan.MinImagesPerSource
	}
	return state
}

func (r *vidRushMaterializationRun) belowSourceMinimum(state *vidRushMaterializationState, provider string) bool {
	return state.minPerSource > 0 && state.readyByProvider[strings.ToLower(strings.TrimSpace(provider))] < state.minPerSource
}

func (r *vidRushMaterializationRun) markReadyImage(state *vidRushMaterializationState, candidate scriptpkg.SegmentAssetCandidate) {
	if candidate.Provider != scriptpkg.VidRushProviderInternetImages && candidate.Provider != scriptpkg.VidRushProviderImageGeneration {
		return
	}
	group := vidRushReadyImageGroupFor(candidate, r.perSceneImageScope, r.processor.catalog != nil)
	if group == "" {
		state.readyByProvider[strings.ToLower(strings.TrimSpace(candidate.Provider))]++
		return
	}
	if _, duplicate := state.readyImageGroups[group]; duplicate {
		// Duplicate bytes/images count once for this scene. The provider
		// counter and the target must not be advanced by a second copy.
		r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: duplicate ready image content for %s ignored: %s", r.segment.SegmentID, group))
		return
	}
	state.readyImageGroups[group] = struct{}{}
	state.readyByProvider[strings.ToLower(strings.TrimSpace(candidate.Provider))]++
}

// materialize acquires, verifies and finalizes one candidate batch. It is the
// single per-candidate pipeline used by both web discovery and the generation
// fallback.
func (r *vidRushMaterializationRun) materialize(ctx context.Context, candidates []scriptpkg.SegmentAssetCandidate, targetImages int) ([]scriptpkg.SegmentAssetCandidate, error) {
	materialized := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	state := newVidRushMaterializationState(targetImages, r.plan)
	for _, candidate := range candidates {
		item, include, handled := r.prepareMaterializationCandidate(ctx, state, candidate)
		if handled {
			if include {
				materialized = append(materialized, item.asset)
			}
			continue
		}
		var verified scriptports.VerifiedArtifact
		item, verified, include, handled = r.acquireMaterializationCandidate(ctx, state, item)
		if handled {
			if include {
				materialized = append(materialized, item.asset)
			}
			continue
		}
		persisted, err := r.finalizeMaterializationCandidate(ctx, state, item, verified)
		if err != nil {
			return nil, err
		}
		materialized = append(materialized, persisted)
	}
	// The per-source minimum is a floor the materializer tries to reach; a
	// provider that still falls short is reported, never silently accepted.
	if state.minPerSource > 0 {
		reported := make(map[string]bool, 2)
		for _, candidate := range candidates {
			provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
			if (provider != scriptpkg.VidRushProviderInternetImages && provider != scriptpkg.VidRushProviderImageGeneration) || reported[provider] {
				continue
			}
			reported[provider] = true
			if r.belowSourceMinimum(state, provider) {
				r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: %s delivered %d ready images for %s, below min_images_per_source=%d", provider, state.readyByProvider[provider], r.segment.SegmentID, state.minPerSource))
			}
		}
	}
	return materialized, nil
}

// prepareMaterializationCandidate applies ownership, hydration, cache and
// reuse policy. handled=false means the candidate must enter acquisition.
func (r *vidRushMaterializationRun) prepareMaterializationCandidate(ctx context.Context, state *vidRushMaterializationState, candidate scriptpkg.SegmentAssetCandidate) (vidRushMaterializationCandidate, bool, bool) {
	// Materialized caches are shared across runs, so a cached artifact is
	// untrusted with respect to the current scene. Never let a stale
	// candidate from another r.segment enter selection or certification.
	if owner := strings.TrimSpace(candidate.SegmentID); owner != "" && owner != strings.TrimSpace(r.segment.SegmentID) {
		return vidRushMaterializationCandidate{}, false, true
	}
	item, wasReady, include := r.hydrateMaterializationCandidate(ctx, candidate)
	if include {
		return item, true, true
	}
	item, include, handled := r.reuseMaterializationCandidate(ctx, state, item, wasReady)
	return item, include, handled
}

// hydrateMaterializationCandidate loads the durable catalog view and clears
// any recorded lifecycle that a force refresh must not trust.
func (r *vidRushMaterializationRun) hydrateMaterializationCandidate(ctx context.Context, candidate scriptpkg.SegmentAssetCandidate) (vidRushMaterializationCandidate, bool, bool) {
	isImage := candidate.Provider == scriptpkg.VidRushProviderInternetImages || candidate.Provider == scriptpkg.VidRushProviderImageGeneration
	catalogImage := r.processor.catalog != nil && strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) &&
		(strings.TrimSpace(candidate.Entity) != "" || strings.HasPrefix(strings.TrimSpace(candidate.AssetID), "entity-image-"))
	refreshMaterialized := r.plan != nil && (r.plan.ForceRefresh || r.plan.MediaPlan.ForceRefreshAssets)
	wasReady := readyVidRushCandidate(candidate)
	if hydrated, hydrationErr := r.processor.hydrateEntityCatalogMaterialization(ctx, candidate); hydrationErr != nil {
		r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: entity catalog lookup: %v", hydrationErr))
	} else {
		if catalogImage && !wasReady && readyVidRushCandidate(hydrated) &&
			(strings.TrimSpace(hydrated.DriveLink) != "" || strings.TrimSpace(hydrated.ContentSHA256) != "") {
			if catalogMetrics := entityImageCatalogMetricsFor(r.processor.metrics); catalogMetrics != nil {
				catalogMetrics.IncEntityImageCatalogDriveReuse()
			}
		}
		candidate = hydrated
	}
	// A force refresh distrusts every recorded lifecycle: the bytes
	// behind a stored URL or catalog row can change upstream, so a
	// recorded hash is only a claim until bytes are re-downloaded
	// and re-hashed in-run. Stripping here (after hydration, which
	// re-stamps catalog statuses) is the single point that keeps
	// un-acquired candidates out of selection and overlay bindings
	// on refresh — including the groups-cap and acquire-budget
	// preservation paths below, which keep candidates for
	// diagnostics only. Identifiers and URLs are preserved so the
	// Acquire below can still re-download from the source.
	if refreshMaterialized {
		candidate.AcquisitionStatus = ""
		candidate.VerificationStatus = ""
		candidate.PersistenceStatus = ""
		candidate.IndexStatus = ""
	}
	item := vidRushMaterializationCandidate{
		asset: candidate, catalogImage: catalogImage, isImage: isImage, refreshMaterialized: refreshMaterialized,
	}
	semanticStatus := strings.TrimSpace(candidate.SemanticStatus)
	if catalogImage && semanticStatus != "" && !strings.EqualFold(semanticStatus, entitycatalog.CandidateSemanticAccepted) {
		// Discovery already downloaded and visually scored this entity
		// candidate. Re-acquiring an unknown/rejected image here repeats the
		// expensive network transfer but cannot make it bindable this run.
		return item, wasReady, true
	}
	return item, wasReady, false
}

// reuseMaterializationCandidate applies the image cap, YouTube terminal path
// and the two reuse caches. handled=false means acquisition is still needed.
func (r *vidRushMaterializationRun) reuseMaterializationCandidate(ctx context.Context, state *vidRushMaterializationState, item vidRushMaterializationCandidate, wasReady bool) (vidRushMaterializationCandidate, bool, bool) {
	candidate := item.asset
	if item.isImage && state.targetImages > 0 && len(state.readyImageGroups) >= state.targetImages && (!item.catalogImage || wasReady) && !r.belowSourceMinimum(state, candidate.Provider) {
		// Keep the remaining remote hits for diagnostics/candidate-set
		// hashing, but do not download or persist surplus images. This
		// prevents pgvector/Drive fan-out from exceeding the scene r.plan.
		return item, true, true
	}
	legacyPersisted := candidate.IsLegacyCandidate() && strings.TrimSpace(candidate.DriveLink) != ""
	// YouTube clips are already fully materialized by the canonical
	// StockService/extractor. Never send them through VidRush's generic
	// Acquire → Verify → Finalize path a second time.
	if candidate.Provider == scriptpkg.VidRushProviderYouTube && readyVidRushCandidate(candidate) {
		return item, true, true
	}
	// A force refresh must re-acquire even a durable catalog hit: the
	// strip above already cleared recorded lifecycles, so this gate is
	// defense in depth — a ready durable hit never terminates a refresh
	// run. A fresh Acquire→Verify recomputes the digest and the
	// Finalize below commits the new version through the canonical
	// writer, so the catalog self-heals instead of pinning stale bytes.
	// A durable catalog hit is terminal even when this run has a Drive
	// output root. Reacquiring it only to copy it into a per-run images
	// folder downloaded the same entity image once per scene. Its
	// canonical DriveLink (and cached LocalPath when still present) is
	// already a reusable artifact; only new images go through the
	// run-specific finalization route below.
	if !item.refreshMaterialized && readyVidRushCandidate(candidate) && (!candidate.IsLegacyCandidate() || legacyPersisted) {
		if item.catalogImage {
			incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageReuse, scriptmetrics.EntityImageOutcomeSuccess)
		}
		r.markReadyImage(state, candidate)
		return item, true, true
	}
	if !item.refreshMaterialized {
		if key := vidRushCandidateIdentity(candidate); key != "" {
			if cached, ok := cacheLoad(vidrushMaterializedCache, key); ok {
				if persisted, ok := cached.(scriptpkg.SegmentAssetCandidate); ok &&
					(strings.TrimSpace(persisted.SegmentID) == "" || strings.TrimSpace(persisted.SegmentID) == strings.TrimSpace(r.segment.SegmentID)) &&
					readyVidRushCandidate(persisted) {
					if item.catalogImage {
						incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageReuse, scriptmetrics.EntityImageOutcomeSuccess)
					}
					r.markReadyImage(state, persisted)
					item.asset = persisted
					return item, true, true
				}
			}
			var persisted scriptpkg.SegmentAssetCandidate
			if hit, cacheErr := loadVidRushPersistentJSON(ctx, r.processor.cache, "materialized", key, &persisted); cacheErr != nil {
				r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: durable cache read %s: %v", key, cacheErr))
			} else if hit &&
				(strings.TrimSpace(persisted.SegmentID) == "" || strings.TrimSpace(persisted.SegmentID) == strings.TrimSpace(r.segment.SegmentID)) &&
				readyVidRushCandidate(persisted) {
				if item.catalogImage {
					incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageReuse, scriptmetrics.EntityImageOutcomeSuccess)
				}
				cacheStore(vidrushMaterializedCache, key, persisted)
				r.markReadyImage(state, persisted)
				item.asset = persisted
				return item, true, true
			}
		}
	}
	return item, false, false
}

// acquireMaterializationCandidate enforces provider policy and budget, then
// runs Acquire→Verify. handled=false means the verified candidate is ready to
// be finalized.
func (r *vidRushMaterializationRun) acquireMaterializationCandidate(ctx context.Context, state *vidRushMaterializationState, item vidRushMaterializationCandidate) (vidRushMaterializationCandidate, scriptports.VerifiedArtifact, bool, bool) {
	candidate := item.asset
	providerName := strings.ToLower(strings.TrimSpace(candidate.Provider))
	if _, supported := providerPolicy(providerName); !supported {
		return item, scriptports.VerifiedArtifact{}, true, true
	}
	acquireBudget := vidRushAcquireBudget(r.plan, providerName)
	if providerName == scriptpkg.VidRushProviderInternetImages || providerName == scriptpkg.VidRushProviderImageGeneration {
		segmentTarget := vidRushImageTargetForSegment(r.plan, r.segment)
		if segmentTarget > vidRushImageTarget(r.plan) {
			acquireBudget = segmentTarget + vidRushImageAcquireSlack
		}
	}
	budgetKey := vidRushAcquisitionBudgetKey(providerName, candidate.Entity, r.segment.SegmentID)
	if state.attempts[budgetKey] >= acquireBudget {
		// Preserve the discovered candidate for diagnostics and a future
		// retry, but do not turn every remote search hit into a download.
		// Recorded lifecycles were already stripped above on refresh,
		// so a preserved candidate can never bind by a stale hash.
		return item, scriptports.VerifiedArtifact{}, true, true
	}
	state.attempts[budgetKey]++

	provider, err := r.processor.providers.Provider(providerName)
	if err != nil {
		candidate.AcquisitionStatus = scriptpkg.VidRushStatusFailed
		r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: %s provider unavailable for %s: %v", providerName, r.segment.SegmentID, err))
		item.asset = candidate
		return item, scriptports.VerifiedArtifact{}, true, true
	}
	catalogMaterializationStarted := time.Time{}
	if item.catalogImage {
		incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageAcquire, scriptmetrics.EntityImageOutcomeFound)
		if catalogMetrics := entityImageCatalogMetricsFor(r.processor.metrics); catalogMetrics != nil {
			catalogMetrics.IncEntityImageCatalogNewDownload()
			catalogMaterializationStarted = time.Now()
		}
	}
	item.catalogStarted = catalogMaterializationStarted
	lifecycle := acquireAndVerify(ctx, provider, candidate, providerName, r.processor.metrics)
	if item.catalogImage {
		outcome := scriptmetrics.EntityImageOutcomeSuccess
		if lifecycle.err != nil && lifecycle.stage == "acquire" {
			outcome = scriptmetrics.EntityImageOutcomeFailure
		}
		incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageAcquire, outcome)
	}
	if lifecycle.err != nil && lifecycle.stage == "acquire" {
		err = lifecycle.err
		candidate = lifecycle.candidate
		if item.catalogImage {
			addEntityImageCatalogCandidates(r.processor.metrics, scriptmetrics.EntityImageStageAcquire, scriptmetrics.EntityImageOutcomeRejected, 1)
		}
		if !catalogMaterializationStarted.IsZero() {
			if catalogMetrics := entityImageCatalogMetricsFor(r.processor.metrics); catalogMetrics != nil {
				catalogMetrics.IncEntityImageCatalogURLBroken()
			}
			observeEntityImageCatalogMaterialization(r.processor.metrics, catalogMaterializationStarted)
		}
		if statusErr := setEntityImageCatalogCandidateStatus(ctx, r.processor.catalog, candidate, entitycatalog.CandidateStatusBroken); statusErr != nil {
			r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: mark broken URL: %v", statusErr))
		}
		r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: acquire %s for %s: %v", providerName, r.segment.SegmentID, err))
		item.asset = candidate
		return item, scriptports.VerifiedArtifact{}, true, true
	}
	if lifecycle.err != nil {
		if item.catalogImage {
			incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageVerify, scriptmetrics.EntityImageOutcomeFailure)
		}
		err = lifecycle.err
		candidate = lifecycle.candidate
		if item.catalogImage {
			addEntityImageCatalogCandidates(r.processor.metrics, scriptmetrics.EntityImageStageVerify, scriptmetrics.EntityImageOutcomeRejected, 1)
		}
		if !catalogMaterializationStarted.IsZero() {
			if catalogMetrics := entityImageCatalogMetricsFor(r.processor.metrics); catalogMetrics != nil {
				catalogMetrics.IncEntityImageCatalogURLBroken()
			}
			observeEntityImageCatalogMaterialization(r.processor.metrics, catalogMaterializationStarted)
		}
		if statusErr := setEntityImageCatalogCandidateStatus(ctx, r.processor.catalog, candidate, entitycatalog.CandidateStatusBroken); statusErr != nil {
			r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: mark broken URL: %v", statusErr))
		}
		r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: verify %s for %s: %v", providerName, r.segment.SegmentID, err))
		item.asset = candidate
		return item, scriptports.VerifiedArtifact{}, true, true
	}
	if item.catalogImage {
		incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageVerify, scriptmetrics.EntityImageOutcomeSuccess)
		addEntityImageCatalogCandidates(r.processor.metrics, scriptmetrics.EntityImageStageAcquire, scriptmetrics.EntityImageOutcomeAccepted, 1)
		addEntityImageCatalogCandidates(r.processor.metrics, scriptmetrics.EntityImageStageVerify, scriptmetrics.EntityImageOutcomeAccepted, 1)
	}
	return item, lifecycle.verified, false, false
}

// finalizeMaterializationCandidate applies the semantic gate, persists the
// verified artifact and records the reusable caches. A non-nil error is
// terminal for the whole batch.
func (r *vidRushMaterializationRun) finalizeMaterializationCandidate(ctx context.Context, state *vidRushMaterializationState, item vidRushMaterializationCandidate, verified scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	candidate := item.asset
	// Person/entity catalog images are reusable only after a semantic
	// identity verdict. Publishing a technically valid but unknown image
	// costs a slow Drive write and can never satisfy entity binding. Keep
	// it in diagnostics for a later refresh, but do not finalize it.
	semanticStatus := strings.TrimSpace(verified.Candidate.SemanticStatus)
	if semanticStatus == "" {
		semanticStatus = strings.TrimSpace(candidate.SemanticStatus)
	}
	if item.catalogImage && semanticStatus != "" && !strings.EqualFold(semanticStatus, entitycatalog.CandidateSemanticAccepted) {
		verified.Candidate.PersistenceStatus = ""
		r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: deferred unaccepted entity image for %s (%s)", r.segment.SegmentID, verified.Candidate.Entity))
		return verified.Candidate, nil
	}
	// One dispatch for every artifact family: the provider clips the
	// run used land in <Title>/<Language>/clips under
	// r.plan.DriveFolderID, while entity images are pulled back OUT of the
	// run bundle and published by the finalizer into the canonical image
	// library (<ImagesRootFolder>/<canonical-entity-slug>/<file>).
	verified = routeGenerationOutputToPlanBundle(r.plan, verified)
	cacheKey := vidRushCandidateIdentity(candidate)
	var persisted scriptpkg.SegmentAssetCandidate
	err := measureVidRushProvider(ctx, r.processor.metrics, kernobs.OperationInfo{
		Stage: kernobs.StagePersist, Component: "vidrush", Operation: "finalize", Provider: strings.ToLower(strings.TrimSpace(candidate.Provider)),
	}, func(callCtx context.Context) error {
		var finalizeErr error
		persisted, finalizeErr = finalizeWithRetry(callCtx, r.processor.finalizer, verified)
		return finalizeErr
	})
	if err != nil {
		if item.catalogImage {
			incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStagePublish, scriptmetrics.EntityImageOutcomeFailure)
			addEntityImageCatalogCandidates(r.processor.metrics, scriptmetrics.EntityImageStagePublish, scriptmetrics.EntityImageOutcomeRejected, 1)
		}
		verified.Candidate.PersistenceStatus = scriptpkg.VidRushStatusFailed
		verified.Candidate.FailLifecycle(scriptpkg.VidRushLifecycleStageFinalize, err.Error())
		observeEntityImageCatalogMaterialization(r.processor.metrics, item.catalogStarted)
		r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: finalize %s for %s: %v", strings.ToLower(strings.TrimSpace(candidate.Provider)), r.segment.SegmentID, err))
		return verified.Candidate, nil
	}
	persisted.ClearLifecycleFailure()
	if item.catalogImage {
		incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStagePublish, scriptmetrics.EntityImageOutcomeSuccess)
		addEntityImageCatalogCandidates(r.processor.metrics, scriptmetrics.EntityImageStagePublish, scriptmetrics.EntityImageOutcomeAccepted, 1)
	}
	if strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
		r.newInternetImageUploads++
	}
	observeEntityImageCatalogMaterialization(r.processor.metrics, item.catalogStarted)
	if catalogErr := r.processor.persistEntityCatalogMaterialization(ctx, candidate, persisted); catalogErr != nil {
		return scriptpkg.SegmentAssetCandidate{}, fmt.Errorf("vidrush_materialization: entity catalog materialization: %w", catalogErr)
	}
	if item.isImage && readyVidRushCandidate(persisted) {
		r.markReadyImage(state, persisted)
	}
	if key := vidRushCandidateIdentity(persisted); key != "" && strings.TrimSpace(persisted.PersistenceStatus) == scriptpkg.VidRushStatusPersisted && strings.TrimSpace(persisted.DriveLink) != "" {
		cacheStore(vidrushMaterializedCache, key, persisted)
		if cacheErr := storeVidRushPersistentJSON(ctx, r.processor.cache, "materialized", key, persisted); cacheErr != nil {
			r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: durable cache write %s: %v", key, cacheErr))
		}
	}
	if cacheKey != "" && strings.TrimSpace(persisted.PersistenceStatus) == scriptpkg.VidRushStatusPersisted && strings.TrimSpace(persisted.DriveLink) != "" {
		cacheStore(vidrushMaterializedCache, cacheKey, persisted)
		if cacheErr := storeVidRushPersistentJSON(ctx, r.processor.cache, "materialized", cacheKey, persisted); cacheErr != nil {
			r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: durable cache write %s: %v", cacheKey, cacheErr))
		}
	}
	return persisted, nil
}

// vidRushAcquisitionBudgetKey charges an acquisition to the entity that
// requested it, or to the scene when the candidate has no entity. A provider
// counter shared by every entity let one person's downloads consume the
// budget of every other person in the same segment.
func vidRushAcquisitionBudgetKey(provider, entity, segment string) string {
	scene := strings.TrimSpace(segment)
	entity = strings.ToLower(strings.Join(strings.Fields(entity), " "))
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00scene:" + scene + "\x00entity:" + entity
}

// vidRushReadyImageGroupFor is the one grouping rule for ready images. In
// per-scene scope the content hash is the identity, so duplicate bytes count
// once; catalog entity images are grouped by canonical identity instead.
func vidRushReadyImageGroupFor(candidate scriptpkg.SegmentAssetCandidate, perScene, catalog bool) string {
	if catalog && strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) &&
		(strings.TrimSpace(candidate.Entity) != "" || strings.HasPrefix(strings.TrimSpace(candidate.AssetID), "entity-image-")) {
		return vidRushCandidateIdentity(candidate)
	}
	if perScene {
		group := strings.ToLower(strings.TrimSpace(candidate.ContentSHA256))
		if group == "" {
			group = strings.ToLower(strings.TrimSpace(candidate.AssetID))
		}
		return group
	}
	return vidRushImageGroup(candidate)
}
