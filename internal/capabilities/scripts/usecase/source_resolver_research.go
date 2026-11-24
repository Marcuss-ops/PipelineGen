// Package scripts — source_resolver_research.go is the research source
// resolver core: the WebResearchResolver (the only script source
// resolver allowed to access external web content) and its Resolve flow
// (cache hit / web search / page fetch / source assembly). The pure query +
// cache-key helpers live in source_research_queries.go, the source-quality
// validation in source_research_validate.go, and the cache-only submission
// gate in source_resolver_preflight.go.
//
// Split to satisfy the strict per-file LOC cap,
// architecture/policy.yaml#max_lines_per_file_strict.
package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase/gencore"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
	queryPlannerVersion = "v2"
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
	lexicon       *linguistics.Profiles
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

func NewWebResearchResolver(searcher scriptports.WebSearcher, fetcher scriptports.WebPageFetcher) *WebResearchResolver {
	return &WebResearchResolver{searcher: searcher, fetcher: fetcher, lexicon: linguistics.DefaultProfilesOrNil()}
}

// SetLexicon injects the configured linguistic registry. Research quality
// checks must use the same file-backed SSOT as the rest of the application.
func (r *WebResearchResolver) SetLexicon(registry *linguistics.Profiles) error {
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

// Resolve runs the research flow as four phases: cache lookup, candidate
// collection, bounded page fetch, and the source-quality gate plus cache write.
// Each phase is a helper so the gates that decide whether a script gets sourced
// facts are individually readable and testable.
func (r *WebResearchResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	if r == nil || r.searcher == nil || r.fetcher == nil {
		return nil, ErrResearchProviderNotConfigured
	}
	if len(src.Research.Candidates) > 0 {
		return r.resolveCandidates(ctx, src, resCtx)
	}
	topic := researchTopic(src)
	if topic == "" {
		return nil, &scriptpkg.NoSourceError{ItemID: resCtx.ItemID, Reason: "research source requires topic or query"}
	}
	policy := normalizeResearchPolicy(src.Research)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(policy.TimeoutSeconds)*time.Second)
	defer cancel()
	ex := newResearchExecution(src, policy, topic, resCtx.Language, r.policyVersion)

	cached, err := r.lookupResearchCache(ctx, src, ex, resCtx.Title)
	if err != nil {
		return nil, err
	}
	if cached != nil {
		return cached, nil
	}
	if ex.cacheMode == scriptpkg.SourceCacheModeCacheOnly {
		return nil, ErrResearchCacheMiss
	}
	if !ex.searchEnabled {
		return nil, ErrResearchDisabledCacheMiss
	}

	ex.report.Status = "RUNNING"
	ex.report.Mode = "web_research"
	ex.report.Searched = true
	candidates, err := r.collectResearchCandidates(ctx, ex)
	if err != nil {
		return nil, err
	}
	body, suspicious, lastFetchErr := r.fetchResearchSources(ctx, ex, candidates)
	return r.finishResearch(ctx, src, resCtx, ex, body, suspicious, lastFetchErr)
}

// researchExecution is the per-call state that the phases share: resolved
// policy, cache identity, and the report being accumulated.
type researchExecution struct {
	topic, lang, version, key, fingerprint, cacheMode string
	searchEnabled, forceRefresh                       bool
	policy                                            scriptpkg.ResearchPolicy
	report                                            *scriptpkg.ResearchReport
}

type researchCandidate struct {
	source scriptpkg.ResearchWebSource
	query  string
}

// normalizeResearchPolicy fills the documented defaults. The evidence floor is
// deliberately 0.7 × MinSources: three snippets alone cannot satisfy a
// three-source financial gate, while one full page plus two snippets can.
func normalizeResearchPolicy(policy scriptpkg.ResearchPolicy) scriptpkg.ResearchPolicy {
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
	return policy
}

