package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

const researchFanoutDefaultWorkers = 4

type candidateResearchResult struct {
	Index       int
	CandidateID string
	Label       string
	Resolved    *scriptpkg.ResolvedSource
	Report      *scriptpkg.ResearchReport
	Fingerprint string
	CacheKey    string
}

func (r *WebResearchResolver) resolveCandidates(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	candidates := make([]string, len(src.Research.Candidates))
	seen := make(map[string]struct{}, len(candidates))
	for i, candidate := range src.Research.Candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return nil, fmt.Errorf("%w: research candidate %d is empty", ErrResearchInsufficientSources, i)
		}
		key := strings.ToLower(candidate)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: duplicate research candidate %q", ErrResearchInsufficientSources, candidate)
		}
		seen[key] = struct{}{}
		candidates[i] = candidate
	}
	topic := strings.TrimSpace(src.Topic)
	if topic == "" {
		topic = strings.TrimSpace(resCtx.Title)
	}
	lang := resCtx.Language
	if lang == "" {
		lang = "it"
	}
	aggregateKey := researchAggregateCacheKey(topic, lang, src, r.policyVersion)
	if cached := r.loadAggregateCache(ctx, aggregateKey, src, topic, lang, resCtx); cached != nil {
		return cached, nil
	}
	if normalizeCacheMode(src.CachePolicy.Mode) == scriptpkg.SourceCacheModeCacheOnly {
		return nil, ErrResearchCacheMiss
	}
	if r.ranker == nil {
		return nil, ErrResearchRankerNotConfigured
	}

	child := src
	child.Research.Candidates = nil
	child.Topic = ""
	child.Query = ""
	workers := src.Research.MaxParallel
	if workers <= 0 {
		workers = researchFanoutDefaultWorkers
	}
	resolved, err := concurrent.Map(ctx, candidates, workers, func(childCtx context.Context, index int, candidate string) (*candidateResearchResult, error) {
		candidateSource := child
		candidateSource.Topic = candidate + " boxer"
		// Keep the seed query broad enough for providers such as SearXNG to
		// return results. The candidate-aware subject filter is the final
		// relevance gate; forcing quotes plus "wikipedia" made otherwise
		// well-documented candidates return zero results on some engines.
		candidateSource.Query = researchCandidateSearchName(candidate) + " boxing"
		candidateSource.Research.MaxQueries = src.Research.MaxQueries
		if candidateSource.Research.MaxQueries <= 0 {
			candidateSource.Research.MaxQueries = 1
		}
		candidateContext := resCtx
		candidateContext.ItemID = fmt.Sprintf("%s:research:%02d", resCtx.ItemID, index)
		candidateContext.Title = candidate
		candidateResolver := *r
		candidateResolver.subject = candidate
		result, resolveErr := candidateResolver.Resolve(childCtx, candidateSource, candidateContext)
		if resolveErr != nil {
			return nil, fmt.Errorf("candidate %q: %w", candidate, resolveErr)
		}
		if result.ResearchReport == nil || !result.ResearchReport.QualityGatePassed {
			return nil, fmt.Errorf("candidate %q failed research quality gate", candidate)
		}
		return &candidateResearchResult{Index: index, CandidateID: candidate, Label: candidate, Resolved: result, Report: result.ResearchReport, Fingerprint: result.Fingerprint, CacheKey: result.ResearchReport.CacheKey}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("research fan-out failed: %w", err)
	}

	inputs := make([]scriptports.ResearchCandidateRankingInput, len(resolved))
	for i, result := range resolved {
		inputs[i] = scriptports.ResearchCandidateRankingInput{CandidateID: result.CandidateID, Label: result.Label, Sources: result.Report.Sources, Claims: result.Report.Claims}
	}
	ranking, err := r.ranker.Rank(ctx, topic, inputs)
	if err != nil {
		return nil, fmt.Errorf("research ranking failed: %w", err)
	}
	if err := validateResearchRanking(inputs, ranking); err != nil {
		return nil, err
	}
	pack, aggregateReport, err := buildResearchEvidencePack(topic, resolved, ranking)
	if err != nil {
		return nil, err
	}
	aggregateReport.CacheKey = aggregateKey
	aggregateReport.Evidence = pack
	aggregateReport.CacheSaved = false
	projection := pack.ModelSourceText()
	if r.cache != nil && normalizeCacheMode(src.CachePolicy.Mode) != scriptpkg.SourceCacheModeDisabled {
		if err := r.saveAggregateCache(ctx, aggregateKey, src, topic, lang, pack, aggregateReport, projection); err != nil {
			return nil, err
		}
		aggregateReport.CacheSaved = true
	}
	return &scriptpkg.ResolvedSource{Type: scriptpkg.SourceResearch, Topic: topic, Title: researchTitle(topic, resCtx.Title), SourceText: projection, Language: lang, Fingerprint: pack.Fingerprint, ResearchReport: aggregateReport, ResearchEvidence: pack}, nil
}

