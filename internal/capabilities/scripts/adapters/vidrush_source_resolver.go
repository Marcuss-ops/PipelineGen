package adapters

import (
	"context"
	"fmt"
	"strings"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SourceResolutionCandidate is the result of resolving one segment/slot.
type SourceResolutionCandidate struct {
	Source   mediadomain.SegmentMediaSource
	Provider string
	Stage    string
	Query    string
}

// SourceResolutionRequest contains the canonical inputs for source selection.
type SourceResolutionRequest struct {
	Plan    *scriptpkg.ResolvedGenerationPlan
	Segment scriptpkg.CanonicalSegment
	Slot    mediadomain.SlotKind
	Profile scriptpkg.SegmentSemanticProfile
}

// SourceResolutionProvider searches one provider. Implementations should
// return already-known candidates when possible; acquisition remains separate.
type SourceResolutionProvider interface {
	Resolve(ctx context.Context, req SourceResolutionRequest) (*SourceResolutionCandidate, error)
}

// CanonicalAssetSearcher is the SQLite/Qdrant-backed local catalog surface.
// It is intentionally narrower than the provider search interfaces.
type CanonicalAssetSearcher interface {
	SearchAssets(ctx context.Context, q assetsearch.AssetSearchQuery) ([]assetsearch.AssetSearchHit, error)
}

// SourceResolver applies the fixed source priority and never skips a locked
// assignment. It is deliberately provider-agnostic and therefore testable.
type VidRushSourceResolver struct {
	LocalStock    SourceResolutionProvider
	Canonical     CanonicalAssetSearcher
	YouTube       SourceResolutionProvider
	Artlist       SourceResolutionProvider
	ImageFallback SourceResolutionProvider
}

// Resolve returns the first available source according to the canonical order:
// locked source, suggested AssetID, local stock, YouTube, Artlist, images.
func (r VidRushSourceResolver) Resolve(ctx context.Context, req SourceResolutionRequest) (*SourceResolutionCandidate, error) {
	if req.Plan == nil {
		return nil, fmt.Errorf("source resolver: plan is required")
	}
	if locked := lockedSource(req.Plan, req.Segment.ID, req.Slot); locked != nil {
		return &SourceResolutionCandidate{Source: *locked, Provider: locked.Provider, Stage: "locked"}, nil
	}
	if suggested := suggestedAssetSource(req.Plan, req.Segment.ID, req.Slot); suggested != nil {
		return &SourceResolutionCandidate{Source: *suggested, Provider: suggested.Provider, Stage: "suggested_asset_id"}, nil
	}
	if r.Canonical != nil && req.Slot == mediadomain.SlotPrimaryVideo {
		if candidate, err := r.resolveCanonical(ctx, req); err == nil && candidate != nil {
			return candidate, nil
		}
	}

	providers := []struct {
		name  string
		stage string
		value SourceResolutionProvider
	}{
		// Canonical is checked above; this stage is the optional legacy local
		// stock provider retained for callers that already have one.
		{"local_stock", "local_stock", r.LocalStock},
		{scriptpkg.VidRushProviderYouTube, "youtube", r.YouTube},
		{scriptpkg.VidRushProviderArtlist, "artlist", r.Artlist},
		{scriptpkg.VidRushProviderInternetImages, "image_fallback", r.ImageFallback},
	}
	for _, provider := range providers {
		if provider.value == nil || !providerEnabled(req.Plan, provider.name) {
			continue
		}
		candidate, err := provider.value.Resolve(ctx, req)
		if err != nil {
			continue
		}
		if candidate == nil {
			continue
		}
		candidate.Stage = provider.stage
		if strings.TrimSpace(candidate.Provider) == "" {
			candidate.Provider = provider.name
		}
		return candidate, nil
	}
	return nil, fmt.Errorf("source resolver: no source available for segment %q slot %q", req.Segment.ID, req.Slot)
}

func (r VidRushSourceResolver) resolveCanonical(ctx context.Context, req SourceResolutionRequest) (*SourceResolutionCandidate, error) {
	query := strings.TrimSpace(req.Profile.Topic)
	if query == "" {
		query = strings.TrimSpace(req.Segment.Text)
	}
	if query == "" {
		return nil, nil
	}
	hits, err := r.Canonical.SearchAssets(ctx, assetsearch.AssetSearchQuery{
		Query: query, Source: "stock", MediaType: "video", Limit: 5, IsSystem: true,
	})
	if err != nil || len(hits) == 0 {
		return nil, err
	}
	for _, hit := range hits {
		if strings.TrimSpace(hit.AssetID) == "" {
			continue
		}
		return &SourceResolutionCandidate{
			Source: mediadomain.SegmentMediaSource{
				SegmentID: req.Segment.ID, Slot: req.Slot, Provider: hit.Source,
				AssetID: hit.AssetID, Query: query,
			},
			Provider: hit.Source, Stage: "canonical_stock", Query: query,
		}, nil
	}
	return nil, nil
}

func lockedSource(plan *scriptpkg.ResolvedGenerationPlan, segmentID string, slot mediadomain.SlotKind) *mediadomain.SegmentMediaSource {
	for _, assignment := range plan.MediaPlan.Assignments {
		if !assignment.Locked || assignment.SegmentID != segmentID || assignment.Slot != slot {
			continue
		}
		if strings.TrimSpace(assignment.Asset.AssetID) != "" {
			return &mediadomain.SegmentMediaSource{SegmentID: segmentID, Slot: slot, Provider: assignment.Asset.Provider, AssetID: assignment.Asset.AssetID, Mode: mediadomain.SegmentMediaSourceModeRequired}
		}
		if strings.TrimSpace(assignment.Asset.SourceURL) != "" {
			return &mediadomain.SegmentMediaSource{SegmentID: segmentID, Slot: slot, Provider: assignment.Asset.Provider, SourceURL: assignment.Asset.SourceURL, Mode: mediadomain.SegmentMediaSourceModeRequired}
		}
	}
	return nil
}

func suggestedAssetSource(plan *scriptpkg.ResolvedGenerationPlan, segmentID string, slot mediadomain.SlotKind) *mediadomain.SegmentMediaSource {
	for _, source := range plan.MediaPlan.Sources {
		if source.SegmentID == segmentID && source.Slot == slot && strings.TrimSpace(source.AssetID) != "" {
			copy := source
			return &copy
		}
	}
	return nil
}

func providerEnabled(plan *scriptpkg.ResolvedGenerationPlan, provider string) bool {
	if provider == "local_stock" {
		return true
	}
	switch provider {
	case scriptpkg.VidRushProviderYouTube:
		return plan.MediaPlan.ProviderPolicy.YouTube.AsBool()
	case scriptpkg.VidRushProviderArtlist:
		return plan.MediaPlan.ProviderPolicy.Artlist.AsBool()
	case scriptpkg.VidRushProviderInternetImages:
		return plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() || plan.MediaPlan.ProviderPolicy.ImageGeneration.AsBool()
	default:
		return false
	}
}
