// Package scripts — source_resolver_research.go is the research source
// resolver core: the WebResearchResolver (the only script source
// resolver allowed to access external web content), the cache-only
// submission preflight, and the Resolve flow (cache hit / web search /
// page fetch / source assembly). The pure query + cache-key helpers
// live in source_research_queries.go and the source-quality validation
// in source_research_validate.go (split 2026-08-07 to satisfy the
// strict per-file LOC cap, architecture/policy.yaml#max_lines_per_file_strict).
package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/linguistics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

const (
	researchVersion             = "web-research-v2"
	researchDefaultMaxQueries   = 4
	researchDefaultResults      = 5
	researchDefaultMaxPages     = 8
	researchDefaultMinSources   = 3
	researchDefaultTimeout      = 90
	researchDefaultCacheTTLHour = 168
)

// identityVersion and queryPlannerVersion are opaque version tokens folded
// into the research cache fingerprint. Bump them when the SubjectIdentity
// registry or the QueryPlanner logic changes: stale cache entries must not
// survive a semantic change in identity resolution or query planning.
const (
	identityVersion     = "v1"
	queryPlannerVersion = "v1"
)

var (
	ErrResearchProviderNotConfigured = errors.New("RESEARCH_PROVIDER_NOT_CONFIGURED")
	ErrResearchSearchFailed          = errors.New("RESEARCH_SEARCH_FAILED")
	ErrResearchFetchFailed           = errors.New("RESEARCH_FETCH_FAILED")
	ErrResearchInsufficientSources   = errors.New("RESEARCH_INSUFFICIENT_SOURCES")
	ErrResearchTimeout               = errors.New("RESEARCH_TIMEOUT")
	ErrResearchPromptInjection       = errors.New("RESEARCH_PROMPT_INJECTION_DETECTED")
	ErrResearchCacheMiss             = errors.New("RESEARCH_CACHE_MISS")
	ErrResearchDisabledCacheMiss     = errors.New("RESEARCH_DISABLED_CACHE_MISS")
	ErrResearchRankerNotConfigured   = errors.New("RESEARCH_RANKER_NOT_CONFIGURED")
)

// WebResearchResolver is the only script source resolver allowed to access
// external web content. It deliberately does not modify the generic LLM
// client, so translations and metadata generation remain offline.
type WebResearchResolver struct {
	searcher      scriptports.WebSearcher
	fetcher       scriptports.WebPageFetcher
	cache         scriptports.TopicSourceCache
	lexicon       *linguistics.LexiconRegistry
	ranker        scriptports.ResearchRanker
	subject       string
	coordinator   researchSearchCoordinatorPort
	policyVersion string
}

// researchSearchCoordinatorPort is the port consumed by the resolver
// for subject-aware multi-provider search. Defined as an interface to
// keep the resolver free of infrastructure imports.
type researchSearchCoordinatorPort interface {
	SearchWithFallback(ctx context.Context, subject string, queries []string, targetPool int) []CoordinatorSearchResult
}

// CoordinatorSearchResult carries a subject-valid search hit with metadata.
// Exported so the composition adapter (app package) can satisfy the port
// without circular imports.
type CoordinatorSearchResult struct {
	Hit        scriptports.WebSearchHit
	Provider   string
	QueryLevel int
}

// ResearchSubmissionPreflight is the cache-only submission gate used by the
// HTTP enqueue path. It shares the resolver's cache-key and policy semantics
// without requiring a searcher or page fetcher.
//
// ResearchPreflight validates source-cache policies before script.generate is
// enqueued. It never performs web search or page fetching.
type ResearchPreflight interface {
	Validate(ctx context.Context, item scriptpkg.GenerationItemV2) error
}

type ResearchSubmissionPreflight struct {
	cache         scriptports.TopicSourceCache
	policyVersion string
}

// SetResearchPolicyVersion injects the opaque provider-policy token that
// the research cache fingerprint folds in. It MUST be identical between
// the submission preflight and the worker resolver (both are wired from
// the same composition config), so SearXNG-only and SearXNG+DDG
// deployments produce distinct cache keys.
func (p *ResearchSubmissionPreflight) SetResearchPolicyVersion(v string) {
	if p != nil {
		p.policyVersion = strings.TrimSpace(v)
	}
}

