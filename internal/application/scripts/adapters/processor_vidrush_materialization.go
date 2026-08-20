package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
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

func NewVidRushMaterializationProcessorWithCache(providers *VidRushAssetProviderRegistry, finalizer scriptports.VidRushArtifactFinalizer, cache scriptports.VidRushCachePort, metrics ...VidRushTimingMetrics) *VidRushMaterializationProcessor {
	return NewVidRushMaterializationProcessorWithCatalog(providers, finalizer, cache, nil, metrics...)
}

func NewVidRushMaterializationProcessorWithCatalog(providers *VidRushAssetProviderRegistry, finalizer scriptports.VidRushArtifactFinalizer, cache scriptports.VidRushCachePort, catalog entitycatalog.Repository, metrics ...VidRushTimingMetrics) *VidRushMaterializationProcessor {
	var m VidRushTimingMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &VidRushMaterializationProcessor{providers: providers, finalizer: finalizer, cache: cache, catalog: catalog, metrics: m}
}

func (p *VidRushMaterializationProcessor) Name() ProcessorName {
	return ProcessorVidRushMaterialization
}

func (p *VidRushMaterializationProcessor) Policy(plan *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	if plan != nil && (plan.MediaPlan.ProviderPolicy.Artlist.AsBool() ||
		plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() ||
		plan.MediaPlan.ProviderPolicy.ImageGeneration.AsBool()) {
		return ProcessorRequired
	}
	return ProcessorBestEffort
}

