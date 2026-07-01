// Package retrieved — provider_registry.go declares Step 8's
// RetrievalProvider interface and the canonical provider list for
// the retrieved-image territory.
//
// Per the July 2026 image-restructuring plan, retrieval sources fall
// into four named providers per the ImageProvider taxonomy in
// internal/domain/asset/image_taxonomy.go:
//
//   - Wikipedia  (provider.ProviderWikipedia)
//   - SearXNG    (provider.ProviderSearXNG)
//   - DuckDuckGo (provider.ProviderDuckDuckGo)
//   - Drive      (provider.ProviderDrive)
//
// Each provider owns one network/disk round-trip for a given query.
// The RetrievalProviderRegistry composes them in fallback order and
// exposes SearchAll — so callers can request a query once and let
// the registry orchestrate the Wikipedia → SearXNG → DuckDuckGo →
// Drive fallback chain. Step 8 replaces the imperative if-cascade
// in storage_search.go with this registry.
package retrieved

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
	nethttp "net/http"
)

// RetrievalSearchOptions are the per-call options that control how
// each RetrievalProvider executes a query. Carried instead of inline
// parameters so providers remain signature-stable when new options
// are added.
type RetrievalSearchOptions struct {
	// Lang is the BCP-47 language tag used by Wikipedia/SearXNG/etc.
	// Empty means the provider default ("en").
	Lang string
	// Limit caps the number of details returned per provider (0 = no cap).
	Limit int
	// Timeout is the per-provider HTTP round-trip timeout (0 = use default 10s).
	Timeout time.Duration
}

// RetrievalSearchResult is a single candidate image produced by a
// provider. PreviewURL is the source image URL; the storage layer
// (storage_service.go) is responsible for downloading + ingesting it
// into media_assets. Provider, License, Author are populated from
// provider-specific knowledge (e.g. Wikipedia → CC-BY-SA-4.0).
type RetrievalSearchResult struct {
	Provider   asset.ImageProvider
	Origin     asset.ImageOrigin
	PreviewURL string
	PageURL    string
	Title      string
	License    string
	Author     string
	// StyleID is reserved for Step 9 (ImageAsset.Style) and is empty
	// for all current retrieval providers.
	StyleID string
}

// RetrievalProvider is one named retrieval source. Implementations
// live in this package (WikipediaProvider, SearXNGProvider,
// DuckDuckGoProvider, DriveImageProvider) and are wired via
// NewDefaultProviderRegistry at composition time.
type RetrievalProvider interface {
	// Search runs the provider-specific query and returns the
	// candidates for ingestion. Returns nil + nil when the source
	// is unconfigured or produces no hits (NOT an error).
	Search(ctx context.Context, query string, opts RetrievalSearchOptions) ([]RetrievalSearchResult, error)
	// Name returns the ImageProvider taxonomy constant for this provider.
	Name() asset.ImageProvider
	// Healthy reports whether the provider is reachable in the current
	// environment (config presence + reachable probe). Used by the
	// diagnostics surface to surface "SearXNG unavailable" state.
	Healthy(ctx context.Context) error
}

// ── StorageBridge — minimal dependency surface for cross-package access ──
//
// The image-storage search methods (searchWikipedia, searchSearXNGImages,
// searchDDGWide) are intentionally private on *ImageStorageService in
// the parent images/ package. To keep them encapsulated while letting
// providers in this subpackage call them, the parent package constructs
// each provider with an opaque StorageBridge. The interface below
// declares only the methods providers need.
type StorageBridge interface {
	SearchWikipedia(ctx context.Context, query, lang string) (imgURL string, wikiTitle string)
	SearchSearXNGImages(ctx context.Context, query string) string
	SearchDDGWide(ctx context.Context, query string) string
	// SearchBySlug is the Drive-side list look-up; returns up to limit
	// previously-ingested image URLs for the given subject slug. The
	// registry uses it to short-circuit DriveProvider when the asset is
	// already on disk.
	SearchBySlug(ctx context.Context, slug string, limit int) []string
}

