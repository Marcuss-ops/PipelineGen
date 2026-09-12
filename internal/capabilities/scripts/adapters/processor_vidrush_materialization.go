package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// VidRushMaterializationProcessor is the single acquisition boundary for
// provider candidates. Search processors only discover candidates; this
// processor is the only postprocessor allowed to acquire, verify and send a
// candidate through the common finalizer.
type VidRushMaterializationProcessor struct {
	providers *VidRushAssetProviderRegistry
	finalizer scriptports.VidRushArtifactFinalizer
	cache     scriptports.VidRushCachePort
	catalog   entitycatalog.Repository
	metrics   VidRushTimingMetrics
	sampler   scriptports.MediaSamplerPort
	log       *zap.Logger
}

// WithLogger makes materialization failures observable in live runs without
// changing the provider/finalizer ports or leaking infrastructure into them.
func (p *VidRushMaterializationProcessor) WithLogger(log *zap.Logger) *VidRushMaterializationProcessor {
	if p != nil {
		p.log = log
	}
	return p
}

type vidRushMaterializedSegment struct {
	result   scriptpkg.VidRushSegmentResult
	warnings []string
}

const (
	vidRushArtlistAcquireBudget = 3
	// Provider calls are bounded independently from the postprocessor context.
	// A failed remote provider must not consume the whole VidRush run while its
	// fallback scraper or browser waits on an unavailable upstream.
	// Browser-authenticated HLS downloads commonly need ~30 seconds before
	// the scraper returns the verified local artifact. Keep this below the
	// resolver's broader timeout while avoiding premature cancellation of a
	// valid Artlist acquisition.
	vidRushArtlistAcquireTimeout    = 120 * time.Second
	vidRushImageAcquireTimeout      = 15 * time.Second
	vidRushGenerationAcquireTimeout = 2 * time.Minute
	vidRushVerifyTimeout            = 20 * time.Second
	// Search providers routinely return a mixture of hotlink-protected,
	// corrupt and valid URLs. Keep the candidate set bounded upstream, but
	// allow enough acquisition attempts to reach the requested number of
	// durable verified images without ever promoting an unverified hit.
	// Search providers commonly place unusable hotlinks before downloadable
	// images. Keep trying the bounded candidate pool until the scene reaches
	// its target instead of turning the first few bad URLs into a false miss.
	vidRushImageAcquireSlack = 20
)

func NewVidRushMaterializationProcessor(providers *VidRushAssetProviderRegistry, finalizer scriptports.VidRushArtifactFinalizer, metrics ...VidRushTimingMetrics) *VidRushMaterializationProcessor {
	return NewVidRushMaterializationProcessorWithCatalog(providers, finalizer, nil, nil, metrics...)
}

func NewVidRushMaterializationProcessorWithCatalog(providers *VidRushAssetProviderRegistry, finalizer scriptports.VidRushArtifactFinalizer, cache scriptports.VidRushCachePort, catalog entitycatalog.Repository, metrics ...VidRushTimingMetrics) *VidRushMaterializationProcessor {
	var m VidRushTimingMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &VidRushMaterializationProcessor{providers: providers, finalizer: finalizer, cache: cache, catalog: catalog, metrics: m}
}

// WithMediaSampler installs the canonical selection port. Materialization
// requires this port before binding a primary video.
func (p *VidRushMaterializationProcessor) WithMediaSampler(sampler scriptports.MediaSamplerPort) *VidRushMaterializationProcessor {
	if p != nil {
		p.sampler = sampler
	}
	return p
}

func (p *VidRushMaterializationProcessor) Name() ProcessorName {
	return ProcessorVidRushMaterialization
}

func (p *VidRushMaterializationProcessor) Policy(plan *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	if plan != nil && (providerEnabledForVidRush(plan, scriptpkg.VidRushProviderArtlist) ||
		providerEnabledForVidRush(plan, scriptpkg.VidRushProviderInternetImages) ||
		providerEnabledForVidRush(plan, scriptpkg.VidRushProviderImageGeneration)) {
		return ProcessorRequired
	}
	return ProcessorBestEffort
}

