package adapters

import (
	"context"
	"sort"
	"sync"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

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
