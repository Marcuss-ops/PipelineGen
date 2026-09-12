// Package usecase — source_resolver_preflight.go: the cache-only research
// submission gate.
//
// ResearchSubmissionPreflight (and the ResearchPreflight contract it
// satisfies) shares the resolver's cache-key and policy semantics WITHOUT a
// searcher or a page fetcher: it validates the source-cache policy
// synchronously on the HTTP enqueue path, so a search-enabled cache miss
// continues to the worker while every offline miss fails before a
// script.generate job ever exists. WebResearchResolver.Validate is the same
// gate evaluated on the resolver side.
//
// Extracted 2026-09-12 from source_resolver_research.go to keep every file
// under max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package usecase

import (
	"context"
	"fmt"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase/gencore"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

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
			child.Source.Research.RankingMetric = resolveRankingMetric(src, strings.TrimSpace(src.Topic)).String()
			child.Source.Topic = strings.TrimSpace(candidate)
			child.Source.Query = researchSubjectIdentity(strings.TrimSpace(candidate)).CanonicalName
			if err := p.Validate(ctx, child); err != nil {
				return fmt.Errorf("research candidate %d: %w", index, err)
			}
		}
		return nil
	}
	mode := gencore.NormalizeCacheMode(src.CachePolicy.Mode)
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
			child.Source.Research.RankingMetric = resolveRankingMetric(src, strings.TrimSpace(src.Topic)).String()
			child.Source.Topic = strings.TrimSpace(candidate)
			child.Source.Query = researchSubjectIdentity(strings.TrimSpace(candidate)).CanonicalName
			if err := r.Validate(ctx, child); err != nil {
				return fmt.Errorf("research candidate %d: %w", index, err)
			}
		}
		return nil
	}
	mode := gencore.NormalizeCacheMode(src.CachePolicy.Mode)
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