func (p *VidRushMaterializationProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p == nil {
		return nil, fmt.Errorf("vidrush materialization: processor not configured")
	}
	if result, handled := p.metadataOnlyResult(plan, input); handled {
		return result, nil
	}
	if allSegmentsFixedMedia(input.SpecScene, input.VidRushSegments) {
		segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input.VidRushSegments))
		for _, segment := range input.VidRushSegments {
			cloned := cloneVidRushSegmentResult(segment)
			cloned.ExecutionMode = scriptpkg.SceneExecutionFixedMedia
			segments = append(segments, cloned)
		}
		return &PostProcessResult{VidRushSegments: segments, Changed: true}, nil
	}
	if err := materializationDependenciesError(plan, input, p.providers, p.finalizer); err != nil {
		return nil, err
	}
	if p.providers == nil || p.finalizer == nil {
		return &PostProcessResult{}, nil
	}
	if err := requireVidRushEnabledProviders(plan, p.providers); err != nil {
		return nil, err
	}
	if len(input.VidRushSegments) == 0 {
		return &PostProcessResult{}, nil
	}

	processed, err := concurrent.Map(ctx, input.VidRushSegments, 2, func(ctx context.Context, _ int, segment scriptpkg.VidRushSegmentResult) (vidRushMaterializedSegment, error) {
		return p.materializeOne(ctx, plan, segment)
	})
	if err != nil {
		return nil, fmt.Errorf("vidrush materialization: bounded segment workers: %w", err)
	}
	segments := make([]scriptpkg.VidRushSegmentResult, 0, len(processed))
	var warnings []string
	for _, item := range processed {
		segments = append(segments, item.result)
		warnings = append(warnings, item.warnings...)
	}

	// Internet-image discovery runs before materialization, when candidates
	// still have only remote provenance. Re-project entity bindings after the
	// common finalizer has persisted the assets so the SpecScene receives the
	// durable AssetID/DriveLink rather than the earlier not_found result.
	var entityImagePolicy mediadomain.EntityImagePolicy
	if plan != nil {
		entityImagePolicy = plan.MediaPlan.Extraction.EntityImages
		entityImagePolicy.Enabled = plan.MediaPlan.Extraction.EntityImageSurfaceEnabled()
	}
	updatedSpecScene := projectEntityImageBindings(input.SpecScene, segments, entityImagePolicy)
	return &PostProcessResult{
		VidRushSegments:  segments,
		UpdatedSpecScene: updatedSpecScene,
		SpecSceneChanged: len(updatedSpecScene.Scenes) > 0,
		Warnings:         warnings,
		Changed:          true,
	}, nil
}

// Materialize implements the single-segment materialization boundary consumed
// by the incremental VidRush coordinator. It reuses materializeOne so the
// acquire → verify → finalize stage is implemented exactly once, and it
// returns an immutable result without mutating shared scene state.
func (p *VidRushMaterializationProcessor) Materialize(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	if p == nil {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("vidrush materialization: processor not configured")
	}
	if result, handled := p.metadataOnlyResult(plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{segment}}); handled {
		return result.VidRushSegments[0], nil
	}
	if segment.ExecutionMode.IsFixedMedia() {
		cloned := cloneVidRushSegmentResult(segment)
		cloned.ExecutionMode = scriptpkg.SceneExecutionFixedMedia
		return cloned, nil
	}
	if err := materializationDependenciesError(plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{segment}}, p.providers, p.finalizer); err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	if p.providers == nil || p.finalizer == nil {
		return cloneVidRushSegmentResult(segment), nil
	}
	if err := requireVidRushEnabledProviders(plan, p.providers); err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	if p.log != nil {
		driveFolderID := ""
		planTitle := ""
		planLanguage := ""
		if plan != nil {
			driveFolderID = strings.TrimSpace(plan.DriveFolderID)
			planTitle = plan.Title
			planLanguage = plan.Language
		}
		p.log.Info("VidRush materialization started", zap.String("segment_id", segment.SegmentID), zap.Int("candidates", len(segment.Assets.Candidates)), zap.Int("secondary_images", len(segment.Assets.SecondaryImages)), zap.String("mode", plan.MediaPlan.Materialization.Mode), zap.String("drive_folder_id", driveFolderID), zap.String("plan_title", planTitle), zap.String("plan_language", planLanguage))
	}
	out, err := p.materializeOne(ctx, plan, segment)
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	if p.log != nil {
		p.log.Info("VidRush materialization completed", zap.String("segment_id", segment.SegmentID), zap.Int("selected_images", len(out.result.Assets.SecondaryImages)), zap.Int("materialized_candidates", len(out.result.Assets.Candidates)), zap.Strings("warnings", out.warnings))
	}
	return out.result, nil
}

