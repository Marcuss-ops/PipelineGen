package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
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
	vidRushArtlistAcquireTimeout    = 25 * time.Second
	vidRushImageAcquireTimeout      = 15 * time.Second
	vidRushGenerationAcquireTimeout = 2 * time.Minute
	vidRushVerifyTimeout            = 20 * time.Second
	// Search providers routinely return a mixture of hotlink-protected,
	// corrupt and valid URLs. Keep the candidate set bounded upstream, but
	// allow enough acquisition attempts to reach the requested number of
	// durable verified images without ever promoting an unverified hit.
	vidRushImageAcquireSlack = 8
)

func NewVidRushMaterializationProcessor(providers *VidRushAssetProviderRegistry, finalizer scriptports.VidRushArtifactFinalizer, metrics ...VidRushTimingMetrics) *VidRushMaterializationProcessor {
	return NewVidRushMaterializationProcessorWithCache(providers, finalizer, nil, metrics...)
}

func NewVidRushMaterializationProcessorWithCache(providers *VidRushAssetProviderRegistry, finalizer scriptports.VidRushArtifactFinalizer, cache scriptports.VidRushCachePort, metrics ...VidRushTimingMetrics) *VidRushMaterializationProcessor {
	var m VidRushTimingMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &VidRushMaterializationProcessor{providers: providers, finalizer: finalizer, cache: cache, metrics: m}
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
		updated := cloneVidRushSegmentResult(segment)
		var warnings []string
		materialize := func(candidates []scriptpkg.SegmentAssetCandidate, targetImages int) []scriptpkg.SegmentAssetCandidate {
			materialized := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
			attempts := make(map[string]int, 3)
			readyImages := 0
			for _, candidate := range candidates {
				isImage := candidate.Provider == scriptpkg.VidRushProviderInternetImages || candidate.Provider == scriptpkg.VidRushProviderImageGeneration
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
				acquireCtx, cancelAcquire := context.WithTimeout(ctx, vidRushProviderTimeout(providerName))
				acquireStart := time.Now()
				local, err := provider.Acquire(acquireCtx, candidate)
				observeVidRushProviderDuration(p.metrics, providerName+"_acquire", time.Since(acquireStart))
				cancelAcquire()
				if err != nil {
					candidate.AcquisitionStatus = scriptpkg.VidRushStatusFailed
					warnings = append(warnings, fmt.Sprintf("vidrush_materialization: acquire %s for %s: %v", providerName, segment.SegmentID, err))
					materialized = append(materialized, candidate)
					continue
				}
				verifyCtx, cancelVerify := context.WithTimeout(ctx, vidRushVerifyTimeout)
				verifyStart := time.Now()
				verified, err := provider.Verify(verifyCtx, local)
				observeVidRushProviderDuration(p.metrics, providerName+"_verify", time.Since(verifyStart))
				cancelVerify()
				if err != nil {
					candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
					candidate.VerificationStatus = scriptpkg.VidRushStatusFailed
					warnings = append(warnings, fmt.Sprintf("vidrush_materialization: verify %s for %s: %v", providerName, segment.SegmentID, err))
					materialized = append(materialized, candidate)
					continue
				}
				cacheKey := vidRushCandidateIdentity(candidate)
				finalizeStart := time.Now()
				persisted, err := p.finalizer.Finalize(ctx, verified)
				observeVidRushProviderDuration(p.metrics, "vidrush_finalize", time.Since(finalizeStart))
				if err != nil {
					verified.Candidate.PersistenceStatus = scriptpkg.VidRushStatusFailed
					warnings = append(warnings, fmt.Sprintf("vidrush_materialization: finalize %s for %s: %v", providerName, segment.SegmentID, err))
					materialized = append(materialized, verified.Candidate)
					continue
				}
				materialized = append(materialized, persisted)
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
		updated.Assets.Candidates = materialize(updated.Assets.Candidates, imageTarget)
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
			return vidRushMaterializedSegment{}, fmt.Errorf(
				"vidrush materialization: required persisted Artlist primary unavailable for segment %s",
				segment.SegmentID,
			)
		}
		updated.Assets.CandidateSetHash = candidateSetHash(materialized)
		return vidRushMaterializedSegment{result: updated, warnings: warnings}, nil
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

	return &PostProcessResult{VidRushSegments: segments, Warnings: warnings, Changed: true}, nil
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