func validateResearchRanking(inputs []scriptports.ResearchCandidateRankingInput, ranking []scriptports.ResearchCandidateRanking) error {
	if len(ranking) != len(inputs) {
		return fmt.Errorf("research ranking incomplete: expected %d candidates, got %d", len(inputs), len(ranking))
	}
	expected := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		expected[input.CandidateID] = struct{}{}
	}
	seenCandidates := make(map[string]struct{}, len(ranking))
	seenRanks := make(map[int]struct{}, len(ranking))
	for _, ranked := range ranking {
		if _, ok := expected[ranked.CandidateID]; !ok {
			return fmt.Errorf("research ranking returned unknown candidate %q", ranked.CandidateID)
		}
		if _, ok := seenCandidates[ranked.CandidateID]; ok {
			return fmt.Errorf("research ranking duplicated candidate %q", ranked.CandidateID)
		}
		seenCandidates[ranked.CandidateID] = struct{}{}
		if ranked.Rank < 1 || ranked.Rank > len(inputs) {
			return fmt.Errorf("candidate %q returned invalid rank %d", ranked.CandidateID, ranked.Rank)
		}
		if _, ok := seenRanks[ranked.Rank]; ok {
			return fmt.Errorf("duplicate research rank %d", ranked.Rank)
		}
		seenRanks[ranked.Rank] = struct{}{}
	}
	for rank := 1; rank <= len(inputs); rank++ {
		if _, ok := seenRanks[rank]; !ok {
			return fmt.Errorf("research ranking missing rank %d", rank)
		}
	}
	return nil
}

func buildResearchEvidencePack(topic string, results []*candidateResearchResult, ranking []scriptports.ResearchCandidateRanking) (*scriptpkg.ResearchEvidencePack, *scriptpkg.ResearchReport, error) {
	byCandidate := make(map[string]*candidateResearchResult, len(results))
	for _, result := range results {
		byCandidate[result.CandidateID] = result
	}
	pack := &scriptpkg.ResearchEvidencePack{Version: scriptpkg.ResearchEvidenceVersion, Topic: topic}
	aggregate := &scriptpkg.ResearchReport{Status: "SUCCEEDED", Mode: "multi_candidate", SearchEnabled: true, Searched: true, QualityGatePassed: true, ResearchVersion: researchVersion}
	for _, ranked := range ranking {
		result, ok := byCandidate[ranked.CandidateID]
		if !ok {
			return nil, nil, fmt.Errorf("missing research result for candidate %q", ranked.CandidateID)
		}
		idMap := make(map[string]string, len(result.Report.Sources))
		sources := make([]scriptpkg.ResearchWebSource, len(result.Report.Sources))
		for i, source := range result.Report.Sources {
			newID := ranked.CandidateID + ":" + source.ID
			idMap[source.ID] = newID
			source.ID = newID
			sources[i] = source
			aggregate.Sources = append(aggregate.Sources, source)
		}
		claims := make([]scriptpkg.ResearchClaim, len(result.Report.Claims))
		for i, claim := range result.Report.Claims {
			claimCopy := claim
			claimCopy.SourceIDs = make([]string, len(claim.SourceIDs))
			for j, oldID := range claim.SourceIDs {
				newID, ok := idMap[oldID]
				if !ok {
					return nil, nil, fmt.Errorf("candidate %q claim references unknown source %q", ranked.CandidateID, oldID)
				}
				claimCopy.SourceIDs[j] = newID
			}
			claims[i] = claimCopy
			aggregate.Claims = append(aggregate.Claims, claimCopy)
		}
		aggregate.Queries = append(aggregate.Queries, result.Report.Queries...)
		aggregate.PagesRequested += result.Report.PagesRequested
		aggregate.PagesFetched += result.Report.PagesFetched
		aggregate.PagesFailed += result.Report.PagesFailed
		aggregate.RejectedSources += result.Report.RejectedSources
		pack.Candidates = append(pack.Candidates, scriptpkg.RankedResearchCandidate{CandidateID: ranked.CandidateID, Label: result.Label, Rank: ranked.Rank, Score: ranked.Score, Rationale: ranked.Rationale, Fingerprint: result.Fingerprint, CacheKey: result.CacheKey, Sources: sources, Claims: claims})
	}
	aggregate.AcceptedSources = len(aggregate.Sources)
	if err := pack.Validate(); err != nil {
		return nil, nil, err
	}
	fingerprint, err := pack.ComputeFingerprint()
	if err != nil {
		return nil, nil, err
	}
	pack.Fingerprint = fingerprint
	return pack, aggregate, nil
}

