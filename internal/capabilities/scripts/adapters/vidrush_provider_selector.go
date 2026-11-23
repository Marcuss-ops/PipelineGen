package adapters

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
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

	profile := segment.CanonicalSemanticProfile()
	if profile.SegmentID == "" {
		profile.SegmentID = segment.SegmentID
	}
	preferences := make([]ProviderPreference, 0, 4)
	for _, provider := range []string{
		scriptpkg.VidRushProviderYouTube,
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

// VidRushAssetProviderRegistry is the single provider-selection surface for
// VidRush. It is mutable only during composition and can be frozen before
// workers start, making dispatch deterministic and safe for concurrent reads.
type VidRushAssetProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]scriptports.VidRushAssetProvider
	frozen    bool
}

func NewVidRushAssetProviderRegistry() *VidRushAssetProviderRegistry {
	return &VidRushAssetProviderRegistry{providers: make(map[string]scriptports.VidRushAssetProvider, 3)}
}

func (r *VidRushAssetProviderRegistry) Register(provider scriptports.VidRushAssetProvider) error {
	if r == nil || provider == nil {
		return scriptports.ErrVidRushProviderNotFound
	}
	name := normalizeVidRushProviderName(provider.Name())
	if !scriptpkg.IsVidRushProvider(name) {
		return scriptports.ErrVidRushProviderNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return scriptports.ErrVidRushProviderRegistryFrozen
	}
	if _, exists := r.providers[name]; exists {
		return scriptports.ErrVidRushProviderDuplicate
	}
	r.providers[name] = provider
	return nil
}

func (r *VidRushAssetProviderRegistry) Freeze() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

func (r *VidRushAssetProviderRegistry) Provider(name string) (scriptports.VidRushAssetProvider, error) {
	if r == nil {
		return nil, scriptports.ErrVidRushProviderNotFound
	}
	r.mu.RLock()
	provider, ok := r.providers[normalizeVidRushProviderName(name)]
	r.mu.RUnlock()
	if !ok {
		return nil, scriptports.ErrVidRushProviderNotFound
	}
	return provider, nil
}

func (r *VidRushAssetProviderRegistry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	r.mu.RUnlock()
	sort.Strings(names)
	return names
}

// Search dispatches through the registry and never treats an unavailable
// provider as an empty successful result.
func (r *VidRushAssetProviderRegistry) Search(ctx context.Context, provider string, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	p, err := r.Provider(provider)
	if err != nil {
		return nil, err
	}
	return p.Search(ctx, req)
}

func normalizeVidRushProviderName(name string) string {
	if name == scriptpkg.VidRushProviderImageGeneration {
		return name
	}
	return lowerTrim(name)
}

func lowerTrim(value string) string {
	// Kept local so this registry has no dependency on transport helpers.
	var b []byte
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b = append(b, c)
	}
	start := 0
	for start < len(b) && (b[start] == ' ' || b[start] == '\t' || b[start] == '\n' || b[start] == '\r') {
		start++
	}
	end := len(b)
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\n' || b[end-1] == '\r') {
		end--
	}
	return string(b[start:end])
}

// ResolveManualSegmentQueries returns the caller-supplied search queries for
// one segment/provider/slot. Locked assignments are authoritative and suppress
// provider search. Empty Providers and MediaTypes are intentionally treated as
// unconstrained, matching the media-plan contract.
func ResolveManualSegmentQueries(
	plan *scriptpkg.ResolvedGenerationPlan,
	segment scriptpkg.CanonicalSegment,
	provider string,
	slot mediadomain.SlotKind,
) []string {
	if plan == nil {
		return nil
	}
	if hasLockedSegmentAssignment(plan.MediaPlan.Assignments, segment.ID, slot) {
		return nil
	}

	provider = strings.ToLower(strings.TrimSpace(provider))
	queries := make([]string, 0)
	seen := make(map[string]struct{})
	for _, search := range manualSearchesForSegment(plan.MediaPlan, segment.ID) {
		if search.Slot != slot {
			continue
		}
		if !searchAllowsProvider(search.Providers, provider) || !searchAllowsSlotMediaTypes(search.MediaTypes, slot) {
			continue
		}
		query := strings.TrimSpace(search.Query)
		if query == "" {
			continue
		}
		key := strings.ToLower(query)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		queries = append(queries, query)
	}
	return queries
}

// manualSearchesForSegment is the single segment filter for caller-authored
// manual searches. Slot discovery and query routing must see the same rows.
func manualSearchesForSegment(plan mediadomain.MediaPlanSpec, segmentID string) []mediadomain.SegmentMediaSearch {
	segmentID = strings.TrimSpace(segmentID)
	out := make([]mediadomain.SegmentMediaSearch, 0)
	for _, search := range plan.Searches {
		if strings.TrimSpace(search.SegmentID) == segmentID {
			out = append(out, search)
		}
	}
	return out
}

// ManualSearchSlots returns the explicit slots for one segment. This is the
// slot-discovery half of the same resolver that routes the queries, so a
// search cannot be visible to one owner and invisible to the other.
func ManualSearchSlots(plan mediadomain.MediaPlanSpec, segmentID string) []mediadomain.SlotKind {
	seen := map[mediadomain.SlotKind]bool{}
	for _, search := range manualSearchesForSegment(plan, segmentID) {
		if slot := search.Slot; slot.IsValid() {
			seen[slot] = true
		}
	}
	ordered := []mediadomain.SlotKind{mediadomain.SlotPrimaryVideo, mediadomain.SlotSecondaryImage, mediadomain.SlotEvidenceOverlay, mediadomain.SlotPortrait, mediadomain.SlotDocument, mediadomain.SlotBackground, mediadomain.SlotMap}
	out := make([]mediadomain.SlotKind, 0, len(seen))
	for _, slot := range ordered {
		if seen[slot] {
			out = append(out, slot)
		}
	}
	return out
}

func hasLockedSegmentAssignment(assignments []mediadomain.SegmentMediaAssignment, segmentID string, slot mediadomain.SlotKind) bool {
	for _, assignment := range assignments {
		if assignment.Locked && assignment.SegmentID == segmentID && assignment.Slot == slot {
			return true
		}
	}
	return false
}

func searchAllowsProvider(providers []string, provider string) bool {
	if len(providers) == 0 {
		return true
	}
	for _, candidate := range providers {
		if strings.EqualFold(strings.TrimSpace(candidate), provider) {
			return true
		}
	}
	return false
}

func searchAllowsSlotMediaTypes(mediaTypes []string, slot mediadomain.SlotKind) bool {
	if len(mediaTypes) == 0 {
		return true
	}
	for _, mediaType := range mediaTypes {
		if mediadomain.IsMediaTypeAllowed(slot, strings.ToLower(strings.TrimSpace(mediaType))) {
			return true
		}
	}
	return false
}
