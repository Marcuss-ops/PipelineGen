package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

const (
	researchVersion             = "web-research-v1"
	researchDefaultMaxQueries   = 4
	researchDefaultResults      = 5
	researchDefaultMaxPages     = 8
	researchDefaultMinSources   = 3
	researchDefaultTimeout      = 90
	researchDefaultCacheTTLHour = 168
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
)

// WebResearchResolver is the only script source resolver allowed to access
// external web content. It deliberately does not modify the generic LLM
// client, so translations and metadata generation remain offline.
type WebResearchResolver struct {
	searcher scriptports.WebSearcher
	fetcher  scriptports.WebPageFetcher
	cache    scriptports.TopicSourceCache
}

func NewWebResearchResolver(searcher scriptports.WebSearcher, fetcher scriptports.WebPageFetcher) *WebResearchResolver {
	return &WebResearchResolver{searcher: searcher, fetcher: fetcher}
}

func (r *WebResearchResolver) SetCache(cache scriptports.TopicSourceCache) {
	if r != nil {
		r.cache = cache
	}
}

func (r *WebResearchResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	if r == nil || r.searcher == nil || r.fetcher == nil {
		return nil, ErrResearchProviderNotConfigured
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
	fingerprint := hashResearch(strings.Join(queries, "\n"))
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
	for _, query := range queries {
		hits, err := r.searcher.Search(ctx, query, policy.ResultsPerQuery)
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("%w: %v", ErrResearchTimeout, ctx.Err())
			}
			return nil, fmt.Errorf("%w: %v", ErrResearchSearchFailed, err)
		}
		for _, hit := range hits {
			raw := strings.TrimSpace(hit.URL)
			u, err := url.Parse(raw)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || seen[raw] {
				continue
			}
			seen[raw] = true
			report.Sources = append(report.Sources, scriptpkg.ResearchWebSource{ID: fmt.Sprintf("S%d", len(report.Sources)+1), Title: strings.TrimSpace(hit.Title), URL: raw, Excerpt: trimResearch(hit.Content, 600)})
			if len(report.Sources) >= policy.MaxPages {
				break
			}
		}
		if len(report.Sources) >= policy.MaxPages {
			break
		}
	}
	if len(report.Sources) < policy.MinSources {
		return nil, fmt.Errorf("%w: found %d sources, need %d", ErrResearchInsufficientSources, len(report.Sources), policy.MinSources)
	}
	var source strings.Builder
	suspiciousPages := 0
	var lastFetchErr error
	for i := range report.Sources {
		s := &report.Sources[i]
		report.PagesRequested++
		page, err := r.fetcher.Fetch(ctx, s.URL, 2000)
		if err != nil {
			report.PagesFailed++
			lastFetchErr = err
			continue
		}
		if suspiciousResearchText(page.Text) || suspiciousResearchText(page.Title) {
			report.PagesFailed++
			suspiciousPages++
			continue
		}
		report.PagesFetched++
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
	}
	if report.PagesFetched < policy.MinSources && policy.RequireCitations {
		if suspiciousPages > 0 {
			return nil, fmt.Errorf("%w: %d suspicious pages", ErrResearchPromptInjection, suspiciousPages)
		}
		if lastFetchErr != nil {
			return nil, fmt.Errorf("%w: fetched %d pages, need %d: %v", ErrResearchInsufficientSources, report.PagesFetched, policy.MinSources, lastFetchErr)
		}
		return nil, fmt.Errorf("%w: fetched %d pages, need %d", ErrResearchInsufficientSources, report.PagesFetched, policy.MinSources)
	}
	if report.PagesFetched == 0 {
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
		reportJSON, err := json.Marshal(report)
		if err != nil {
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
			return nil, fmt.Errorf("%w: cache save: %v", ErrResearchCacheMiss, err)
		}
		report.CacheSaved = true
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

func researchQueries(topic, explicit string, max int) []string {
	base := strings.TrimSpace(explicit)
	if base == "" {
		base = topic
	}
	// Keep the caller's/base query first. Some SearXNG engine profiles
	// return a canonical knowledge result only for the short subject query;
	// enriched variants remain useful for corroborating evidence.
	queries := []string{base, base + " reliable biography career finances", base + " official record career", base + " financial history reputable sources", base + " recovery business documented"}
	if max < len(queries) {
		queries = queries[:max]
	}
	return queries
}

func researchTitle(topic, title string) string {
	if strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	return topic
}
func hashResearch(s string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(s))))
	return hex.EncodeToString(h[:])
}
func trimResearch(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func suspiciousResearchText(s string) bool {
	s = strings.ToLower(s)
	for _, marker := range []string{"ignore previous instructions", "ignore all previous", "reveal the admin token", "print the admin token", "system prompt", "developer message"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}