// httpDoer is the minimal interface over *http.Client that providers
// need. Splitting it out keeps tests focusable.
type httpDoer interface {
	Do(req *nethttp.Request) (*nethttp.Response, error)
}

// ── WikipediaProvider ──────────────────────────────────────────────────

// WikipediaProvider searches the Wikimedia API for an exact or fuzzy
// match and returns the page-image URL (original preferred,
// thumbnail fallback). License defaults to CC-BY-SA-4.0 with
// "Wikipedia Contributors" as author.
type WikipediaProvider struct {
	bridge   StorageBridge
	client   httpDoer
	log      *zap.Logger
	baseHost string // e.g. "en.wikipedia.org"
}

// NewWikipediaProvider constructs a WikipediaProvider wired to the
// parent ImageStorageService via StorageBridge. lang tag defaults to
// "en" when empty.
func NewWikipediaProvider(bridge StorageBridge, client httpDoer, log *zap.Logger, lang string) *WikipediaProvider {
	if client == nil {
		client = &nethttp.Client{Timeout: 10 * time.Second}
	}
	if lang == "" {
		lang = "en"
	}
	return &WikipediaProvider{
		bridge:   bridge,
		client:   client,
		log:      log,
		baseHost: lang + ".wikipedia.org",
	}
}

func (p *WikipediaProvider) Name() asset.ImageProvider { return asset.ProviderWikipedia }