func (p *VidRushMaterializationProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p == nil {
		return nil, fmt.Errorf("vidrush materialization: processor not configured")
	}
	if plan != nil && plan.MediaPlan.Materialization.Mode == mediadomain.MaterializationMetadataOnly {
		segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input.VidRushSegments))
		for _, segment := range input.VidRushSegments {
			segments = append(segments, cloneVidRushSegmentResult(segment))
		}
		return &PostProcessResult{VidRushSegments: segments, Changed: len(segments) > 0}, nil
	}
	if p.providers == nil || p.finalizer == nil {
		if vidRushMaterializationRequested(plan, input) {
			return nil, fmt.Errorf("vidrush materialization: provider registry and common finalizer are required")
		}
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
	if plan != nil && plan.MediaPlan.Materialization.Mode == mediadomain.MaterializationMetadataOnly {
		return cloneVidRushSegmentResult(segment), nil
	}
	if p.providers == nil || p.finalizer == nil {
		if vidRushMaterializationRequested(plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{segment}}) {
			return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("vidrush materialization: provider registry and common finalizer are required")
		}
		return cloneVidRushSegmentResult(segment), nil
	}
	if err := requireVidRushEnabledProviders(plan, p.providers); err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	out, err := p.materializeOne(ctx, plan, segment)
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
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
	if err != nil || candidateID < 1 || strings.TrimSpace(persisted.DriveLink) == "" || strings.TrimSpace(persisted.FileHash) == "" {
		return err
	}
	now := time.Now().UTC()
	if err := p.catalog.UpsertMaterialization(ctx, entitycatalog.Materialization{
		CandidateID: candidateID, AssetID: persisted.AssetID, FileHash: persisted.FileHash,
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
	var warnings []string
	newInternetImageUploads := 0
	materialize := func(candidates []scriptpkg.SegmentAssetCandidate, targetImages int) []scriptpkg.SegmentAssetCandidate {
		materialized := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
		attempts := make(map[string]int, 3)
		readyImages := 0
		for _, candidate := range candidates {
			isImage := candidate.Provider == scriptpkg.VidRushProviderInternetImages || candidate.Provider == scriptpkg.VidRushProviderImageGeneration
			catalogImage := p.catalog != nil && strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) &&
				(strings.TrimSpace(candidate.Entity) != "" || strings.HasPrefix(strings.TrimSpace(candidate.AssetID), "entity-image-"))
			wasReady := readyVidRushCandidate(candidate)
			if hydrated, hydrationErr := p.hydrateEntityCatalogMaterialization(ctx, candidate); hydrationErr != nil {
				warnings = append(warnings, fmt.Sprintf("vidrush_materialization: entity catalog lookup: %v", hydrationErr))
			} else {
				if catalogImage && !wasReady && readyVidRushCandidate(hydrated) &&
					(strings.TrimSpace(hydrated.DriveLink) != "" || strings.TrimSpace(hydrated.FileHash) != "") {
					if catalogMetrics := entityImageCatalogMetricsFor(p.metrics); catalogMetrics != nil {
						catalogMetrics.IncEntityImageCatalogDriveReuse()
					}
				}
				candidate = hydrated
			}
			if isImage && targetImages > 0 && readyImages >= targetImages {
				// Keep the remaining remote hits for diagnostics/candidate-set
				// hashing, but do not download or persist surplus images. This
				// prevents Qdrant/Drive fan-out from exceeding the scene plan.
				materialized = append(materialized, candidate)
				continue
			}
			legacyPersisted := candidate.IsLegacyCandidate() && strings.TrimSpace(candidate.DriveLink) != ""
			if readyVidRushCandidate(candidate) && (!candidate.IsLegacyCandidate() || legacyPersisted) {
				materialized = append(materialized, candidate)
				if isImage {
					readyImages++
				}
				continue
			}
			if key := vidRushCandidateIdentity(candidate); key != "" {
				if cached, ok := vidrushMaterializedCache.Load(key); ok {
					if persisted, ok := cached.(scriptpkg.SegmentAssetCandidate); ok && readyVidRushCandidate(persisted) {
						materialized = append(materialized, persisted)
						if isImage {
							readyImages++
						}
						continue
					}
				}
				var persisted scriptpkg.SegmentAssetCandidate
				if hit, cacheErr := loadVidRushPersistentJSON(ctx, p.cache, "materialized", key, &persisted); cacheErr != nil {
					warnings = append(warnings, fmt.Sprintf("vidrush_materialization: durable cache read %s: %v", key, cacheErr))
				} else if hit && readyVidRushCandidate(persisted) {
					materialized = append(materialized, persisted)
					vidrushMaterializedCache.Store(key, persisted)
					if isImage {
						readyImages++
					}
					continue
				}
			}
			providerName := strings.ToLower(strings.TrimSpace(candidate.Provider))
			if providerName != scriptpkg.VidRushProviderArtlist && providerName != scriptpkg.VidRushProviderInternetImages && providerName != scriptpkg.VidRushProviderImageGeneration {
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
			acquireCtx, cancelAcquire := context.WithTimeout(ctx, vidRushProviderTimeout(providerName))
			var local scriptports.LocalArtifact
			err = measureVidRushProvider(acquireCtx, p.metrics, kernobs.OperationInfo{
				Stage: kernobs.StageAcquire, Component: "vidrush", Operation: "acquire", Provider: providerName,
			}, func(callCtx context.Context) error {
				var acquireErr error
				local, acquireErr = provider.Acquire(callCtx, candidate)
				return acquireErr
			})
			cancelAcquire()
			if err != nil {
				candidate.AcquisitionStatus = scriptpkg.VidRushStatusFailed
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
			verifyCtx, cancelVerify := context.WithTimeout(ctx, vidRushVerifyTimeout)
			var verified scriptports.VerifiedArtifact
			err = measureVidRushProvider(verifyCtx, p.metrics, kernobs.OperationInfo{
				Stage: kernobs.StageVerify, Component: "vidrush", Operation: "verify", Provider: providerName,
			}, func(callCtx context.Context) error {
				var verifyErr error
				verified, verifyErr = provider.Verify(callCtx, local)
				return verifyErr
			})
			cancelVerify()
			if err != nil {
				candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
				candidate.VerificationStatus = scriptpkg.VidRushStatusFailed
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
				warnings = append(warnings, fmt.Sprintf("vidrush_materialization: entity catalog materialization: %v", catalogErr))
			}
			if isImage && readyVidRushCandidate(persisted) {
				readyImages++
			}
			if key := vidRushCandidateIdentity(persisted); key != "" && strings.TrimSpace(persisted.PersistenceStatus) == scriptpkg.VidRushStatusPersisted && strings.TrimSpace(persisted.DriveLink) != "" {
				vidrushMaterializedCache.Store(key, persisted)
				if cacheErr := storeVidRushPersistentJSON(ctx, p.cache, "materialized", key, persisted); cacheErr != nil {
					warnings = append(warnings, fmt.Sprintf("vidrush_materialization: durable cache write %s: %v", key, cacheErr))
				}
			}
			if cacheKey != "" && strings.TrimSpace(persisted.PersistenceStatus) == scriptpkg.VidRushStatusPersisted && strings.TrimSpace(persisted.DriveLink) != "" {
				vidrushMaterializedCache.Store(cacheKey, persisted)
				if cacheErr := storeVidRushPersistentJSON(ctx, p.cache, "materialized", cacheKey, persisted); cacheErr != nil {
					warnings = append(warnings, fmt.Sprintf("vidrush_materialization: durable cache write %s: %v", cacheKey, cacheErr))
				}
			}
		}
		return materialized
	}

	// Search candidates must be acquired and verified before deciding how
	// many generated images are actually missing. This keeps generation a
	// true fallback instead of a parallel source that duplicates valid web
	// assets.
	imageTarget := vidRushImageTarget(plan)
	discoveredCandidates := append([]scriptpkg.SegmentAssetCandidate(nil), updated.Assets.Candidates...)
	updated.Assets.Candidates = materialize(discoveredCandidates, imageTarget)
	generationCandidates, generationState := p.planGenerationFallback(plan, updated)
	updated.Cache.ImageGeneration = generationState
	if len(generationCandidates) > 0 {
		// Only the newly planned fallback candidates need a second
		// acquisition pass. Replaying the already attempted web candidates
		// here would duplicate downloads after a generation fallback.
		generated := materialize(generationCandidates, len(generationCandidates))
		updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, generated)
	}
	materialized := updated.Assets.Candidates
	updated.Assets.SecondaryImages = durableVidRushImages(materialized)
	updated.Assets.GeneratedImages = filterVidRushGeneratedImages(materialized)
	if imageTarget > 0 && len(updated.Assets.SecondaryImages) < imageTarget {
		warnings = append(warnings, fmt.Sprintf(
			"FAILED_REQUIRED_IMAGE_COUNT: required=%d verified=%d segment=%s",
			imageTarget, len(updated.Assets.SecondaryImages), segment.SegmentID,
		))
	}
	updated.Assets.PrimaryVideo = nil
	for i := range materialized {
		candidate := materialized[i]
		if candidate.Provider != scriptpkg.VidRushProviderArtlist || !readyVidRushCandidate(candidate) {
			continue
		}
		if updated.Assets.PrimaryVideo == nil || ScoreVidRushCandidate(candidate, false) > ScoreVidRushCandidate(*updated.Assets.PrimaryVideo, false) {
			selected := candidate
			selected.SelectionReason = "highest ranked verified and persisted video"
			updated.Assets.PrimaryVideo = &selected
		}
	}
	if vidRushArtlistOnlyPlan(plan) && updated.Assets.PrimaryVideo == nil {
		diagnostics := make([]string, 0, minInt(len(materialized), 3))
		for _, candidate := range materialized {
			if candidate.Provider != scriptpkg.VidRushProviderArtlist {
				continue
			}
			diagnostics = append(diagnostics, fmt.Sprintf("asset=%s acquire=%s verify=%s persist=%s source=%t page=%t drive=%t", candidate.AssetID, candidate.AcquisitionStatus, candidate.VerificationStatus, candidate.PersistenceStatus, strings.TrimSpace(candidate.SourceURL) != "", strings.TrimSpace(candidate.SourcePageURL) != "", strings.TrimSpace(candidate.DriveLink) != ""))
			if len(diagnostics) == 3 {
				break
			}
		}
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

func vidRushProviderTimeout(provider string) time.Duration {
	switch provider {
	case scriptpkg.VidRushProviderArtlist:
		return vidRushArtlistAcquireTimeout
	case scriptpkg.VidRushProviderInternetImages:
		return vidRushImageAcquireTimeout
	case scriptpkg.VidRushProviderImageGeneration:
		return vidRushGenerationAcquireTimeout
	default:
		return vidRushImageAcquireTimeout
	}
}

func vidRushMaterializationRequested(plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) bool {
	if plan != nil && (plan.MediaPlan.ProviderPolicy.Artlist.AsBool() ||
		plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() ||
		plan.MediaPlan.ProviderPolicy.ImageGeneration.AsBool()) {
		return true
	}
	for _, segment := range input.VidRushSegments {
		for _, candidate := range segment.Assets.Candidates {
			if scriptpkg.IsVidRushProvider(candidate.Provider) {
				return true
			}
		}
	}
	return false
}

// requireVidRushEnabledProviders makes capability availability explicit at
// the materialization boundary. A plan that enables a provider must never
// complete successfully with an empty result merely because composition did
// not register that provider.
func requireVidRushEnabledProviders(plan *scriptpkg.ResolvedGenerationPlan, registry *VidRushAssetProviderRegistry) error {
	if plan == nil || registry == nil {
		return nil
	}
	checks := []struct {
		name    string
		enabled bool
	}{
		{name: scriptpkg.VidRushProviderArtlist, enabled: plan.MediaPlan.ProviderPolicy.Artlist.AsBool()},
		{name: scriptpkg.VidRushProviderInternetImages, enabled: plan.MediaPlan.ProviderPolicy.InternetImages.AsBool()},
		{name: scriptpkg.VidRushProviderImageGeneration, enabled: plan.MediaPlan.ProviderPolicy.ImageGeneration.AsBool()},
	}
	for _, check := range checks {
		if !check.enabled {
			continue
		}
		if _, err := registry.Provider(check.name); err != nil {
			return fmt.Errorf("vidrush materialization: provider %q is enabled but unavailable: %w", check.name, err)
		}
	}
	return nil
}

func vidRushAcquireBudget(plan *scriptpkg.ResolvedGenerationPlan, provider string) int {
	switch provider {
	case scriptpkg.VidRushProviderArtlist:
		return vidRushArtlistAcquireBudget
	case scriptpkg.VidRushProviderInternetImages:
		target := vidRushImageTarget(plan)
		if target == 0 {
			target = vidRushDefaultImagesPerScene
		}
		return target + vidRushImageAcquireSlack
	case scriptpkg.VidRushProviderImageGeneration:
		if target := vidRushImageTarget(plan); target > 0 {
			return target
		}
		return vidRushDefaultImagesPerScene
	default:
		return 0
	}
}

func (p *VidRushMaterializationProcessor) planGenerationFallback(plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) ([]scriptpkg.SegmentAssetCandidate, string) {
	if plan == nil || !plan.MediaPlan.ProviderPolicy.ImageGeneration.AsBool() {
		return nil, "BYPASSED"
	}
	targetImages := vidRushImageTarget(plan)
	verified := 0
	for _, candidate := range segment.Assets.Candidates {
		if (candidate.Provider == scriptpkg.VidRushProviderInternetImages || candidate.Provider == scriptpkg.VidRushProviderImageGeneration) && readyVidRushCandidate(candidate) {
			verified++
		}
	}
	missing := targetImages - verified
	if missing <= 0 {
		return nil, "HIT_EXACT"
	}
	out := make([]scriptpkg.SegmentAssetCandidate, 0, missing)
	for i := 0; i < missing; i++ {
		prompt := strings.TrimSpace(segment.Text)
		if prompt == "" {
			prompt = strings.Join(segment.Insights.ImageQueries, ", ")
		}
		key := VidRushGenerationCacheKey(VidRushGenerationRequest{
			SegmentTextHash: segment.TextHash, Prompt: prompt, Style: "cinematic",
			Width: 1920, Height: 1080, Provider: scriptpkg.VidRushProviderImageGeneration,
			PromptVersion: plan.PromptVersion, TargetImages: targetImages,
		})
		out = append(out, scriptpkg.SegmentAssetCandidate{
			AssetID: key + fmt.Sprintf("-%d", i), Provider: scriptpkg.VidRushProviderImageGeneration,
			Query: prompt, Score: 1, RelevanceScore: 1, TechnicalQualityScore: 1,
			RightsScore: 1, DiversityScore: 1, ProviderReliability: 1,
			RightsStatus: "verified", AcquisitionStatus: scriptpkg.VidRushStatusCandidateFound,
		})
	}
	return out, "MISS"
}

const vidRushDefaultImagesPerScene = 2

func vidRushImageTarget(plan *scriptpkg.ResolvedGenerationPlan) int {
	if plan == nil {
		return 0
	}
	if plan.ImagesPerScene > 0 {
		return plan.ImagesPerScene
	}
	if plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() || plan.MediaPlan.ProviderPolicy.ImageGeneration.AsBool() {
		return vidRushDefaultImagesPerScene
	}
	return 0
}

func durableVidRushImages(candidates []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Provider != scriptpkg.VidRushProviderInternetImages && candidate.Provider != scriptpkg.VidRushProviderImageGeneration {
			continue
		}
		if readyVidRushCandidate(candidate) {
			out = append(out, candidate)
		}
	}
	return out
}