func newResearchExecution(src scriptpkg.SourceSpec, policy scriptpkg.ResearchPolicy, topic, lang, policyVersion string) *researchExecution {
	if lang == "" {
		lang = "it"
	}
	cacheMode := gencore.NormalizeCacheMode(src.CachePolicy.Mode)
	version := strings.TrimSpace(src.CachePolicy.Version)
	if version == "" {
		version = researchVersion
	}
	queries := researchQueries(topic, src.Query, policy.MaxQueries, resolveRankingMetric(src, topic))
	fingerprint := researchFingerprint(queries, policy, policyVersion)
	key := scriptpkg.ComputeResearchCacheKey(hashResearch(topic), lang, version, fingerprint, policy.MaxPages)
	searchEnabled := src.Search
	return &researchExecution{
		topic: topic, lang: lang, version: version, key: key, fingerprint: fingerprint,
		cacheMode:     cacheMode,
		searchEnabled: searchEnabled,
		forceRefresh:  src.ForceRefresh || cacheMode == scriptpkg.SourceCacheModeForceRefresh,
		policy:        policy,
		report:        &scriptpkg.ResearchReport{SearchEnabled: searchEnabled, CacheKey: key, ResearchVersion: version, Queries: queries},
	}
}

// lookupResearchCache returns (nil, nil) when the flow must continue with a live
// search, the stored resolution on a hit, and a typed cache-miss error when the
// backend itself fails (the pre-existing contract).
func (r *WebResearchResolver) lookupResearchCache(ctx context.Context, src scriptpkg.SourceSpec, ex *researchExecution, title string) (*scriptpkg.ResolvedSource, error) {
	if ex.forceRefresh || ex.cacheMode == scriptpkg.SourceCacheModeDisabled || r.cache == nil {
		return nil, nil
	}
	cached, err := r.cache.GetResearchCache(ctx, ex.key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrResearchCacheMiss, err)
	}
	if strings.TrimSpace(cached) == "" {
		return nil, nil
	}
	report := *ex.report
	report.Status = "CACHE_HIT"
	report.Mode = "cache_hit"
	report.CacheHit = true
	report.Queries = nil
	report.Searched = false
	report.CacheSaved = false
	if recordReader, ok := r.cache.(interface {
		GetResearchCacheRecord(context.Context, string) (scriptpkg.ResearchCacheRecord, error)
	}); ok {
		if saved, recordErr := recordReader.GetResearchCacheRecord(ctx, ex.key); recordErr == nil && strings.TrimSpace(saved.ResearchReportJSON) != "" {
			var savedReport scriptpkg.ResearchReport
			if json.Unmarshal([]byte(saved.ResearchReportJSON), &savedReport) == nil {
				savedReport.Status = "CACHE_HIT"
				savedReport.Mode = "cache_hit"
				savedReport.CacheHit = true
				savedReport.SearchEnabled = ex.searchEnabled
				savedReport.Searched = false
				savedReport.CacheSaved = false
				savedReport.CacheKey = ex.key
				savedReport.ResearchVersion = ex.version
				savedReport.Queries = nil
				report = savedReport
			}
		}
	}
	return &scriptpkg.ResolvedSource{
		Type: scriptpkg.SourceResearch, Topic: ex.topic, Title: researchTitle(ex.topic, title),
		SourceText: cached, Language: ex.lang, GroundingPolicy: src.GroundingPolicy,
		Fingerprint: ex.key, ResearchReport: &report,
	}, nil
}

