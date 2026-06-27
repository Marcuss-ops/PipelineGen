// Package app — search_backends.go is the Wave 21 PR 9 BACKFILL
// composition-only bridge between the canonical Search capability
// (internal/application/search/) and the three concrete backend
// domains:
//
//   - providers.SearchProvider — exposed by every source integration
//     (artlist, youtube, stock); keyed by providers.Registry.
//   - assets.ClipsRepository    — the canonical SQLite-backed local
//     catalog. Exposes SearchClipsAdvanced (AdvancedSearchRepo).
//   - mediasearch.Service       — the QDRANT-004 single-tenant
//     semantic API. Workspace-gated.
//
// Wave 19 cross-capability rule: this file IS the ONLY place in
// internal/app/ where multiple internal/application/* domains are
// imported at once. The composition root's canonical pattern
// (per AGENTS.md Pattern 0 + Wave 19 PR-2 dead-plus-setup) is
// "composition-only bridge in internal/app/"; this file fills
// that role for the Search capability. The bare search package
// remains stdlib-only (Wave 19 invariant).
//
// BACKFILL PR 9 policy: the legacy clipssearch.Service keeps
// serving /api/clips/search for safety (the SearchAggregator is
// wired ALONGSIDE, not in front). PR 10 cutover swaps the
// consumer; until then BuildSearchBackends / SearchAggregatorHolder
// return a usable Aggregator for tests + diagnostic exposure only.
package app

import (
	"context"

	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"

	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	mediasearch "github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// ── Adapters (one type per backend) ───────────────────────────────────

// providerSearchBackend wraps a single providers.SearchProvider so
// the Aggregator can coordinate fanout. Capabilities are translated
// from the provider-native enum (CapabilitySearch + CapabilityVideo
// + ...) into the search.Capability enum used by Aggregator.Eligible.
//
// SourceRef carries the provider-native identifier (YouTube VideoID,
// artlist item id, etc). The Aggregator's 4-key dedup uses
// Source+"|"+SourceRef as the canonical-provider-identity key.
type providerSearchBackend struct {
	provider providers.SearchProvider
	caps     []search.Capability
	srcName  string
}

func (b *providerSearchBackend) Name() string {
	if b.srcName != "" {
		return b.srcName
	}
	return b.provider.Name()
}

func (b *providerSearchBackend) Capabilities() []search.Capability {
	if b.caps != nil {
		return b.caps
	}
	return []search.Capability{search.CapVideo}
}

func (b *providerSearchBackend) Search(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	provReq := providers.SearchRequest{
		Query: q.Text,
		Limit: q.Limit,
		Filters: providers.SearchFilters{
			MediaTypes: mediaTypesFromStrings(q.MediaTypes),
		},
	}
	res, err := b.provider.Search(ctx, provReq)
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		out = append(out, search.Candidate{
			Source:      b.Name(),
			SourceRef:   c.SourceRef,
			MediaType:   string(c.MediaType),
			Title:       c.Title,
			PreviewURL:  c.PreviewURL,
			Hash:        "", // providers don't emit content hash
			Score:       c.Score,
		})
	}
	return out, nil
}

var _ search.SearchBackend = (*providerSearchBackend)(nil)

// localSearchBackend wraps sqassets.ClipsRepository.SearchClipsAdvanced
// (the canonical AdvancedSearchRepo interface). Maps Asset rows
// into search.Candidate. Score is normalised 1.0 (local hits are
// keyword-matched) because the repository already filters by
// metadata; the semantic rerank at the Aggregator level is what
// surfaces relevance ordering across all backends.
type localSearchBackend struct {
	repo   *sqassets.ClipsRepository
	srcName string
}

func (b *localSearchBackend) Name() string {
	if b.srcName != "" {
		return b.srcName
	}
	return "local"
}

func (b *localSearchBackend) Capabilities() []search.Capability {
	return []search.Capability{
		search.CapVideo,
		search.CapImage,
		search.CapAudio,
		search.CapMusic,
	}
}

func (b *localSearchBackend) Search(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	req := assetpkg.AdvancedSearchRequest{
		Q:      q.Text,
		Limit:  q.Limit,
		Source: sourceOrAll(q.Filters.Source),
	}
	res, err := b.repo.SearchClipsAdvanced(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate, 0, len(res.Clips))
	for _, clip := range res.Clips {
		out = append(out, search.Candidate{
			AssetID:   clip.ID,
			Source:    string(clip.Source),
			SourceRef: clip.ID,
			Title:     clip.Name,
			MediaType: string(clip.MediaType),
			Score:     1.0, // local hits == exact metadata match, see comment above
		})
	}
	return out, nil
}

var _ search.SearchBackend = (*localSearchBackend)(nil)

// semanticSearchBackend wraps mediasearch.Service with a workspace
// injected at construction. The WorkspaceContext is REQUIRED by
// mediasearch.Service (QDRANT-004 tenant-isolation gate): an
// empty WorkspaceID or the reserved sentinel "default" would
// produce ErrMissingWorkspace. The composition root MUST supply
// a valid workspaceID; BuildSearchBackends short-circuits the
// registration if the ID is empty.
type semanticSearchBackend struct {
	svc         *mediasearch.Service
	workspaceID string
	srcName     string
}

func (b *semanticSearchBackend) Name() string {
	if b.srcName != "" {
		return b.srcName
	}
	return "semantic"
}