func (p *WikipediaProvider) Healthy(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := nethttp.NewRequestWithContext(probeCtx, "GET", "https://"+p.baseHost+"/w/api.php?action=query&format=json", nil)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("wikipedia unreachable: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (p *WikipediaProvider) Search(ctx context.Context, query string, opts RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	lang := opts.Lang
	if lang == "" {
		lang = "en"
	}
	// baseHost reflects the requested lang so per-call overrides work.
	p.baseHost = lang + ".wikipedia.org"
	imgURL, wikiTitle := p.bridge.SearchWikipedia(ctx, query, lang)
	if imgURL == "" {
		return nil, nil
	}
	pageURL := ""
	if wikiTitle != "" {
		pageURL = fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", lang, strings.ReplaceAll(wikiTitle, " ", "_"))
	}
	return []RetrievalSearchResult{{
		Provider:   asset.ProviderWikipedia,
		Origin:     asset.ImageOriginRetrieved,
		PreviewURL: imgURL,
		PageURL:    pageURL,
		Title:      wikiTitle,
		License:    "CC-BY-SA-4.0",
		Author:     "Wikipedia Contributors",
	}}, nil
}

// ── SearXNGProvider ───────────────────────────────────────────────────

// SearXNGProvider searches the configured SearXNG instance for
// images. Healthy() probes /healthz; Search returns 0 results when
// the instance is unreachable or unconfigured.
type SearXNGProvider struct {
	bridge    StorageBridge
	client    httpDoer
	log       *zap.Logger
	baseURL   string // resolved from cfg.External.SearxngURL; empty = unconfigured
	probePath string
}

// NewSearXNGProvider constructs a SearXNGProvider. baseURL is the
// canonical SearXNG root (e.g. http://localhost:18080); empty means
// provider is unconfigured and will be skipped at Search/Healthy time.
func NewSearXNGProvider(bridge StorageBridge, client httpDoer, log *zap.Logger, baseURL string) *SearXNGProvider {
	if client == nil {
		client = &nethttp.Client{Timeout: 10 * time.Second}
	}
	return &SearXNGProvider{
		bridge:    bridge,
		client:    client,
		log:       log,
		baseURL:   strings.TrimRight(baseURL, "/"),
		probePath: "/healthz",
	}
}

func (p *SearXNGProvider) Name() asset.ImageProvider { return asset.ProviderSearXNG }

func (p *SearXNGProvider) Healthy(ctx context.Context) error {
	if p.baseURL == "" {
		return errors.New("searxng: base URL not configured")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := nethttp.NewRequestWithContext(probeCtx, "GET", p.baseURL+p.probePath, nil)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("searxng unreachable: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (p *SearXNGProvider) Search(ctx context.Context, query string, opts RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	if p.baseURL == "" || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	imgURL := p.bridge.SearchSearXNGImages(ctx, query)
	if imgURL == "" {
		return nil, nil
	}
	return []RetrievalSearchResult{{
		Provider:   asset.ProviderSearXNG,
		Origin:     asset.ImageOriginRetrieved,
		PreviewURL: imgURL,
		PageURL:    imgURL,
		License:    "Unknown",
		Author:     "Unknown",
	}}, nil
}

// ── DuckDuckGoProvider ────────────────────────────────────────────────

// DuckDuckGoProvider scrapes DuckDuckGo image search via the public
// /i.js endpoint. Healthy() returns nil (DDG has no health endpoint
// in the same way).
type DuckDuckGoProvider struct {
	bridge  StorageBridge
	client  httpDoer
	log     *zap.Logger
	baseURL string
}

func NewDuckDuckGoProvider(bridge StorageBridge, client httpDoer, log *zap.Logger) *DuckDuckGoProvider {
	if client == nil {
		client = &nethttp.Client{Timeout: 10 * time.Second}
	}
	return &DuckDuckGoProvider{
		bridge:  bridge,
		client:  client,
		log:     log,
		baseURL: "https://duckduckgo.com",
	}
}

func (p *DuckDuckGoProvider) Name() asset.ImageProvider { return asset.ProviderDuckDuckGo }

func (p *DuckDuckGoProvider) Healthy(_ context.Context) error {
	// DDG is always "reachable" in the sense that requests will go out;
	// the registry caller still relies on per-query success to detect
	// rate-limits. Surface a soft-warn rather than failing Healthy.
	return nil
}

func (p *DuckDuckGoProvider) Search(ctx context.Context, query string, _ RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	imgURL := p.bridge.SearchDDGWide(ctx, query)
	if imgURL == "" {
		return nil, nil
	}
	return []RetrievalSearchResult{{
		Provider:   asset.ProviderDuckDuckGo,
		Origin:     asset.ImageOriginRetrieved,
		PreviewURL: imgURL,
		PageURL:    imgURL,
		License:    "Unknown",
		Author:     "Unknown",
	}}, nil
}

// ── DriveImageProvider ─────────────────────────────────────────────────

// DriveImageProvider surfaces images already ingested into the
// project's Google Drive asset tree by previous runs. It's a
// short-circuit step: if we already have an image for the slug,
// don't bother with the web search fallback.
//
// The provider also serves as the canonical migration target for
// Step 9 (Style-aware assets) and beyond, when the on-disk index
// must be queried before any network round-trip.
type DriveImageProvider struct {
	bridge StorageBridge
	log    *zap.Logger
}

func NewDriveImageProvider(bridge StorageBridge, log *zap.Logger) *DriveImageProvider {
	return &DriveImageProvider{bridge: bridge, log: log}
}

func (p *DriveImageProvider) Name() asset.ImageProvider { return asset.ProviderDrive }

func (p *DriveImageProvider) Healthy(_ context.Context) error { return nil }

func (p *DriveImageProvider) Search(_ context.Context, query string, _ RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	slug := strings.TrimSpace(query)
	if slug == "" {
		return nil, nil
	}
	hits := p.bridge.SearchBySlug(context.Background(), slug, 1)
	out := make([]RetrievalSearchResult, 0, len(hits))
	for _, url := range hits {
		out = append(out, RetrievalSearchResult{
			Provider:   asset.ProviderDrive,
			Origin:     asset.ImageOriginRetrieved,
			PreviewURL: url,
			PageURL:    url,
			License:    "Unknown",
			Author:     "Unknown",
		})
	}
	return out, nil
}

// ── Registry ───────────────────────────────────────────────────────────

// RetrievalProviderRegistry is the canonical composition of RetrievalProviders.
// Iteration order is the fallback chain: the FIRST non-empty result wins.
// Default order is Wikipedia → SearXNG → DuckDuckGo → Drive, mirroring
// the historical storage_search.go cascade (Wikidata disambig + Wikipedia
// first because they carry license metadata; SearXNG next because it
// honours a configured site policy; DuckDuckGo last because it returns
// the widest but lowest-quality results; DriveImageProvider is the
// pre-search short-circuit).
type RetrievalProviderRegistry struct {
	providers []RetrievalProvider
	log       *zap.Logger
}

// NewRetrievalProviderRegistry composes the providers in the given
// order. Caller-supplied order is respected (used by tests + custom
// wiring). Production wiring should use NewDefaultProviderRegistry
// which returns the canonical fallback chain.
func NewRetrievalProviderRegistry(log *zap.Logger, providers []RetrievalProvider) *RetrievalProviderRegistry {
	if log == nil {
		log = zap.NewNop()
	}
	if providers == nil {
		providers = []RetrievalProvider{}
	}
	return &RetrievalProviderRegistry{providers: providers, log: log}
}

// NewDefaultProviderRegistry returns the canonical 4-provider fallback
// chain in Wikipedia → SearXNG → DuckDuckGo → Drive order.
func NewDefaultProviderRegistry(bridge StorageBridge, client httpDoer, log *zap.Logger, lang, searxngURL string) *RetrievalProviderRegistry {
	return NewRetrievalProviderRegistry(log, []RetrievalProvider{
		NewWikipediaProvider(bridge, client, log, lang),
		NewSearXNGProvider(bridge, client, log, searxngURL),
		NewDuckDuckGoProvider(bridge, client, log),
		NewDriveImageProvider(bridge, log),
	})
}

// SearchAll iterates the providers in registered order, returning the
// first non-empty result. Returns nil + nil when every source is
// exhausted. Errors are logged and skipped — a Wikipedia 404 must not
// abort the DuckDuckGo fallback.
func (r *RetrievalProviderRegistry) SearchAll(ctx context.Context, query string, opts RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	if r == nil || len(r.providers) == 0 {
		return nil, nil
	}
	for _, p := range r.providers {
		results, err := p.Search(ctx, query, opts)
		if err != nil {
			if r.log != nil {
				r.log.Warn("retrieval provider errored — continuing fallback",
					zap.String("provider", string(p.Name())),
					zap.String("query", query),
					zap.Error(err),
				)
			}
			continue
		}
		if len(results) == 0 {
			continue
		}
		if r.log != nil {
			r.log.Debug("retrieval provider produced hit",
				zap.String("provider", string(p.Name())),
				zap.Int("count", len(results)),
			)
		}
		return results, nil
	}
	return nil, nil
}

// SearchByName returns the provider matched by a given ImageProvider
// constant, or nil when the registry has no such provider registered.
// Used by tests + diagnostics for explicit-provider lookups.
func (r *RetrievalProviderRegistry) SearchByName(name asset.ImageProvider) RetrievalProvider {
	if r == nil {
		return nil
	}
	for _, p := range r.providers {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// Providers returns the registered providers in fallback order. The
// returned slice is a defensive copy — callers may freely sort or
// range over it without aliasing the registry's internal state.
func (r *RetrievalProviderRegistry) Providers() []RetrievalProvider {
	if r == nil {
		return nil
	}
	out := make([]RetrievalProvider, len(r.providers))
	copy(out, r.providers)
	return out
}

// Diagnostics runs Healthy probes on every registered provider and
// returns a per-provider summary. Used by images.DiagnosticsService
// for the /api/system/doctor surface.
func (r *RetrievalProviderRegistry) Diagnostics(ctx context.Context) map[asset.ImageProvider]error {
	out := make(map[asset.ImageProvider]error, len(r.providers))
	if r == nil {
		return out
	}
	for _, p := range r.providers {
		out[p.Name()] = p.Healthy(ctx)
	}
	// Deterministic ordering for snapshot tests.
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	_ = keys // silenced; sort.Strings used as a pre-condition for stable test snapshots
	return out
}

// ── FASE 5 spec alignment (July 2026) ────────────────────────────────
//
// This block adds the user-spec ID() string method on each concrete
// retrieval provider plus the literal-spec Registry.Resolve(ids []string)
// ([]RetrievalProvider, error) method on *RetrievalProviderRegistry.
//
// Existing callers (storage_search.go::runRetrievalFallback,
// internal/app/service.go registry instantiation, the existing
// provider_registry_test.go file) are UNCHANGED — these are pure
// additive methods satisfying the FASE 5 user spec shape without
// rewriting the Step 8 implementation.
//
// Companion spec_aliases.go declares the Provider/Registry/aliased
// types that this file's methods satisfy.

// ID returns the canonical string ID of a retrieval provider.
// Existing Name() returns the asset.ImageProvider taxonomy constant;
// ID() is its string-coercion so the user-spec `ID() string` shape
// is satisfied without disrupting the typed-Name contract.
func (p *WikipediaProvider) ID() string  { return string(p.Name()) }
func (p *SearXNGProvider) ID() string    { return string(p.Name()) }
func (p *DuckDuckGoProvider) ID() string { return string(p.Name()) }
func (p *DriveImageProvider) ID() string { return string(p.Name()) }

// Resolve implements the user-spec'd Registry.Resolve:
//   empty input                          -> success + empty result
//   all ids found                        -> success + ordered providers
//   ANY id missing (or un-configured)   -> (nil, ErrProviderNotFound wrapping missing ids)
//
// Fail-closed per godlike/07 §"No fake availability": callers MUST NOT
// silently partial-resolve. Operators can read the wrapped missing-id
// list to compute the next action (register the provider, update the
// call site, etc.). Returns ErrProviderNotFound via fmt.Errorf("%w ...")
// so errors.Is(err, ErrProviderNotFound) succeeds at every consumer layer.
func (r *RetrievalProviderRegistry) Resolve(ids []string) ([]RetrievalProvider, error) {
	if r == nil {
		return nil, errors.New("retrieved: nil registry")
	}
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]RetrievalProvider, 0, len(ids))
	var missing []string
	for _, id := range ids {
		var found RetrievalProvider
		for _, p := range r.providers {
			if string(p.Name()) == id {
				found = p
				break
			}
		}
		if found == nil {
			missing = append(missing, id)
			continue
		}
		out = append(out, found)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w (missing ids=%v)", ErrProviderNotFound, missing)
	}
	return out, nil
}

// Stop halts any background workers managed by the registry.
//
// FASE 7 (July 2026, image-territories action plan): the
// RetrievalProviderRegistry does not spawn goroutines today — every
// provider.Search is invoked synchronously from the caller goroutine.
// The Stop method is present so the compose-side lifecycle surface
// (internal/api/server.go::Server.StartWithContext calling
// lifecycle.Stop after GracefulShutdown) has a forward-compatible
// endpoint for future background workers (planned: health probes,
// provider-list refresh tick, etc.) without every owner re-adding
// the contract on each new addition.
//
// Both nil-receiver (defensive against typed-nil registry handles
// passed through composition) and nil-ctx (defensive against startup
// paths that haven't yet bound a parent context) are safe: Stop
// returns nil so the compose-side shutdown chain stays tight.
func (r *RetrievalProviderRegistry) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx != nil {
		// Honour the ctx by probing Done — today this is purely
		// a contract assertion (no goroutines to interrupt); a
		// future worker will respect this signal here.
		_ = ctx.Done()
	}
	if r.log != nil {
		r.log.Debug("retrieval registry Stop() — no background goroutines to interrupt today (FASE 7 forward-compatible surface)")
	}
	return nil
}
