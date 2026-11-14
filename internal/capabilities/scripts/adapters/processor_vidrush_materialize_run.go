package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
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

// materialize acquires, verifies and finalizes one candidate batch. It is the
// single per-candidate pipeline used by both web discovery and the generation
// fallback.
func (r *vidRushMaterializationRun) materialize(ctx context.Context, candidates []scriptpkg.SegmentAssetCandidate, targetImages int) ([]scriptpkg.SegmentAssetCandidate, error) {

	materialized := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	attempts := make(map[string]int, 3)
	readyImageGroups := make(map[string]struct{}, targetImages)
	// readyByProvider counts ready images per image provider so the
	// per-source minimum (r.plan.MinImagesPerSource) can be enforced.
	readyByProvider := make(map[string]int, 2)
	minPerSource := 0
	if r.plan != nil {
		minPerSource = r.plan.MinImagesPerSource
	}
	belowSourceMinimum := func(provider string) bool {
		return minPerSource > 0 && readyByProvider[strings.ToLower(strings.TrimSpace(provider))] < minPerSource
	}
	markReadyImage := func(candidate scriptpkg.SegmentAssetCandidate) {
		if candidate.Provider != scriptpkg.VidRushProviderInternetImages && candidate.Provider != scriptpkg.VidRushProviderImageGeneration {
			return
		}
		readyByProvider[strings.ToLower(strings.TrimSpace(candidate.Provider))]++
		group := vidRushImageGroup(candidate)
		if r.processor.catalog != nil && strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) &&
			(strings.TrimSpace(candidate.Entity) != "" || strings.HasPrefix(strings.TrimSpace(candidate.AssetID), "entity-image-")) {
			group = vidRushCandidateIdentity(candidate)
		} else if r.perSceneImageScope {
			group = strings.ToLower(strings.TrimSpace(candidate.ContentSHA256))
			if group == "" {
				group = strings.ToLower(strings.TrimSpace(candidate.AssetID))
			}
		}
		if group != "" {
			readyImageGroups[group] = struct{}{}
		}
	}
	for _, candidate := range candidates {
		// Materialized caches are shared across runs, so a cached artifact is
		// untrusted with respect to the current scene. Never let a stale
		// candidate from another r.segment enter selection or certification.
		if owner := strings.TrimSpace(candidate.SegmentID); owner != "" && owner != strings.TrimSpace(r.segment.SegmentID) {
			continue
		}
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
		if isImage && targetImages > 0 && len(readyImageGroups) >= targetImages && (!catalogImage || wasReady) && !belowSourceMinimum(candidate.Provider) {
			// Keep the remaining remote hits for diagnostics/candidate-set
			// hashing, but do not download or persist surplus images. This
			// prevents Qdrant/Drive fan-out from exceeding the scene r.plan.
			materialized = append(materialized, candidate)
			continue
		}
		legacyPersisted := candidate.IsLegacyCandidate() && strings.TrimSpace(candidate.DriveLink) != ""
		// YouTube clips are already fully materialized by the canonical
		// StockService/extractor. Never send them through VidRush's generic
		// Acquire → Verify → Finalize path a second time.
		if candidate.Provider == scriptpkg.VidRushProviderYouTube && readyVidRushCandidate(candidate) {
			materialized = append(materialized, candidate)
			continue
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
		if !refreshMaterialized && readyVidRushCandidate(candidate) && (!candidate.IsLegacyCandidate() || legacyPersisted) {
			if catalogImage {
				incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageReuse, scriptmetrics.EntityImageOutcomeSuccess)
			}
			materialized = append(materialized, candidate)
			markReadyImage(candidate)
			continue
		}
		if !refreshMaterialized {
			if key := vidRushCandidateIdentity(candidate); key != "" {
				if cached, ok := vidrushMaterializedCache.Get(key); ok {
					if persisted, ok := cached.(scriptpkg.SegmentAssetCandidate); ok &&
						(strings.TrimSpace(persisted.SegmentID) == "" || strings.TrimSpace(persisted.SegmentID) == strings.TrimSpace(r.segment.SegmentID)) &&
						readyVidRushCandidate(persisted) {
						if catalogImage {
							incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageReuse, scriptmetrics.EntityImageOutcomeSuccess)
						}
						materialized = append(materialized, persisted)
						markReadyImage(persisted)
						continue
					}
				}
				var persisted scriptpkg.SegmentAssetCandidate
				if hit, cacheErr := loadVidRushPersistentJSON(ctx, r.processor.cache, "materialized", key, &persisted); cacheErr != nil {
					r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: durable cache read %s: %v", key, cacheErr))
				} else if hit &&
					(strings.TrimSpace(persisted.SegmentID) == "" || strings.TrimSpace(persisted.SegmentID) == strings.TrimSpace(r.segment.SegmentID)) &&
					readyVidRushCandidate(persisted) {
					if catalogImage {
						incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageReuse, scriptmetrics.EntityImageOutcomeSuccess)
					}
					materialized = append(materialized, persisted)
					vidrushMaterializedCache.Put(key, persisted)
					markReadyImage(persisted)
					continue
				}
			}
		}
		providerName := strings.ToLower(strings.TrimSpace(candidate.Provider))
		if _, supported := providerPolicy(providerName); !supported {
			materialized = append(materialized, candidate)
			continue
		}
		acquireBudget := vidRushAcquireBudget(r.plan, providerName)
		if providerName == scriptpkg.VidRushProviderInternetImages || providerName == scriptpkg.VidRushProviderImageGeneration {
			segmentTarget := vidRushImageTargetForSegment(r.plan, r.segment)
			if segmentTarget > vidRushImageTarget(r.plan) {
				acquireBudget = segmentTarget + vidRushImageAcquireSlack
			}
		}
		if attempts[providerName] >= acquireBudget {
			// Preserve the discovered candidate for diagnostics and a future
			// retry, but do not turn every remote search hit into a download.
			// Recorded lifecycles were already stripped above on refresh,
			// so a preserved candidate can never bind by a stale hash.
			materialized = append(materialized, candidate)
			continue
		}
		attempts[providerName]++

		provider, err := r.processor.providers.Provider(providerName)
		if err != nil {
			candidate.AcquisitionStatus = scriptpkg.VidRushStatusFailed
			r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: %s provider unavailable for %s: %v", providerName, r.segment.SegmentID, err))
			materialized = append(materialized, candidate)
			continue
		}
		catalogMaterializationStarted := time.Time{}
		if catalogImage := r.processor.catalog != nil && providerName == scriptpkg.VidRushProviderInternetImages &&
			(strings.TrimSpace(candidate.Entity) != "" || strings.HasPrefix(strings.TrimSpace(candidate.AssetID), "entity-image-")); catalogImage {
			incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageAcquire, scriptmetrics.EntityImageOutcomeFound)
			if catalogMetrics := entityImageCatalogMetricsFor(r.processor.metrics); catalogMetrics != nil {
				catalogMetrics.IncEntityImageCatalogNewDownload()
				catalogMaterializationStarted = time.Now()
			}
		}
		lifecycle := acquireAndVerify(ctx, provider, candidate, providerName, r.processor.metrics)
		if catalogImage {
			outcome := scriptmetrics.EntityImageOutcomeSuccess
			if lifecycle.err != nil && lifecycle.stage == "acquire" {
				outcome = scriptmetrics.EntityImageOutcomeFailure
			}
			incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageAcquire, outcome)
		}
		if lifecycle.err != nil && lifecycle.stage == "acquire" {
			err = lifecycle.err
			candidate = lifecycle.candidate
			if catalogImage {
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
			materialized = append(materialized, candidate)
			continue
		}
		if lifecycle.err != nil {
			if catalogImage {
				incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageVerify, scriptmetrics.EntityImageOutcomeFailure)
			}
			err = lifecycle.err
			candidate = lifecycle.candidate
			if catalogImage {
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
			materialized = append(materialized, candidate)
			continue
		}
		if catalogImage {
			incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStageVerify, scriptmetrics.EntityImageOutcomeSuccess)
			addEntityImageCatalogCandidates(r.processor.metrics, scriptmetrics.EntityImageStageAcquire, scriptmetrics.EntityImageOutcomeAccepted, 1)
			addEntityImageCatalogCandidates(r.processor.metrics, scriptmetrics.EntityImageStageVerify, scriptmetrics.EntityImageOutcomeAccepted, 1)
		}

		verified := lifecycle.verified
		// One dispatch for every artifact family: the provider clips the
		// run used land in <Title>/<Language>/clips under
		// r.plan.DriveFolderID, while entity images are pulled back OUT of the
		// run bundle and published by the finalizer into the canonical image
		// library (<ImagesRootFolder>/<canonical-entity-slug>/<file>).
		verified = routeGenerationOutputToPlanBundle(r.plan, verified)
		cacheKey := vidRushCandidateIdentity(candidate)
		var persisted scriptpkg.SegmentAssetCandidate
		err = measureVidRushProvider(ctx, r.processor.metrics, kernobs.OperationInfo{
			Stage: kernobs.StagePersist, Component: "vidrush", Operation: "finalize", Provider: providerName,
		}, func(callCtx context.Context) error {
			var finalizeErr error
			persisted, finalizeErr = finalizeWithRetry(callCtx, r.processor.finalizer, verified)
			return finalizeErr
		})
		if err != nil {
			if catalogImage {
				incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStagePublish, scriptmetrics.EntityImageOutcomeFailure)
				addEntityImageCatalogCandidates(r.processor.metrics, scriptmetrics.EntityImageStagePublish, scriptmetrics.EntityImageOutcomeRejected, 1)
			}
			verified.Candidate.PersistenceStatus = scriptpkg.VidRushStatusFailed
			verified.Candidate.FailLifecycle(scriptpkg.VidRushLifecycleStageFinalize, err.Error())
			observeEntityImageCatalogMaterialization(r.processor.metrics, catalogMaterializationStarted)
			r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: finalize %s for %s: %v", providerName, r.segment.SegmentID, err))
			materialized = append(materialized, verified.Candidate)
			continue
		}
		persisted.ClearLifecycleFailure()
		materialized = append(materialized, persisted)
		if catalogImage {
			incEntityImageCatalogLifecycle(r.processor.metrics, scriptmetrics.EntityImageStagePublish, scriptmetrics.EntityImageOutcomeSuccess)
			addEntityImageCatalogCandidates(r.processor.metrics, scriptmetrics.EntityImageStagePublish, scriptmetrics.EntityImageOutcomeAccepted, 1)
		}
		if providerName == scriptpkg.VidRushProviderInternetImages {
			r.newInternetImageUploads++
		}
		observeEntityImageCatalogMaterialization(r.processor.metrics, catalogMaterializationStarted)
		if catalogErr := r.processor.persistEntityCatalogMaterialization(ctx, candidate, persisted); catalogErr != nil {
			return nil, fmt.Errorf("vidrush_materialization: entity catalog materialization: %w", catalogErr)
		}
		if isImage && readyVidRushCandidate(persisted) {
			markReadyImage(persisted)
		}
		if key := vidRushCandidateIdentity(persisted); key != "" && strings.TrimSpace(persisted.PersistenceStatus) == scriptpkg.VidRushStatusPersisted && strings.TrimSpace(persisted.DriveLink) != "" {
			vidrushMaterializedCache.Put(key, persisted)
			if cacheErr := storeVidRushPersistentJSON(ctx, r.processor.cache, "materialized", key, persisted); cacheErr != nil {
				r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: durable cache write %s: %v", key, cacheErr))
			}
		}
		if cacheKey != "" && strings.TrimSpace(persisted.PersistenceStatus) == scriptpkg.VidRushStatusPersisted && strings.TrimSpace(persisted.DriveLink) != "" {
			vidrushMaterializedCache.Put(cacheKey, persisted)
			if cacheErr := storeVidRushPersistentJSON(ctx, r.processor.cache, "materialized", cacheKey, persisted); cacheErr != nil {
				r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: durable cache write %s: %v", cacheKey, cacheErr))
			}
		}
	}
	// The per-source minimum is a floor the materializer tries to reach; a
	// provider that still falls short is reported, never silently accepted.
	if minPerSource > 0 {
		reported := make(map[string]bool, 2)
		for _, candidate := range candidates {
			provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
			if (provider != scriptpkg.VidRushProviderInternetImages && provider != scriptpkg.VidRushProviderImageGeneration) || reported[provider] {
				continue
			}
			reported[provider] = true
			if belowSourceMinimum(provider) {
				r.warnings = append(r.warnings, fmt.Sprintf("vidrush_materialization: %s delivered %d ready images for %s, below min_images_per_source=%d", provider, readyByProvider[provider], r.segment.SegmentID, minPerSource))
			}
		}
	}
	return materialized, nil

}
