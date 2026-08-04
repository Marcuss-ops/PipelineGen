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
	"unicode"

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
	lexicon  *linguistics.LexiconRegistry
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

type ResearchSubmissionPreflight struct{ cache scriptports.TopicSourceCache }

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
	_, _, _, _, key := researchCacheIdentity(src, item.Language)
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

func (r *WebResearchResolver) SetCache(cache scriptports.TopicSourceCache) {
	if r != nil {
		r.cache = cache
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
	_, _, _, _, key := researchCacheIdentity(src, item.Language)
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

func researchCacheIdentity(src scriptpkg.SourceSpec, language string) (string, []string, scriptpkg.SourceCachePolicy, string, string) {
	topic := strings.TrimSpace(src.Topic)
	if topic == "" {
		topic = strings.TrimSpace(src.Query)
	}
	policy := src.Research
	if policy.MaxQueries <= 0 {
		policy.MaxQueries = researchDefaultMaxQueries
	}
	if policy.MaxPages <= 0 {
		policy.MaxPages = researchDefaultMaxPages
	}
	queries := researchQueries(topic, src.Query, policy.MaxQueries)
	lang := strings.TrimSpace(language)
	if lang == "" {
		lang = "it"
	}
	version := strings.TrimSpace(src.CachePolicy.Version)
	if version == "" {
		version = researchVersion
	}
	fingerprint := researchFingerprint(queries, policy.FreshnessDays)
	key := scriptpkg.ComputeResearchCacheKey(hashResearch(topic), lang, version, fingerprint, policy.MaxPages)
	return topic, queries, src.CachePolicy, version, key
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
	fingerprint := researchFingerprint(queries, policy.FreshnessDays)
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
			candidates = append(candidates, researchCandidate{source: scriptpkg.ResearchWebSource{Title: strings.TrimSpace(hit.Title), URL: raw, Excerpt: trimResearch(hit.Content, 600)}, query: query})
			if len(candidates) >= policy.MaxPages {
				break
			}
		}
		if len(candidates) >= policy.MaxPages {
			break
		}
	}
	var source strings.Builder
	suspiciousPages := 0
	var lastFetchErr error
	for _, candidate := range candidates {
		report.PagesRequested++
		page, err := r.fetcher.Fetch(ctx, candidate.source.URL, 2000)
		if err != nil {
			report.PagesFailed++
			lastFetchErr = err
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
	if report.AcceptedSources < policy.MinSources {
		if suspiciousPages > 0 {
			return nil, fmt.Errorf("%w: %d suspicious pages", ErrResearchPromptInjection, suspiciousPages)
		}
		if lastFetchErr != nil {
			return nil, fmt.Errorf("%w: accepted %d sources, need %d: %v", ErrResearchInsufficientSources, report.AcceptedSources, policy.MinSources, lastFetchErr)
		}
		return nil, fmt.Errorf("%w: accepted %d sources, need %d", ErrResearchInsufficientSources, report.AcceptedSources, policy.MinSources)
	}
	report.QualityGatePassed = true
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

func researchQueries(topic, explicit string, max int) []string {
	_ = max
	query := strings.TrimSpace(explicit)
	if query == "" {
		query = strings.TrimSpace(topic)
	}
	if query == "" {
		return nil
	}
	return []string{query}
}

func researchFingerprint(queries []string, freshnessDays int) string {
	return hashResearch(fmt.Sprintf("%s\nfreshness_days:%d", strings.Join(queries, "\n"), freshnessDays))
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

func validateResearchSource(topic, query, language string, freshnessDays int, page scriptports.WebPage) (bool, string) {
	return validateResearchSourceWithLexicon(topic, query, language, freshnessDays, page, nil)
}

func (r *WebResearchResolver) validateResearchSource(topic, query, language string, freshnessDays int, page scriptports.WebPage) (bool, string) {
	if r.lexicon == nil {
		return false, "lexicon registry is not configured"
	}
	return validateResearchSourceWithLexicon(topic, query, language, freshnessDays, page, r.lexicon)
}

func validateResearchSourceWithLexicon(topic, query, language string, freshnessDays int, page scriptports.WebPage, registry *linguistics.LexiconRegistry) (bool, string) {
	if strings.TrimSpace(page.Text) == "" {
		return false, "empty page text"
	}
	text := strings.TrimSpace(page.Title + " " + page.Text)
	if suspiciousResearchText(text) {
		return false, "prompt injection detected"
	}

	var stopWords map[string]struct{}
	if registry != nil {
		profile, err := registry.ResolveRequired(language)
		if err != nil {
			return false, err.Error()
		}
		stopWords = profile.StopWords
	}
	terms := researchSignificantTerms(topic+" "+query, stopWords)
	if len(terms) == 0 {
		return false, "no significant research terms"
	}
	pageTerms := researchTokens(text)
	matches := 0
	for term := range terms {
		if _, ok := pageTerms[term]; ok {
			matches++
		}
	}
	if matches < 2 && matches*2 < len(terms) {
		return false, fmt.Sprintf("insufficient topic relevance: matched %d of %d terms", matches, len(terms))
	}

	lang := strings.ToLower(strings.TrimSpace(language))
	if lang != "" {
		// The production resolver is injected with the registry at bootstrap.
		// The standalone helper remains usable for transport-level tests where
		// no language policy is requested.
		if registry != nil {
			profile, err := registry.ResolveRequired(lang)
			if err != nil {
				return false, err.Error()
			}
			markers := profile.StopWords
			languageMatches := 0
			for marker := range markers {
				if _, exists := pageTerms[marker]; exists {
					languageMatches++
				}
			}
			if languageMatches < 2 {
				return false, fmt.Sprintf("page language does not match %s", lang)
			}
		}
	}

	if freshnessDays > 0 {
		published, ok := parseResearchDate(page.PublishedAt)
		if !ok {
			return false, "published_at required for freshness validation"
		}
		cutoff := time.Now().UTC().Add(-time.Duration(freshnessDays) * 24 * time.Hour)
		if published.Before(cutoff) {
			return false, fmt.Sprintf("page is older than %d days", freshnessDays)
		}
	}
	return true, ""
}

func researchSignificantTerms(text string, stopWords map[string]struct{}) map[string]struct{} {
	terms := researchTokens(text)
	for term := range terms {
		if _, stop := stopWords[term]; stop || len([]rune(term)) < 3 {
			delete(terms, term)
		}
	}
	return terms
}

func researchTokens(text string) map[string]struct{} {
	result := make(map[string]struct{})
	var token strings.Builder
	flush := func() {
		if token.Len() > 0 {
			result[strings.ToLower(token.String())] = struct{}{}
			token.Reset()
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			token.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return result
}

func parseResearchDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02", "2006/01/02", "02 Jan 2006", "January 2, 2006"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}