// collectResearchCandidates delegates to the subject-aware coordinator when it
// is wired, otherwise queries the raw searcher. The candidate pool is larger
// than MaxPages because providers routinely return URLs whose hosts answer 403:
// blocked pages must not consume the whole fetch budget.
func (r *WebResearchResolver) collectResearchCandidates(ctx context.Context, ex *researchExecution) ([]researchCandidate, error) {
	policy := ex.policy
	candidateLimit := policy.MaxPages * 3
	if candidateLimit < policy.MaxPages+policy.MinSources*2 {
		candidateLimit = policy.MaxPages + policy.MinSources*2
	}
	if candidateLimit > 48 {
		candidateLimit = 48
	}
	seen := map[string]bool{}
	var candidates []researchCandidate

	if r.coordinator != nil && r.subject != "" {
		for _, cr := range r.coordinator.SearchWithFallback(ctx, r.subject, ex.report.Queries, 0) {
			raw := strings.TrimSpace(cr.Hit.URL)
			u, err := url.Parse(raw)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || seen[raw] {
				continue
			}
			seen[raw] = true
			candidates = append(candidates, researchCandidate{
				source: scriptpkg.ResearchWebSource{Title: strings.TrimSpace(cr.Hit.Title), URL: raw, Excerpt: trimResearch(cr.Hit.Content, 600)},
				query:  cr.Hit.Title,
			})
			if len(candidates) >= candidateLimit {
				break
			}
		}
		return candidates, nil
	}

	var lastSearchErr error
	for _, query := range ex.report.Queries {
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
			candidates = append(candidates, researchCandidate{
				source: scriptpkg.ResearchWebSource{Title: strings.TrimSpace(hit.Title), URL: raw, Excerpt: trimResearch(hit.Content, 600)},
				query:  query,
			})
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
	return candidates, nil
}

// fetchResearchSources fetches at most MaxPages accepted pages. A blocked page
// keeps its bounded search snippet only when the snippet itself passes the same
// relevance and injection checks; this preserves provenance without a false
// cache miss for subjects whose authoritative pages are temporarily down.
func (r *WebResearchResolver) fetchResearchSources(ctx context.Context, ex *researchExecution, candidates []researchCandidate) (string, int, error) {
	var body strings.Builder
	suspicious := 0
	var lastFetchErr error
	for _, candidate := range candidates {
		if len(ex.report.Sources) >= ex.policy.MaxPages {
			break
		}
		ex.report.PagesRequested++
		page, err := r.fetcher.Fetch(ctx, candidate.source.URL, 2000)
		if err != nil {
			ex.report.PagesFailed++
			lastFetchErr = fmt.Errorf("%s: %w", candidate.source.URL, err)
			r.acceptResearchSnippet(ex, candidate, &body, &suspicious)
			continue
		}
		ex.report.PagesFetched++
		if strings.TrimSpace(page.Title) == "" {
			page.Title = candidate.source.Title
		}
		valid, reason := r.validateResearchSource(ex.topic, candidate.query, ex.lang, ex.policy.FreshnessDays, page)
		if !valid {
			ex.report.RejectedSources++
			if strings.Contains(reason, "prompt injection") {
				suspicious++
			}
			continue
		}
		s := candidate.source
		s.ID = fmt.Sprintf("S%d", len(ex.report.Sources)+1)
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
			fmt.Fprintf(&body, "[%s] %s\n", s.ID, s.Excerpt)
			ex.report.Claims = append(ex.report.Claims, scriptpkg.ResearchClaim{Text: s.Excerpt, SourceIDs: []string{s.ID}, Verified: true})
		}
		ex.report.Sources = append(ex.report.Sources, s)
	}
	return body.String(), suspicious, lastFetchErr
}

func (r *WebResearchResolver) acceptResearchSnippet(ex *researchExecution, candidate researchCandidate, body *strings.Builder, suspicious *int) {
	if strings.TrimSpace(candidate.source.Excerpt) == "" {
		return
	}
	snippetPage := scriptports.WebPage{Title: candidate.source.Title, Text: candidate.source.Excerpt}
	valid, reason := r.validateResearchSource(ex.topic, candidate.query, ex.lang, ex.policy.FreshnessDays, snippetPage)
	if !valid {
		if strings.Contains(reason, "prompt injection") {
			*suspicious++
		}
		return
	}
	s := candidate.source
	s.ID = fmt.Sprintf("S%d", len(ex.report.Sources)+1)
	s.AccessMode = scriptpkg.EvidenceAccessSnippet
	s.Confidence = 0.55
	s.Excerpt = trimResearch(s.Excerpt, 2000)
	ex.report.Sources = append(ex.report.Sources, s)
	ex.report.Claims = append(ex.report.Claims, scriptpkg.ResearchClaim{Text: s.Excerpt, SourceIDs: []string{s.ID}, Verified: true})
	fmt.Fprintf(body, "[%s] %s\n", s.ID, s.Excerpt)
}

