package adapters

import (
	"errors"
	"fmt"
	"strings"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

var ErrNoVidRushProvider = errors.New("vidrush provider selector: no eligible provider")

// ProviderPreference is the deterministic selector explanation for one
// provider. Scores are policy scores, not model predictions.
type ProviderPreference struct {
	Provider string  `json:"provider"`
	Score    float64 `json:"score"`
	Reason   string  `json:"reason,omitempty"`
}

// VidRushProviderSelection is the selector result, ordered best-first.
type VidRushProviderSelection struct {
	Selected    string               `json:"selected"`
	Preferences []ProviderPreference `json:"preferences"`
	UsedAssetID string               `json:"used_asset_id,omitempty"`
}

// VidRushProviderSelector applies deterministic source priority and semantic
// provider boosts. Existing assets always win over external discovery.
type VidRushProviderSelector struct{}

func NewVidRushProviderSelector() VidRushProviderSelector { return VidRushProviderSelector{} }

// Select chooses the best eligible provider for one segment/slot.
func (VidRushProviderSelector) Select(plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult, contentType string) (VidRushProviderSelection, error) {
	if plan == nil {
		return VidRushProviderSelection{}, errors.New("vidrush provider selector: plan is required")
	}
	if asset := existingSegmentAsset(plan, segment.SegmentID, contentType); asset != nil {
		return VidRushProviderSelection{
			Selected: asset.Provider, UsedAssetID: asset.AssetID,
			Preferences: []ProviderPreference{{Provider: asset.Provider, Score: 1, Reason: "existing canonical asset"}},
		}, nil
	}

	profile := profileFromVidRushSegment(segment)
	if profile.SegmentID == "" {
		profile.SegmentID = segment.SegmentID
	}
	preferences := make([]ProviderPreference, 0, 4)
	for _, provider := range []string{
		scriptpkg.VidRushProviderYouTube,
		scriptpkg.VidRushProviderArtlist,
		scriptpkg.VidRushProviderInternetImages,
		scriptpkg.VidRushProviderImageGeneration,
	} {
		if !providerAllowed(plan.MediaPlan.ProviderPolicy, provider) {
			continue
		}
		score, reason := providerScore(provider, profile, contentType)
		preferences = append(preferences, ProviderPreference{Provider: provider, Score: score, Reason: reason})
	}
	if len(preferences) == 0 {
		return VidRushProviderSelection{}, fmt.Errorf("%w for segment %q", ErrNoVidRushProvider, segment.SegmentID)
	}
	sortProviderPreferences(preferences)
	return VidRushProviderSelection{Selected: preferences[0].Provider, Preferences: preferences}, nil
}

func existingSegmentAsset(plan *scriptpkg.ResolvedGenerationPlan, segmentID, contentType string) *mediadomain.MediaRef {
	for _, assignment := range plan.MediaPlan.Assignments {
		if assignment.SegmentID != segmentID || !assignment.Locked || !contentTypeMatchesSlot(contentType, assignment.Slot) {
			continue
		}
		if strings.TrimSpace(assignment.Asset.AssetID) != "" || strings.TrimSpace(assignment.Asset.Provider) != "" {
			asset := assignment.Asset
			if asset.Provider == "" {
				asset.Provider = scriptpkg.VidRushProviderArtlist
			}
			return &asset
		}
	}
	for _, source := range plan.MediaPlan.Sources {
		if source.SegmentID != segmentID || strings.TrimSpace(source.AssetID) == "" || !contentTypeMatchesSlot(contentType, source.Slot) {
			continue
		}
		return &mediadomain.MediaRef{AssetID: source.AssetID, Provider: source.Provider}
	}
	return nil
}

func contentTypeMatchesSlot(contentType string, slot mediadomain.SlotKind) bool {
	typeName := strings.ToLower(strings.TrimSpace(contentType))
	if typeName == "" {
		return true
	}
	if strings.Contains(typeName, "image") {
		return slot == mediadomain.SlotSecondaryImage
	}
	return slot == mediadomain.SlotPrimaryVideo || slot == mediadomain.SlotEvidenceOverlay || slot == mediadomain.SlotBackground
}

func providerAllowed(policy mediadomain.MediaProviderPolicy, provider string) bool {
	switch provider {
	case scriptpkg.VidRushProviderYouTube:
		return policy.YouTube.AsBool()
	case scriptpkg.VidRushProviderArtlist:
		return policy.Artlist.AsBool()
	case scriptpkg.VidRushProviderInternetImages:
		return policy.InternetImages.AsBool()
	case scriptpkg.VidRushProviderImageGeneration:
		return policy.ImageGeneration.AsBool()
	default:
		return false
	}
}

func providerScore(provider string, profile scriptpkg.SegmentSemanticProfile, contentType string) (float64, string) {
	hasNamedEntity := len(profile.Entities) > 0
	hasHistoricalSignal := hasHistoricalSignal(profile)
	hasVisualTerms := len(profile.VisualTerms) > 0
	hasTopic := strings.TrimSpace(profile.Topic) != ""
	typeName := strings.ToLower(strings.TrimSpace(contentType))

	score := 0.25
	reason := "general semantic fallback"
	switch provider {
	case scriptpkg.VidRushProviderYouTube:
		if hasNamedEntity || hasHistoricalSignal {
			score += 0.60
			reason = "named historical subject or event favors documentary footage"
		} else if hasTopic {
			score += 0.25
			reason = "topic can be resolved through documentary footage"
		}
	case scriptpkg.VidRushProviderArtlist:
		if hasVisualTerms || typeName == "video" {
			score += 0.55
			reason = "generic filmable visual favors stock footage"
		} else {
			score += 0.20
		}
	case scriptpkg.VidRushProviderInternetImages:
		if strings.Contains(typeName, "image") {
			score += 0.70
			reason = "image content explicitly favors still imagery"
		} else if hasNamedEntity {
			score += 0.45
			reason = "a specific entity favors still imagery"
		}
	case scriptpkg.VidRushProviderImageGeneration:
		if !hasNamedEntity && !hasVisualTerms {
			score += 0.30
			reason = "abstract content is suitable for generated imagery"
		}
	}
	return clampUnit(score), reason
}

func hasHistoricalSignal(profile scriptpkg.SegmentSemanticProfile) bool {
	for _, term := range profile.Terms {
		if term.Kind == scriptpkg.TermKindTemporal || term.Kind == scriptpkg.TermKindTechnology {
			return true
		}
	}
	for _, entity := range profile.Entities {
		value := strings.ToLower(entity.Value)
		if strings.Contains(value, "19") || strings.Contains(value, "18") || strings.Contains(value, "20") {
			return true
		}
	}
	return false
}

func sortProviderPreferences(values []ProviderPreference) {
	for i := 1; i < len(values); i++ {
		current := values[i]
		j := i - 1
		for j >= 0 && (values[j].Score < current.Score || (values[j].Score == current.Score && values[j].Provider > current.Provider)) {
			values[j+1] = values[j]
			j--
		}
		values[j+1] = current
	}
}