func researchAggregateCacheKey(topic, language string, src scriptpkg.SourceSpec, policyVersion string) string {
	version := strings.TrimSpace(src.CachePolicy.Version)
	if version == "" {
		version = researchVersion
	}
	version += "|" + scriptpkg.ResearchEvidenceVersion
	if policyVersion != "" {
		// The aggregate key must differ across provider policies: an
		// aggregate produced with a DDG fallback may carry different
		// evidence than a SearXNG-only one.
		version += "|" + policyVersion
	}
	policy := fmt.Sprintf("%d|%d|%d|%d|%t|%s", src.Research.MaxPages, src.Research.MinSources, src.Research.FreshnessDays, src.Research.MaxParallel, src.Research.RequireCitations, strings.Join(src.Research.Candidates, "\n"))
	return scriptpkg.ComputeResearchCacheKey(hashResearch(topic), language, version, hashResearch(policy), src.Research.MaxPages)
}

func (r *WebResearchResolver) loadAggregateCache(ctx context.Context, key string, src scriptpkg.SourceSpec, topic, language string, resCtx scriptpkg.SourceResolutionContext) *scriptpkg.ResolvedSource {
	if r.cache == nil || normalizeCacheMode(src.CachePolicy.Mode) == scriptpkg.SourceCacheModeDisabled || src.ForceRefresh || normalizeCacheMode(src.CachePolicy.Mode) == scriptpkg.SourceCacheModeForceRefresh {
		return nil
	}
	cached, err := r.cache.GetResearchCache(ctx, key)
	if err != nil || strings.TrimSpace(cached) == "" {
		return nil
	}
	recordReader, ok := r.cache.(interface {
		GetResearchCacheRecord(context.Context, string) (scriptpkg.ResearchCacheRecord, error)
	})
	if !ok {
		return nil
	}
	record, err := recordReader.GetResearchCacheRecord(ctx, key)
	if err != nil {
		return nil
	}
	var report scriptpkg.ResearchReport
	if json.Unmarshal([]byte(record.ResearchReportJSON), &report) != nil || report.Evidence == nil || report.Evidence.Validate() != nil {
		return nil
	}
	report.Status, report.Mode, report.CacheHit = "CACHE_HIT", "multi_candidate_cache_hit", true
	report.CacheSaved, report.CacheKey, report.Queries = false, key, nil
	return &scriptpkg.ResolvedSource{Type: scriptpkg.SourceResearch, Topic: topic, Title: researchTitle(topic, resCtx.Title), SourceText: cached, Language: language, Fingerprint: report.Evidence.Fingerprint, ResearchReport: &report, ResearchEvidence: report.Evidence}
}

func aggregateResearchCacheAvailable(ctx context.Context, cache scriptports.TopicSourceCache, key, mode string) bool {
	if cache == nil || normalizeCacheMode(mode) == scriptpkg.SourceCacheModeDisabled {
		return false
	}
	recordReader, ok := cache.(interface {
		GetResearchCacheRecord(context.Context, string) (scriptpkg.ResearchCacheRecord, error)
	})
	if !ok {
		return false
	}
	record, err := recordReader.GetResearchCacheRecord(ctx, key)
	if err != nil || strings.TrimSpace(record.SourceText) == "" {
		return false
	}
	var report scriptpkg.ResearchReport
	return json.Unmarshal([]byte(record.ResearchReportJSON), &report) == nil && report.Evidence != nil && report.Evidence.Validate() == nil
}

func (r *WebResearchResolver) saveAggregateCache(ctx context.Context, key string, src scriptpkg.SourceSpec, topic, language string, pack *scriptpkg.ResearchEvidencePack, report *scriptpkg.ResearchReport, projection string) error {
	now := time.Now()
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("%w: aggregate report encode: %v", ErrResearchCacheMiss, err)
	}
	ttlHours := src.CachePolicy.TTLHours
	if ttlHours <= 0 {
		ttlHours = researchDefaultCacheTTLHour
	}
	if err := r.cache.SaveResearchCache(ctx, scriptpkg.ResearchCacheRecord{Key: key, Topic: topic, Language: language, MaxSteps: src.Research.MaxPages, SourceText: projection, SourceTextHash: hashResearch(projection), ResearchReportJSON: string(reportJSON), SourcesCount: len(report.Sources), ClaimsVerified: countVerifiedClaims(report.Claims), ClaimsRejected: countRejectedClaims(report.Claims), SearchQueryCount: len(report.Queries), PagesFetched: report.PagesFetched, TopicFingerprint: hashResearch(topic), SourceFingerprint: pack.Fingerprint, ResolverVersion: "webresearch-fanout", ResearchVersion: researchVersion + "|" + scriptpkg.ResearchEvidenceVersion, ExpiresAt: now.Add(time.Duration(ttlHours) * time.Hour), CreatedAt: now, UpdatedAt: now}); err != nil {
		return fmt.Errorf("%w: aggregate cache save: %v", ErrResearchCacheMiss, err)
	}
	return nil
}