// finishResearch applies the evidence gate, assembles the sourced prompt
// fragment and writes the cache entry. Every error here is one of the typed
// research sentinels already part of the resolver contract.
func (r *WebResearchResolver) finishResearch(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext, ex *researchExecution, body string, suspicious int, lastFetchErr error) (*scriptpkg.ResolvedSource, error) {
	report := ex.report
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
	if report.AcceptedSources < ex.policy.MinSources ||
		report.FullPageSources < ex.policy.MinFullPageSources ||
		report.EvidenceScore < ex.policy.MinEvidenceScore {
		if suspicious > 0 {
			return nil, fmt.Errorf("%w: %d suspicious pages", ErrResearchPromptInjection, suspicious)
		}
		if lastFetchErr != nil {
			return nil, fmt.Errorf("%w: accepted %d/%d sources, full pages %d/%d, evidence score %.2f/%.2f: %v", ErrResearchInsufficientSources, report.AcceptedSources, ex.policy.MinSources, report.FullPageSources, ex.policy.MinFullPageSources, report.EvidenceScore, ex.policy.MinEvidenceScore, lastFetchErr)
		}
		return nil, fmt.Errorf("%w: accepted %d/%d sources, full pages %d/%d, evidence score %.2f/%.2f", ErrResearchInsufficientSources, report.AcceptedSources, ex.policy.MinSources, report.FullPageSources, ex.policy.MinFullPageSources, report.EvidenceScore, ex.policy.MinEvidenceScore)
	}
	report.QualityGatePassed = true
	if report.PagesFetched == 0 && report.AcceptedSources == 0 {
		return nil, ErrResearchFetchFailed
	}
	seed := strings.TrimSpace(src.SourceText)
	sourceText := fmt.Sprintf("Topic: %s\nSeed context:\n%s\nResearch sources:\n%s\nUse only these sourced facts. Preserve source markers such as [S1]. Do not invent unsupported claims.", ex.topic, seed, strings.TrimSpace(body))
	report.Status = "SUCCEEDED"
	if err := r.saveResearchCache(ctx, src, ex, sourceText); err != nil {
		return nil, err
	}
	return &scriptpkg.ResolvedSource{
		Type: scriptpkg.SourceResearch, Topic: ex.topic, Title: researchTitle(ex.topic, resCtx.Title),
		SourceText: sourceText, Language: ex.lang, GroundingPolicy: src.GroundingPolicy,
		Fingerprint: ex.key, ResearchReport: report,
	}, nil
}

func (r *WebResearchResolver) saveResearchCache(ctx context.Context, src scriptpkg.SourceSpec, ex *researchExecution, sourceText string) error {
	if r.cache == nil || ex.cacheMode == scriptpkg.SourceCacheModeDisabled {
		return nil
	}
	now := time.Now()
	ttlHours := src.CachePolicy.TTLHours
	if ttlHours <= 0 {
		ttlHours = researchDefaultCacheTTLHour
	}
	ex.report.CacheSaved = true
	reportJSON, err := json.Marshal(ex.report)
	if err != nil {
		ex.report.CacheSaved = false
		return fmt.Errorf("%w: report encode: %v", ErrResearchCacheMiss, err)
	}
	if err := r.cache.SaveResearchCache(ctx, scriptpkg.ResearchCacheRecord{
		Key: ex.key, Topic: ex.topic, Language: ex.lang, MaxSteps: ex.policy.MaxPages, SourceText: sourceText,
		SourceTextHash: hashResearch(sourceText), ResearchReportJSON: string(reportJSON),
		SourcesCount: len(ex.report.Sources), ClaimsVerified: countVerifiedClaims(ex.report.Claims),
		ClaimsRejected: countRejectedClaims(ex.report.Claims), SearchQueryCount: len(ex.report.Queries), PagesFetched: ex.report.PagesFetched,
		TopicFingerprint: hashResearch(ex.topic), SourceFingerprint: ex.fingerprint, ResolverVersion: "webresearch", ResearchVersion: ex.version,
		ExpiresAt: now.Add(time.Duration(ttlHours) * time.Hour), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		ex.report.CacheSaved = false
		return fmt.Errorf("%w: cache save: %v", ErrResearchCacheMiss, err)
	}
	return nil
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