func (p *VidRushMaterializationProcessor) hydrateEntityCatalogMaterialization(ctx context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptpkg.SegmentAssetCandidate, error) {
	if p == nil || p.catalog == nil || !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
		return candidate, nil
	}
	if rawID := strings.TrimPrefix(strings.TrimSpace(candidate.AssetID), "entity-image-"); rawID != strings.TrimSpace(candidate.AssetID) {
		if candidateID, err := strconv.ParseInt(rawID, 10, 64); err == nil && candidateID > 0 {
			materialization, matErr := p.catalog.GetMaterialization(ctx, candidateID)
			if matErr != nil && !errors.Is(matErr, entitycatalog.ErrCandidateNotFound) {
				return candidate, matErr
			}
			if hydrated, ok := applyEntityImageCatalogMaterialization(candidate, materialization); ok {
				return hydrated, nil
			}
		}
	}
	entityName := strings.TrimSpace(candidate.Entity)
	if entityName == "" || strings.TrimSpace(candidate.SourceURL) == "" {
		return candidate, nil
	}
	identity, err := entitycatalog.CanonicalizePersonName(entityName)
	if err != nil {
		return candidate, nil
	}
	rows, err := p.catalog.ListCandidates(ctx, identity.CanonicalEntityID, 100)
	if err != nil {
		if errors.Is(err, entitycatalog.ErrEntityNotFound) {
			return candidate, nil
		}
		return candidate, err
	}
	for _, row := range rows {
		if !strings.EqualFold(strings.TrimSpace(row.SourceURL), strings.TrimSpace(candidate.SourceURL)) {
			continue
		}
		materialization, matErr := p.catalog.GetMaterialization(ctx, row.ID)
		if matErr != nil {
			return candidate, matErr
		}
		if hydrated, ok := applyEntityImageCatalogMaterialization(candidate, materialization); ok {
			return hydrated, nil
		}
	}
	return candidate, nil
}

func (p *VidRushMaterializationProcessor) persistEntityCatalogMaterialization(ctx context.Context, discovered, persisted scriptpkg.SegmentAssetCandidate) error {
	if p == nil || p.catalog == nil || !strings.EqualFold(strings.TrimSpace(discovered.Provider), scriptpkg.VidRushProviderInternetImages) {
		return nil
	}
	candidateID, err := entityImageCatalogCandidateID(ctx, p.catalog, discovered)
	if err != nil || candidateID < 1 {
		return err
	}
	// A catalog-managed image is reusable only when its canonical materialization
	// is durable. Do not allow a successful VidRush result to hide a missing
	// reference-database row or incomplete Drive/hash identity.
	if strings.TrimSpace(persisted.AssetID) == "" || strings.TrimSpace(persisted.DriveLink) == "" || strings.TrimSpace(persisted.LegacyFileMD5) == "" {
		return fmt.Errorf("entity image catalog: materialized candidate %d is missing asset_id, drive_link, or legacy_file_md5", candidateID)
	}
	now := time.Now().UTC()
	if err := p.catalog.UpsertMaterialization(ctx, entitycatalog.Materialization{
		CandidateID: candidateID, AssetID: persisted.AssetID, LegacyFileMD5: persisted.LegacyFileMD5,
		DriveLink: persisted.DriveLink, LocalPath: persisted.LocalPath,
		Status:         entitycatalog.MaterializationStatusMaterialized,
		MaterializedAt: now, LastVerifiedAt: now,
	}); err != nil {
		return err
	}
	return p.catalog.SetCandidateStatus(ctx, candidateID, entitycatalog.CandidateStatusFresh)
}