func (b *semanticSearchBackend) Capabilities() []search.Capability {
	// mediasearch surfaces Hits across all four canonical
	// Capability buckets; the post-hydration guard layer in
	// mediasearch.Service is the authoritative source of which
	// MediaType rows CAN come back (driven by vector-store data).
	return []search.Capability{
		search.CapVideo,
		search.CapImage,
		search.CapAudio,
		search.CapMusic,
	}
}

func (b *semanticSearchBackend) Search(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	req := mediasearch.MediaSearchRequest{
		Query:     q.Text,
		Mode:      q.Mode,
		Limit:     q.Limit,
		Filters:   q.Filters, // alias of search.Filters
		Workspace: mediasearch.WorkspaceContext{
			WorkspaceID: b.workspaceID,
			IsAdmin:     true, // search-aggregator user is the admin search tenant
		},
	}
	res, err := b.svc.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate, 0, len(res.Hits))
	for _, h := range res.Hits {
		out = append(out, search.Candidate{
			AssetID:    h.AssetID,
			Source:     h.Source,
			SourceRef:  h.AssetID,
			MediaType:  h.MediaType,
			Title:      h.Name,
			PreviewURL: h.DeliveryURL, // signed; matches QDRANT-004 wire invariant
			Score:      h.Score,
		})
	}
	return out, nil
}

var _ search.SearchBackend = (*semanticSearchBackend)(nil)

// ── Composition bridge ─────────────────────────────────────────────────

// SearchBackendBuildOpts groups the inputs BuildSearchBackends
// needs. All three backends can be disabled by leaving the
// corresponding fields nil/empty. The ProviderRegistry and
// ClipsRepository are guaranteed by WireAssets's AssetsBundle
// plumbing (providerRegistry is a direct arg, bundle.ClipsRepo
// is bundle-resident); the mediasearch.Service is an opt-in via
// the new AssetsBundle.MediasearchService + SearchWorkspaceID
// fields added by Wave 21 PR 9.
type SearchBackendBuildOpts struct {
	Logger          *zap.Logger
	ProviderReg     *providers.Registry
	ClipsRepo       *sqassets.ClipsRepository
	MediasearchSvc  *mediasearch.Service
	WorkspaceID     string // for semantic backend; empty disables it
}

// BuildSearchBackends registers backends in a fresh BackendRegistry,
// Freeze()s it, and returns it ready to plug into a search.Aggregator.
// Order of registration mirrors the BACKFILL dual-path rationale:
// providers first (deterministic by Name() ordering), then local,
// then semantic (the only one with prerequisites).
//
// Failure modes: every Register call logs and skips on error
// instead of returning — search.BackendRegistry contracts are
// strict (no nil, no empty Name, no duplicate); if a mis-wired
// adapter surfaces, BuildSearchBackends reports it via the logger
// and the production graph never breaks. The error surface is
// logged; tests can read the returned registry's All() to inspect
// effective population.
func BuildSearchBackends(opts SearchBackendBuildOpts) *search.BackendRegistry {
	log := opts.Logger
	if log == nil {
		log = zap.NewNop()
	}
	reg := search.NewBackendRegistry()

	if opts.ProviderReg != nil {
		for _, p := range opts.ProviderReg.All() {
			sp, ok := p.(providers.SearchProvider)
			if !ok {
				continue
			}
			backend := &providerSearchBackend{
				provider: sp,
				caps:     translateCaps(sp.Capabilities()),
			}
			if err := reg.Register(backend); err != nil {
				log.Warn("BuildSearchBackends: provider backend register failed",
					zap.String("provider", sp.Name()),
					zap.Error(err))
			}
		}
	}

	if opts.ClipsRepo != nil {
		if err := reg.Register(&localSearchBackend{repo: opts.ClipsRepo}); err != nil {
			log.Warn("BuildSearchBackends: local backend register failed",
				zap.Error(err))
		}
	}

	if opts.MediasearchSvc != nil && opts.WorkspaceID != "" {
		if err := reg.Register(&semanticSearchBackend{
			svc:         opts.MediasearchSvc,
			workspaceID: opts.WorkspaceID,
		}); err != nil {
			log.Warn("BuildSearchBackends: semantic backend register failed",
				zap.Error(err))
		}
	}

	reg.Freeze()
	return reg
}

// ── Internal helpers ───────────────────────────────────────────────────

// translateCaps maps the providers.Capability enum into the
// search.Capability enum. Caps that don't map (CapabilityScript,
// CapabilityFetch) are dropped — they're not yet represented in
// the search capability and PR 10 will revisit if necessary.
func translateCaps(in []providers.Capability) []search.Capability {
	out := make([]search.Capability, 0, len(in))
	for _, c := range in {
		switch c {
		case providers.CapabilityVideo:
			out = append(out, search.CapVideo)
		case providers.CapabilityImage:
			out = append(out, search.CapImage)
		case providers.CapabilityMusic:
			out = append(out, search.CapMusic)
		case providers.CapabilityVoice:
			out = append(out, search.CapAudio) // voice ≈ audio for cap bridging
		}
	}
	if len(out) == 0 {
		return []search.Capability{search.CapVideo}
	}
	return out
}

func mediaTypesFromStrings(in []string) []assetpkg.MediaType {
	if len(in) == 0 {
		return nil
	}
	out := make([]assetpkg.MediaType, 0, len(in))
	for _, m := range in {
		if m == "" {
			continue
		}
		out = append(out, assetpkg.MediaType(m))
	}
	return out
}

// sourceOrAll normalises the legacy clipssearch "source" filter
// semantic. Empty string == "all" (skip server-side filtering).
func sourceOrAll(s string) string {
	if s == "" {
		return "all"
	}
	return s
}