func NewResearchSubmissionPreflight(cache scriptports.TopicSourceCache) *ResearchSubmissionPreflight {
	return &ResearchSubmissionPreflight{cache: cache}
}

func (p *ResearchSubmissionPreflight) Validate(ctx context.Context, item scriptpkg.GenerationItemV2) error {
	if item.Source.Type != scriptpkg.SourceResearch {
		return nil
	}
	if p == nil {
		return nil
	}
	src := item.Source
	if len(src.Research.Candidates) > 0 {
		if aggregateResearchCacheAvailable(ctx, p.cache, researchAggregateCacheKey(strings.TrimSpace(src.Topic), item.Language, src, p.policyVersion), src.CachePolicy.Mode) {
			return nil
		}
		for index, candidate := range src.Research.Candidates {
			child := item
			child.Source = src
			child.Source.Research.Candidates = nil
			child.Source.Topic = strings.TrimSpace(candidate) + " boxing career earnings business endorsements financial history"
			child.Source.Query = ""
			if err := p.Validate(ctx, child); err != nil {
				return fmt.Errorf("research candidate %d: %w", index, err)
			}
		}
		return nil
	}
	mode := normalizeCacheMode(src.CachePolicy.Mode)
	if !src.Search && (mode == scriptpkg.SourceCacheModeDisabled || mode == scriptpkg.SourceCacheModeForceRefresh) {
		return researchPreflightError(ErrResearchDisabledCacheMiss)
	}
	if mode == scriptpkg.SourceCacheModeDisabled || mode == scriptpkg.SourceCacheModeForceRefresh {
		return nil
	}
	if p.cache == nil {
		if mode == scriptpkg.SourceCacheModeCacheOnly {
			return researchPreflightError(ErrResearchCacheMiss)
		}
		if !src.Search {
			return researchPreflightError(ErrResearchDisabledCacheMiss)
		}
		return nil
	}
	_, _, _, _, key := researchCacheIdentity(src, item.Language, p.policyVersion)
	text, err := p.cache.GetResearchCache(ctx, key)
	if err != nil {
		return researchPreflightError(ErrResearchCacheMiss)
	}
	if strings.TrimSpace(text) != "" {
		return nil
	}
	if mode == scriptpkg.SourceCacheModeCacheOnly {
		return researchPreflightError(ErrResearchCacheMiss)
	}
	if !src.Search {
		return researchPreflightError(ErrResearchDisabledCacheMiss)
	}
	return nil
}

func NewWebResearchResolver(searcher scriptports.WebSearcher, fetcher scriptports.WebPageFetcher) *WebResearchResolver {
	return &WebResearchResolver{searcher: searcher, fetcher: fetcher, lexicon: linguistics.DefaultLexiconOrNil()}
}

// SetLexicon injects the configured linguistic registry. Research quality
// checks must use the same file-backed SSOT as the rest of the application.
func (r *WebResearchResolver) SetLexicon(registry *linguistics.LexiconRegistry) error {
	if registry == nil {
		return fmt.Errorf("research resolver: nil lexicon registry")
	}
	r.lexicon = registry
	return nil
}

func (r *WebResearchResolver) SetResearchRanker(ranker scriptports.ResearchRanker) error {
	if ranker == nil {
		return ErrResearchRankerNotConfigured
	}
	r.ranker = ranker
	return nil
}

func (r *WebResearchResolver) SetCache(cache scriptports.TopicSourceCache) {
	if r != nil {
		r.cache = cache
	}
}

// SetSearchCoordinator injects the subject-aware search coordinator.
// When set, single-candidate Resolve() delegates search to the
// coordinator instead of the raw searcher, enabling multi-provider
// fallback with subject filtering.
func (r *WebResearchResolver) SetSearchCoordinator(coordinator researchSearchCoordinatorPort) {
	if r != nil {
		r.coordinator = coordinator
	}
}

// SetResearchPolicyVersion injects the opaque provider-policy token for
// the research cache fingerprint. See ResearchSubmissionPreflight.
func (r *WebResearchResolver) SetResearchPolicyVersion(v string) {
	if r != nil {
		r.policyVersion = strings.TrimSpace(v)
	}
}

