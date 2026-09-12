// Package adapters — processor_vidrush_entity_images.go: the entity-image
// routing/selection helpers of the VidRush materialization processor
// (extracted 2026-09-12 from processor_vidrush_materialization.go to keep
// both halves under max_lines_per_file_strict=600, godlike/08).
package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	filesystem "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// second ad-hoc Drive client: the finalizer remains the only publication
// boundary, while this small projection tells it where this run's image
// bundle belongs.
func routeEntityImageToGenerationOutput(plan *scriptpkg.ResolvedGenerationPlan, artifact scriptports.VerifiedArtifact) scriptports.VerifiedArtifact {
	if plan == nil || strings.TrimSpace(plan.DriveFolderID) == "" || !isEntityImageCandidate(artifact.Candidate) {
		return artifact
	}
	artifact.OutputDriveFolderID = strings.TrimSpace(plan.DriveFolderID)
	artifact.OutputDriveSubpath = []string{
		filesystem.SafeFolderName(plan.Title),
		filesystem.SafeFolderName(plan.Language),
		"images",
	}
	return artifact
}

func entityImageOutputRequested(plan *scriptpkg.ResolvedGenerationPlan, candidate scriptpkg.SegmentAssetCandidate) bool {
	return plan != nil && strings.TrimSpace(plan.DriveFolderID) != "" &&
		strings.TrimSpace(candidate.SourceURL) != "" && isEntityImageCandidate(candidate)
}

func isEntityImageCandidate(candidate scriptpkg.SegmentAssetCandidate) bool {
	if candidate.Provider != scriptpkg.VidRushProviderInternetImages && candidate.Provider != scriptpkg.VidRushProviderImageGeneration {
		return false
	}
	return strings.TrimSpace(candidate.Entity) != "" || strings.HasPrefix(strings.TrimSpace(candidate.AssetID), "entity-image-")
}

func (p *VidRushMaterializationProcessor) selectPrimaryWithMediaSampler(ctx context.Context, candidates []scriptpkg.SegmentAssetCandidate, profile scriptpkg.SegmentSemanticProfile) *scriptpkg.SegmentAssetCandidate {
	eligible := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if (candidate.Provider == scriptpkg.VidRushProviderArtlist || candidate.Provider == scriptpkg.VidRushProviderYouTube) && readyVidRushCandidate(candidate) {
			eligible = append(eligible, candidate)
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	visual := scriptpkg.BuildSegmentVisualProfile(profile)
	winnerID, err := p.sampler.Sample(ctx, profile.SegmentID, visual.Subject, visual.Terms, eligible, false)
	if err != nil || strings.TrimSpace(winnerID) == "" {
		return nil
	}
	for i := range eligible {
		if eligible[i].AssetID == winnerID {
			winner := eligible[i]
			winner.SelectionReason = "MediaSampler selected verified candidate"
			return &winner
		}
	}
	return nil
}

func vidRushMaterializationRequested(plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) bool {
	if plan != nil && (providerEnabledForVidRush(plan, scriptpkg.VidRushProviderArtlist) ||
		providerEnabledForVidRush(plan, scriptpkg.VidRushProviderInternetImages) ||
		providerEnabledForVidRush(plan, scriptpkg.VidRushProviderImageGeneration)) {
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
		{name: scriptpkg.VidRushProviderYouTube, enabled: plan.MediaPlan.ProviderPolicy.YouTube.AsBool()},
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

func (p *VidRushMaterializationProcessor) planGenerationFallback(plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) ([]scriptpkg.SegmentAssetCandidate, string) {
	if plan == nil || !providerEnabledForVidRush(plan, scriptpkg.VidRushProviderImageGeneration) {
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