// materializeOne materializes one enriched segment: it acquires, verifies and
// finalizes every candidate through the shared provider registry and common
// finalizer, applies the generation fallback, and selects the primary video.
// It is shared by the batch Process path and the single-segment Materialize
// port so the materialization stage is implemented exactly once.
func (p *VidRushMaterializationProcessor) materializeOne(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (vidRushMaterializedSegment, error) {
	updated := cloneVidRushSegmentResult(segment)
	if segment.ExecutionMode.IsFixedMedia() {
		// Fixed media is already authoritative and must not be acquired,
		// verified, persisted, ranked or replaced by this processor.
		updated.ExecutionMode = scriptpkg.SceneExecutionFixedMedia
		return vidRushMaterializedSegment{result: updated}, nil
	}
	var warnings []string
	newInternetImageUploads := 0
	materialize := func(candidates []scriptpkg.SegmentAssetCandidate, targetImages int) ([]scriptpkg.SegmentAssetCandidate, error) {
		materialized := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
		attempts := make(map[string]int, 3)
		readyImageGroups := make(map[string]struct{}, targetImages)
		markReadyImage := func(candidate scriptpkg.SegmentAssetCandidate) {
			if candidate.Provider != scriptpkg.VidRushProviderInternetImages && candidate.Provider != scriptpkg.VidRushProviderImageGeneration {
				return
			}
			if group := vidRushImageGroup(candidate); group != "" {
				readyImageGroups[group] = struct{}{}
			}
		}
		for _, candidate := range candidates {
			// Materialized caches are shared across runs, so a cached artifact is
			// untrusted with respect to the current scene. Never let a stale
			// candidate from another segment enter selection or certification.
			if owner := strings.TrimSpace(candidate.SegmentID); owner != "" && owner != strings.TrimSpace(segment.SegmentID) {
				continue
			}
			isImage := candidate.Provider == scriptpkg.VidRushProviderInternetImages || candidate.Provider == scriptpkg.VidRushProviderImageGeneration
			catalogImage := p.catalog != nil && strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) &&
				(strings.TrimSpace(candidate.Entity) != "" || strings.HasPrefix(strings.TrimSpace(candidate.AssetID), "entity-image-"))
			wasReady := readyVidRushCandidate(candidate)
			if hydrated, hydrationErr := p.hydrateEntityCatalogMaterialization(ctx, candidate); hydrationErr != nil {
				warnings = append(warnings, fmt.Sprintf("vidrush_materialization: entity catalog lookup: %v", hydrationErr))
			} else {
				if catalogImage && !wasReady && readyVidRushCandidate(hydrated) &&
					(strings.TrimSpace(hydrated.DriveLink) != "" || strings.TrimSpace(hydrated.LegacyFileMD5) != "") {
					if catalogMetrics := entityImageCatalogMetricsFor(p.metrics); catalogMetrics != nil {
						catalogMetrics.IncEntityImageCatalogDriveReuse()
					}
				}
				candidate = hydrated
			}
			if isImage && targetImages > 0 && len(readyImageGroups) >= targetImages {
				// Keep the remaining remote hits for diagnostics/candidate-set
				// hashing, but do not download or persist surplus images. This
				// prevents Qdrant/Drive fan-out from exceeding the scene plan.
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
			// A catalog hit is normally terminal and avoids a second download.
			// For an entity image in a run with an explicit Drive output root,
			// the run bundle is also an output contract: reacquire the source so
			// the common finalizer can publish a copy into this job's images
			// folder. Without this exception a warm catalog hit would keep only
			// the old global vidrush link and reproduce the missing-image bug.
			publishToRunOutput := entityImageOutputRequested(plan, candidate)
			if readyVidRushCandidate(candidate) && !publishToRunOutput && (!candidate.IsLegacyCandidate() || legacyPersisted) {
				materialized = append(materialized, candidate)
				markReadyImage(candidate)
				continue
			}
			refreshMaterialized := plan != nil && (plan.ForceRefresh || plan.MediaPlan.ForceRefreshAssets)
			if !refreshMaterialized {
				if key := vidRushCandidateIdentity(candidate); key != "" {
					if cached, ok := vidrushMaterializedCache.Get(key); ok {
						if persisted, ok := cached.(scriptpkg.SegmentAssetCandidate); ok &&
							(strings.TrimSpace(persisted.SegmentID) == "" || strings.TrimSpace(persisted.SegmentID) == strings.TrimSpace(segment.SegmentID)) &&
							readyVidRushCandidate(persisted) {
							materialized = append(materialized, persisted)
							markReadyImage(persisted)
							continue
						}
					}
					var persisted scriptpkg.SegmentAssetCandidate
					if hit, cacheErr := loadVidRushPersistentJSON(ctx, p.cache, "materialized", key, &persisted); cacheErr != nil {
						warnings = append(warnings, fmt.Sprintf("vidrush_materialization: durable cache read %s: %v", key, cacheErr))
					} else if hit &&
						(strings.TrimSpace(persisted.SegmentID) == "" || strings.TrimSpace(persisted.SegmentID) == strings.TrimSpace(segment.SegmentID)) &&
						readyVidRushCandidate(persisted) {
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
			if attempts[providerName] >= vidRushAcquireBudget(plan, providerName) {
				// Preserve the discovered candidate for diagnostics and a future
				// retry, but do not turn every remote search hit into a download.
				materialized = append(materialized, candidate)
				continue
			}
			attempts[providerName]++

			provider, err := p.providers.Provider(providerName)
			if err != nil {
				candidate.AcquisitionStatus = scriptpkg.VidRushStatusFailed
				warnings = append(warnings, fmt.Sprintf("vidrush_materialization: %s provider unavailable for %s: %v", providerName, segment.SegmentID, err))
				materialized = append(materialized, candidate)
				continue
			}
			catalogMaterializationStarted := time.Time{}
			if catalogImage := p.catalog != nil && providerName == scriptpkg.VidRushProviderInternetImages &&
				(strings.TrimSpace(candidate.Entity) != "" || strings.HasPrefix(strings.TrimSpace(candidate.AssetID), "entity-image-")); catalogImage {
				if catalogMetrics := entityImageCatalogMetricsFor(p.metrics); catalogMetrics != nil {
					catalogMetrics.IncEntityImageCatalogNewDownload()
					catalogMaterializationStarted = time.Now()
				}
			}
			lifecycle := acquireAndVerify(ctx, provider, candidate, providerName, p.metrics)
			if lifecycle.err != nil && lifecycle.stage == "acquire" {
				err = lifecycle.err
				candidate = lifecycle.candidate
				if catalogMaterializationStarted.IsZero() == false {
					if catalogMetrics := entityImageCatalogMetricsFor(p.metrics); catalogMetrics != nil {
						catalogMetrics.IncEntityImageCatalogURLBroken()
					}
					observeEntityImageCatalogMaterialization(p.metrics, catalogMaterializationStarted)
				}
				if statusErr := setEntityImageCatalogCandidateStatus(ctx, p.catalog, candidate, entitycatalog.CandidateStatusBroken); statusErr != nil {
					warnings = append(warnings, fmt.Sprintf("vidrush_materialization: mark broken URL: %v", statusErr))
				}
				warnings = append(warnings, fmt.Sprintf("vidrush_materialization: acquire %s for %s: %v", providerName, segment.SegmentID, err))
				materialized = append(materialized, candidate)
				continue
			}
			if lifecycle.err != nil {
				err = lifecycle.err
				candidate = lifecycle.candidate
				if !catalogMaterializationStarted.IsZero() {
					if catalogMetrics := entityImageCatalogMetricsFor(p.metrics); catalogMetrics != nil {
						catalogMetrics.IncEntityImageCatalogURLBroken()
					}
					observeEntityImageCatalogMaterialization(p.metrics, catalogMaterializationStarted)
				}
				if statusErr := setEntityImageCatalogCandidateStatus(ctx, p.catalog, candidate, entitycatalog.CandidateStatusBroken); statusErr != nil {
					warnings = append(warnings, fmt.Sprintf("vidrush_materialization: mark broken URL: %v", statusErr))
				}
				warnings = append(warnings, fmt.Sprintf("vidrush_materialization: verify %s for %s: %v", providerName, segment.SegmentID, err))
				materialized = append(materialized, candidate)
				continue
			}
			verified := lifecycle.verified
			verified = routeEntityImageToGenerationOutput(plan, verified)
			cacheKey := vidRushCandidateIdentity(candidate)
			var persisted scriptpkg.SegmentAssetCandidate
			err = measureVidRushProvider(ctx, p.metrics, kernobs.OperationInfo{
				Stage: kernobs.StagePersist, Component: "vidrush", Operation: "finalize", Provider: providerName,
			}, func(callCtx context.Context) error {
				var finalizeErr error
				persisted, finalizeErr = p.finalizer.Finalize(callCtx, verified)
				return finalizeErr
			})
			if err != nil {
				verified.Candidate.PersistenceStatus = scriptpkg.VidRushStatusFailed
				observeEntityImageCatalogMaterialization(p.metrics, catalogMaterializationStarted)
				warnings = append(warnings, fmt.Sprintf("vidrush_materialization: finalize %s for %s: %v", providerName, segment.SegmentID, err))
				materialized = append(materialized, verified.Candidate)
				continue
			}
			materialized = append(materialized, persisted)
			if providerName == scriptpkg.VidRushProviderInternetImages {
				newInternetImageUploads++
			}
			observeEntityImageCatalogMaterialization(p.metrics, catalogMaterializationStarted)
			if catalogErr := p.persistEntityCatalogMaterialization(ctx, candidate, persisted); catalogErr != nil {
				return nil, fmt.Errorf("vidrush_materialization: entity catalog materialization: %w", catalogErr)
			}
			if isImage && readyVidRushCandidate(persisted) {
				markReadyImage(persisted)
			}
			if key := vidRushCandidateIdentity(persisted); key != "" && strings.TrimSpace(persisted.PersistenceStatus) == scriptpkg.VidRushStatusPersisted && strings.TrimSpace(persisted.DriveLink) != "" {
				vidrushMaterializedCache.Put(key, persisted)
				if cacheErr := storeVidRushPersistentJSON(ctx, p.cache, "materialized", key, persisted); cacheErr != nil {
					warnings = append(warnings, fmt.Sprintf("vidrush_materialization: durable cache write %s: %v", key, cacheErr))
				}
			}
			if cacheKey != "" && strings.TrimSpace(persisted.PersistenceStatus) == scriptpkg.VidRushStatusPersisted && strings.TrimSpace(persisted.DriveLink) != "" {
				vidrushMaterializedCache.Put(cacheKey, persisted)
				if cacheErr := storeVidRushPersistentJSON(ctx, p.cache, "materialized", cacheKey, persisted); cacheErr != nil {
					warnings = append(warnings, fmt.Sprintf("vidrush_materialization: durable cache write %s: %v", cacheKey, cacheErr))
				}
			}
		}
		return materialized, nil
	}

	// Search candidates must be acquired and verified before deciding how
	// many generated images are actually missing. This keeps generation a
	// true fallback instead of a parallel source that duplicates valid web
	// assets.
	imageTarget := vidRushImageTarget(plan)
	discoveredCandidates := prioritizeExactVidRushImageCandidates(updated.Assets.Candidates, imageTarget, plan)
	var materializeErr error
	updated.Assets.Candidates, materializeErr = materialize(discoveredCandidates, imageTarget)
	if materializeErr != nil {
		return vidRushMaterializedSegment{}, materializeErr
	}
	generationCandidates, generationState := p.planGenerationFallback(plan, updated)
	updated.Cache.ImageGeneration = generationState
	if len(generationCandidates) > 0 {
		// Only the newly planned fallback candidates need a second
		// acquisition pass. Replaying the already attempted web candidates
		// here would duplicate downloads after a generation fallback.
		generated, materializeErr := materialize(generationCandidates, len(generationCandidates))
		if materializeErr != nil {
			return vidRushMaterializedSegment{}, materializeErr
		}
		updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, generated)
	}
	materialized := updated.Assets.Candidates
	updated.Assets.SecondaryImages = selectExactVidRushImages(materialized, imageTarget, plan)
	updated.Assets.GeneratedImages = filterVidRushGeneratedImages(updated.Assets.SecondaryImages)
	if imageTarget > 0 && len(updated.Assets.SecondaryImages) != imageTarget {
		warnings = append(warnings, fmt.Sprintf(
			"FAILED_REQUIRED_IMAGE_COUNT: required=%d verified=%d segment=%s",
			imageTarget, len(updated.Assets.SecondaryImages), segment.SegmentID,
		))
	}
	profile := updated.CanonicalSemanticProfile()
	if p.sampler != nil {
		updated.Assets.PrimaryVideo = p.selectPrimaryWithMediaSampler(ctx, materialized, profile)
	} else {
		// Selection is fail-closed when the canonical sampler is absent. The
		// legacy VidRush ranker is not a production fallback; wiring must
		// provide MediaSampler before a primary can be bound.
		updated.Assets.PrimaryVideo = nil
	}
	if vidRushArtlistOnlyPlan(plan) && updated.Assets.PrimaryVideo == nil {
		diagnostics := make([]string, 0, min(len(materialized), 3))
		diagnostics = vidRushArtlistDiagnostics(materialized)
		if len(diagnostics) == 0 {
			providers := make(map[string]int)
			for _, candidate := range discoveredCandidates {
				providers[candidate.Provider]++
			}
			diagnostics = append(diagnostics, fmt.Sprintf("discovered=%d providers=%v; no Artlist candidates reached materialization", len(discoveredCandidates), providers))
		}
		return vidRushMaterializedSegment{}, fmt.Errorf(
			"vidrush materialization: required persisted Artlist primary unavailable for segment %s (%s)",
			segment.SegmentID, strings.Join(diagnostics, "; "),
		)
	}
	updated.Assets.CandidateSetHash = candidateSetHash(materialized)
	// Numeric new-upload counter: 0 when the catalog Drive materialization is
	// reused, 1 per freshly finalized internet_images candidate otherwise.
	updated.Cache.InternetImagesNewUploads = newInternetImageUploads
	return vidRushMaterializedSegment{result: updated, warnings: warnings}, nil
}

// routeEntityImageToGenerationOutput carries the generation destination all