// Validate checks research cache policy synchronously at submission time.
// Search-enabled cache misses are allowed to continue to the worker; all
// offline misses fail before a script.generate job exists.
func (r *WebResearchResolver) Validate(ctx context.Context, item scriptpkg.GenerationItemV2) error {
	if item.Source.Type != scriptpkg.SourceResearch {
		return nil
	}
	src := item.Source
	if len(src.Research.Candidates) > 0 {
		if aggregateResearchCacheAvailable(ctx, r.cache, researchAggregateCacheKey(strings.TrimSpace(src.Topic), item.Language, src, r.policyVersion), src.CachePolicy.Mode) {
			return nil
		}
		for index, candidate := range src.Research.Candidates {
			child := item
			child.Source = src
			child.Source.Research.Candidates = nil
			child.Source.Topic = strings.TrimSpace(candidate) + " boxing career earnings business endorsements financial history"
			child.Source.Query = ""
			if err := r.Validate(ctx, child); err != nil {
				return fmt.Errorf("research candidate %d: %w", index, err)
			}
		}
		return nil
	}
	mode := normalizeCacheMode(src.CachePolicy.Mode)
	if !src.Search && (mode == scriptpkg.SourceCacheModeDisabled || mode == scriptpkg.SourceCacheModeForceRefresh) {
		return researchPreflightError(ErrResearchDisabledCacheMiss)
	}
	if mode == scriptpkg.SourceCacheModeDisabled || mode == scriptpkg.SourceCacheModeForceRefresh {
		return nil
	}
	if r.cache == nil {
		if mode == scriptpkg.SourceCacheModeCacheOnly || !src.Search {
			if mode == scriptpkg.SourceCacheModeCacheOnly {
				return researchPreflightError(ErrResearchCacheMiss)
			}
			return researchPreflightError(ErrResearchDisabledCacheMiss)
		}
		return nil
	}
	_, _, _, _, key := researchCacheIdentity(src, item.Language, r.policyVersion)
	text, err := r.cache.GetResearchCache(ctx, key)
	if err != nil {
		return researchPreflightError(ErrResearchCacheMiss)
	}
	if strings.TrimSpace(text) != "" {
		return nil
	}
	if mode == scriptpkg.SourceCacheModeCacheOnly {
		return researchPreflightError(ErrResearchCacheMiss)
	}
	if !src.Search {
		return researchPreflightError(ErrResearchDisabledCacheMiss)
	}
	return nil
}

func researchPreflightError(err error) error {
	return &scriptpkg.PayloadValidationError{Code: err.Error(), Message: err.Error(), Stage: "request.validation", Retryable: false}
}

