// Package adapters — processor_vidrush_entity_images.go: the entity-image
// routing/selection helpers of the VidRush materialization processor
// (extracted 2026-09-12 from processor_vidrush_materialization.go to keep
// both halves under max_lines_per_file_strict=600, godlike/08).
package adapters

import (
	"context"
	"fmt"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	filesystem "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// The run bundle layout below plan.DriveFolderID has ONE owner: this file.
// Every artifact family that BELONGS TO THE RUN lands in its own child folder
// so a run's clips can never mix with anything else:
//
//	<Title>/<Language>/clips   — provider clips the run actually used (youtube, artlist)
//
// Entity images (internet_images, image_generation) are deliberately NOT part
// of the run bundle. They belong to the canonical image library
//
//	<ImagesRootFolder>/<canonical-entity-slug>/<file>
//
// which outlives the run and is reused by every future video: an image is
// downloaded once and thereafter served from Drive. Routing them under
// <Title>/<Language>/images made the SAME entity image land once per run (one
// copy per title/language) instead of once for the library, and a run whose
// title matched the entity produced the <slug>/<slug>/<file> nesting. The
// library destination is applied by the finalizer
// (app/wiring/vidrush::vidRushArtifactFinalizer), which owns the Drive roots;
// see routeEntityImageToCanonicalLibrary for the half this file owns.
//
// This is a projection, not a second Drive client: the finalizer remains the
// only publication boundary and this code merely tells it where the artifact
// belongs.
const generationClipChildFolder = "clips"

// routeGenerationOutputToPlanBundle is the SINGLE dispatch point for every
// verified artifact, so a new artifact family adds a predicate here instead of
// a second call site. The families are disjoint by construction — an entity
// image is never a provider clip — so exactly one projection can apply.
func routeGenerationOutputToPlanBundle(plan *scriptpkg.ResolvedGenerationPlan, artifact scriptports.VerifiedArtifact) scriptports.VerifiedArtifact {
	if isEntityImageCandidate(artifact.Candidate) {
		return routeEntityImageToCanonicalLibrary(artifact)
	}
	return routeClipToGenerationOutput(plan, artifact)
}

// routeEntityImageToCanonicalLibrary keeps an entity image OUT of the run
// bundle. The positive half of the library destination (the dedicated images
// root plus the canonical-entity SubFolder) is applied by the finalizer, which
// owns the Drive contract and the root configuration; the half owned here is
// the negative one — clearing any run-scoped destination a caller may have set,
// so an entity image can never be published as <Title>/<Language>/images again.
func routeEntityImageToCanonicalLibrary(artifact scriptports.VerifiedArtifact) scriptports.VerifiedArtifact {
	artifact.OutputDriveFolderID = ""
	artifact.OutputDriveSubpath = nil
	return artifact
}

// EntityImageDriveLeaf returns the canonical per-image Drive leaf of an entity
// image — the SINGLE folder level below the images root — or "" when the
// artifact is not an entity image (or carries no usable entity name).
//
// The leaf is derived through the canonical identity chain, never from the
// plan/job:
//
//	"Michael Jordan" → CanonicalizePersonName → "person:michael-jordan"
//	                 → CanonicalEntitySlug   → "michael-jordan"
//
// so casing and whitespace variants converge on ONE folder, and two videos that
// ask for the same person receive the same image. The finalizer's Drive subpath
// and the folder name both come from here: one owner, one derivation. A blank
// result is not an error — the caller falls back to the generic per-image
// destination, which still yields a single folder level and never the run
// bundle.
func EntityImageDriveLeaf(candidate scriptpkg.SegmentAssetCandidate) string {
	if !isEntityImageCandidate(candidate) {
		return ""
	}
	name := strings.TrimSpace(candidate.Entity)
	if name == "" {
		return ""
	}
	identity, err := entitycatalog.CanonicalizePersonName(name)
	if err != nil {
		return ""
	}
	leaf := capabilityentities.CanonicalEntitySlug(identity.CanonicalEntityID)
	if leaf == "" {
		return ""
	}
	return filesystem.SafeFolderName(leaf)
}

// routeClipToGenerationOutput carries the generation destination for a CLIP
// the run actually used. A source.type=clips job declares its clips by id and
// the materialization loop resolves them through the youtube/artlist
// providers; without this projection those clips were finalized only to their
// own catalog location and never appeared under the run's folder, so an
// operator could not see which clips a job consumed. Mirrors
// routeEntityImageToGenerationOutput exactly, with the clips child folder and
// the same fail-closed guard on a missing DriveFolderID.
func routeClipToGenerationOutput(plan *scriptpkg.ResolvedGenerationPlan, artifact scriptports.VerifiedArtifact) scriptports.VerifiedArtifact {
	if plan == nil || strings.TrimSpace(plan.DriveFolderID) == "" || !isGenerationClipCandidate(artifact.Candidate) {
		return artifact
	}
	return routeToGenerationBundle(plan, artifact, generationClipChildFolder)
}

// routeToGenerationBundle applies the canonical <Title>/<Language>/<family>
// child path. It is the one place that knows the bundle shape, so every family
// shares a single path builder.
func routeToGenerationBundle(plan *scriptpkg.ResolvedGenerationPlan, artifact scriptports.VerifiedArtifact, family string) scriptports.VerifiedArtifact {
	artifact.OutputDriveFolderID = strings.TrimSpace(plan.DriveFolderID)
	artifact.OutputDriveSubpath = []string{
		filesystem.SafeFolderName(plan.Title),
		filesystem.SafeFolderName(plan.Language),
		family,
	}
	return artifact
}

func isEntityImageCandidate(candidate scriptpkg.SegmentAssetCandidate) bool {
	if candidate.Provider != scriptpkg.VidRushProviderInternetImages && candidate.Provider != scriptpkg.VidRushProviderImageGeneration {
		return false
	}
	return strings.TrimSpace(candidate.Entity) != "" || strings.HasPrefix(strings.TrimSpace(candidate.AssetID), "entity-image-")
}

// isGenerationClipCandidate reports whether a candidate is a provider CLIP —
// video media the run uses — and therefore belongs in the run's clips child
// folder. Image providers are deliberately excluded so a still can never be
// published into the clips family, and a clip can never land in images.
func isGenerationClipCandidate(candidate scriptpkg.SegmentAssetCandidate) bool {
	switch candidate.Provider {
	case scriptpkg.VidRushProviderYouTube, scriptpkg.VidRushProviderArtlist:
		return true
	default:
		return false
	}
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
	targetImages := vidRushImageTargetForSegment(plan, segment)
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