func (r *WebResearchResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	if r == nil || r.searcher == nil || r.fetcher == nil {
		return nil, ErrResearchProviderNotConfigured
	}
	if len(src.Research.Candidates) > 0 {
		return r.resolveCandidates(ctx, src, resCtx)
	}
	topic := strings.TrimSpace(src.Topic)
	if topic == "" {
		topic = strings.TrimSpace(src.Query)
	}
	if topic == "" {
		return nil, &scriptpkg.NoSourceError{ItemID: resCtx.ItemID, Reason: "research source requires topic or query"}
	}
	policy := src.Research
	if policy.MaxQueries <= 0 {
		policy.MaxQueries = researchDefaultMaxQueries
	}
	if policy.ResultsPerQuery <= 0 {
		policy.ResultsPerQuery = researchDefaultResults
	}
	if policy.MaxPages <= 0 {
		policy.MaxPages = researchDefaultMaxPages
	}
	if policy.MinSources <= 0 {
		policy.MinSources = researchDefaultMinSources
	}
	if policy.MinEvidenceScore <= 0 {
		// A full page is weighted 1.0 and a search snippet 0.55. This
		// default means three snippets alone do not satisfy a three-source
		// financial research gate, while one full page plus two snippets does.
		policy.MinEvidenceScore = float64(policy.MinSources) * 0.7
	}
	if policy.MinFullPageSources <= 0 && policy.MinSources >= 3 {
		policy.MinFullPageSources = 1
	}
	if policy.MaxRounds <= 0 {
		policy.MaxRounds = 1
	}
	if policy.MaxRounds > 2 {
		policy.MaxRounds = 2
	}
	if policy.TimeoutSeconds <= 0 {
		policy.TimeoutSeconds = researchDefaultTimeout
	}
	deadline := time.Duration(policy.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	queries := researchQueries(topic, src.Query, policy.MaxQueries)
	lang := resCtx.Language
	if lang == "" {
		lang = "it"
	}
	cacheMode := normalizeCacheMode(src.CachePolicy.Mode)
	version := strings.TrimSpace(src.CachePolicy.Version)
	if version == "" {
		version = researchVersion
	}
	fingerprint := researchFingerprint(queries, policy, r.policyVersion)
	key := scriptpkg.ComputeResearchCacheKey(hashResearch(topic), lang, version, fingerprint, policy.MaxPages)
	searchEnabled := src.Search
	forceRefresh := src.ForceRefresh || cacheMode == scriptpkg.SourceCacheModeForceRefresh
	reportBase := scriptpkg.ResearchReport{SearchEnabled: searchEnabled, CacheKey: key, ResearchVersion: version, Queries: queries}
	if !forceRefresh && cacheMode != scriptpkg.SourceCacheModeDisabled && r.cache != nil {
		cached, err := r.cache.GetResearchCache(ctx, key)
		if err == nil && strings.TrimSpace(cached) != "" {
			report := reportBase
			report.Status = "CACHE_HIT"
			report.Mode = "cache_hit"
			report.CacheHit = true
			report.Queries = nil
			report.Searched = false
			report.CacheSaved = false
			if recordReader, ok := r.cache.(interface {
				GetResearchCacheRecord(context.Context, string) (scriptpkg.ResearchCacheRecord, error)
			}); ok {
				if saved, recordErr := recordReader.GetResearchCacheRecord(ctx, key); recordErr == nil && strings.TrimSpace(saved.ResearchReportJSON) != "" {
					var savedReport scriptpkg.ResearchReport
					if json.Unmarshal([]byte(saved.ResearchReportJSON), &savedReport) == nil {
						savedReport.Status = "CACHE_HIT"
						savedReport.Mode = "cache_hit"
						savedReport.CacheHit = true
						savedReport.SearchEnabled = searchEnabled
						savedReport.Searched = false
						savedReport.CacheSaved = false
						savedReport.CacheKey = key
						savedReport.ResearchVersion = version
						savedReport.Queries = nil
						report = savedReport
					}
				}
			}
			return &scriptpkg.ResolvedSource{Type: scriptpkg.SourceResearch, Topic: topic, Title: researchTitle(topic, resCtx.Title), SourceText: cached, Language: lang, GroundingPolicy: src.GroundingPolicy, Fingerprint: key, ResearchReport: &report}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrResearchCacheMiss, err)
		}
	}
	if cacheMode == scriptpkg.SourceCacheModeCacheOnly {
		return nil, ErrResearchCacheMiss
	}
	if !searchEnabled {
		return nil, ErrResearchDisabledCacheMiss
	}
	report := &reportBase
	report.Status = "RUNNING"
	report.Mode = "web_research"
	report.Searched = true
	seen := map[string]bool{}
	type researchCandidate struct {
		source scriptpkg.ResearchWebSource
		query  string
	}
	var candidates []researchCandidate
	// Search hits and page fetches have different failure modes: providers
	// frequently return several URLs whose hosts answer 403. Do not stop
	// collecting at MaxPages, or those blocked URLs consume the whole budget
	// and make a well-known subject look undocumented. Keep a bounded retry
	// pool and still cap accepted sources at MaxPages below.
	candidateLimit := policy.MaxPages * 3
	if candidateLimit < policy.MaxPages+policy.MinSources*2 {
		candidateLimit = policy.MaxPages + policy.MinSources*2
	}
	if candidateLimit > 48 {
		candidateLimit = 48
	}
	// When the search coordinator is available, delegate the entire search
	// phase to it. The coordinator handles identity resolution, query
	// planning, multi-provider fallback, and subject filtering.
	//
	// targetPool 0 delegates to the coordinator's configured default
	// (RESEARCH_TARGET_POOL_SIZE, default 8): the search stops as soon as
	// the subject-valid pool is sufficient instead of burning every query ×
	// provider. candidateLimit remains the fetch-phase budget for the
	// non-coordinator fallback path below.
	if r.coordinator != nil && r.subject != "" {
		coordinatorResults := r.coordinator.SearchWithFallback(ctx, r.subject, queries, 0)
		for _, cr := range coordinatorResults {
			raw := strings.TrimSpace(cr.Hit.URL)
			u, err := url.Parse(raw)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || seen[raw] {
				continue
			}
			seen[raw] = true
			candidates = append(candidates, researchCandidate{source: scriptpkg.ResearchWebSource{Title: strings.TrimSpace(cr.Hit.Title), URL: raw, Excerpt: trimResearch(cr.Hit.Content, 600)}, query: cr.Hit.Title})
			if len(candidates) >= candidateLimit {
				break
			}
		}
	} else {
		var lastSearchErr error
		for _, query := range queries {
			hits, err := r.searcher.Search(ctx, query, policy.ResultsPerQuery)
			if err != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return nil, fmt.Errorf("%w: %v", ErrResearchTimeout, ctx.Err())
				}
				lastSearchErr = err
				continue
			}
			for _, hit := range hits {
				if r.subject != "" && !researchHitMatchesSubject(r.subject, hit.Title+" "+hit.Content) {
					continue
				}
				raw := strings.TrimSpace(hit.URL)
				u, err := url.Parse(raw)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || seen[raw] {
					continue
				}
				seen[raw] = true
				candidates = append(candidates, researchCandidate{source: scriptpkg.ResearchWebSource{Title: strings.TrimSpace(hit.Title), URL: raw, Excerpt: trimResearch(hit.Content, 600)}, query: query})
				if len(candidates) >= candidateLimit {
					break
				}
			}
			if len(candidates) >= candidateLimit {
				break
			}
		}
		if len(candidates) == 0 && lastSearchErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrResearchSearchFailed, lastSearchErr)
		}
	}
	var source strings.Builder
	suspiciousPages := 0
	var lastFetchErr error
	for _, candidate := range candidates {
		if len(report.Sources) >= policy.MaxPages {
			break
		}
		report.PagesRequested++
		page, err := r.fetcher.Fetch(ctx, candidate.source.URL, 2000)
		if err != nil {
			report.PagesFailed++
			lastFetchErr = fmt.Errorf("%s: %w", candidate.source.URL, err)
			// A search result already contains a provider-supplied title and
			// excerpt. When the target page blocks automated fetching (403,
			// HTTP/2 peer errors, robots), retain that bounded result only if
			// it independently passes the same relevance and injection checks.
			// This keeps the URL provenance while avoiding a false cache miss
			// for subjects whose authoritative pages are temporarily blocked.
			if strings.TrimSpace(candidate.source.Excerpt) != "" {
				snippetPage := scriptports.WebPage{
					Title: candidate.source.Title,
					Text:  candidate.source.Excerpt,
				}
				if valid, _ := r.validateResearchSource(topic, candidate.query, lang, policy.FreshnessDays, snippetPage); valid {
					s := candidate.source
					s.ID = fmt.Sprintf("S%d", len(report.Sources)+1)
					s.AccessMode = scriptpkg.EvidenceAccessSnippet
					s.Confidence = 0.55
					s.Excerpt = trimResearch(s.Excerpt, 2000)
					report.Sources = append(report.Sources, s)
					report.Claims = append(report.Claims, scriptpkg.ResearchClaim{Text: s.Excerpt, SourceIDs: []string{s.ID}, Verified: true})
					source.WriteString(fmt.Sprintf("[%s] %s\n", s.ID, s.Excerpt))
				}
			}
			continue
		}
		report.PagesFetched++
		if strings.TrimSpace(page.Title) == "" {
			page.Title = candidate.source.Title
		}
		valid, reason := r.validateResearchSource(topic, candidate.query, lang, policy.FreshnessDays, page)
		if !valid {
			report.RejectedSources++
			if strings.Contains(reason, "prompt injection") {
				suspiciousPages++
			}
			continue
		}
		s := candidate.source
		s.ID = fmt.Sprintf("S%d", len(report.Sources)+1)
		s.AccessMode = scriptpkg.EvidenceAccessFullPage
		s.Confidence = 1.0
		if page.Title != "" {
			s.Title = page.Title
		}
		if page.Publisher != "" {
			s.Publisher = page.Publisher
		}
		if page.PublishedAt != "" {
			s.PublishedAt = page.PublishedAt
		}
		if text := trimResearch(page.Text, 2000); text != "" {
			s.Excerpt = text
		}
		if strings.TrimSpace(s.Excerpt) != "" {
			source.WriteString(fmt.Sprintf("[%s] %s\n", s.ID, s.Excerpt))
			report.Claims = append(report.Claims, scriptpkg.ResearchClaim{Text: s.Excerpt, SourceIDs: []string{s.ID}, Verified: true})
		}
		report.Sources = append(report.Sources, s)
	}
	report.AcceptedSources = len(report.Sources)
	for _, accepted := range report.Sources {
		if accepted.AccessMode == scriptpkg.EvidenceAccessSnippet {
			report.SnippetSources++
			report.EvidenceScore += 0.55
			continue
		}
		report.FullPageSources++
		report.EvidenceScore += 1.0
	}
	if report.AcceptedSources < policy.MinSources ||
		report.FullPageSources < policy.MinFullPageSources ||
		report.EvidenceScore < policy.MinEvidenceScore {
		if suspiciousPages > 0 {
			return nil, fmt.Errorf("%w: %d suspicious pages", ErrResearchPromptInjection, suspiciousPages)
		}
		if lastFetchErr != nil {
			return nil, fmt.Errorf("%w: accepted %d/%d sources, full pages %d/%d, evidence score %.2f/%.2f: %v", ErrResearchInsufficientSources, report.AcceptedSources, policy.MinSources, report.FullPageSources, policy.MinFullPageSources, report.EvidenceScore, policy.MinEvidenceScore, lastFetchErr)
		}
		return nil, fmt.Errorf("%w: accepted %d/%d sources, full pages %d/%d, evidence score %.2f/%.2f", ErrResearchInsufficientSources, report.AcceptedSources, policy.MinSources, report.FullPageSources, policy.MinFullPageSources, report.EvidenceScore, policy.MinEvidenceScore)
	}
	report.QualityGatePassed = true
	if report.PagesFetched == 0 && report.AcceptedSources == 0 {
		return nil, ErrResearchFetchFailed
	}
	seed := strings.TrimSpace(src.SourceText)
	sourceText := fmt.Sprintf("Topic: %s\nSeed context:\n%s\nResearch sources:\n%s\nUse only these sourced facts. Preserve source markers such as [S1]. Do not invent unsupported claims.", topic, seed, strings.TrimSpace(source.String()))
	report.Status = "SUCCEEDED"
	if r.cache != nil && cacheMode != scriptpkg.SourceCacheModeDisabled {
		now := time.Now()
		ttlHours := src.CachePolicy.TTLHours
		if ttlHours <= 0 {
			ttlHours = researchDefaultCacheTTLHour
		}
		report.CacheSaved = true
		reportJSON, err := json.Marshal(report)
		if err != nil {
			report.CacheSaved = false
			return nil, fmt.Errorf("%w: report encode: %v", ErrResearchCacheMiss, err)
		}
		if err := r.cache.SaveResearchCache(ctx, scriptpkg.ResearchCacheRecord{
			Key: key, Topic: topic, Language: lang, MaxSteps: policy.MaxPages, SourceText: sourceText,
			SourceTextHash: hashResearch(sourceText), ResearchReportJSON: string(reportJSON),
			SourcesCount: len(report.Sources), ClaimsVerified: countVerifiedClaims(report.Claims),
			ClaimsRejected: countRejectedClaims(report.Claims), SearchQueryCount: len(report.Queries), PagesFetched: report.PagesFetched,
			TopicFingerprint: hashResearch(topic), SourceFingerprint: fingerprint, ResolverVersion: "webresearch", ResearchVersion: version,
			ExpiresAt: now.Add(time.Duration(ttlHours) * time.Hour), CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			report.CacheSaved = false
			return nil, fmt.Errorf("%w: cache save: %v", ErrResearchCacheMiss, err)
		}
	}
	return &scriptpkg.ResolvedSource{Type: scriptpkg.SourceResearch, Topic: topic, Title: researchTitle(topic, resCtx.Title), SourceText: sourceText, Language: lang, GroundingPolicy: src.GroundingPolicy, Fingerprint: key, ResearchReport: report}, nil
}

func countVerifiedClaims(claims []scriptpkg.ResearchClaim) int {
	count := 0
	for _, claim := range claims {
		if claim.Verified {
			count++
		}
	}
	return count
}

func countRejectedClaims(claims []scriptpkg.ResearchClaim) int {
	count := 0
	for _, claim := range claims {
		if !claim.Verified {
			count++
		}
	}
	return count
}
