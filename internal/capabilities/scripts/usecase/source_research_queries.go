// Package scripts — source_research_queries.go holds the pure query /
// cache-key helpers for the research source resolver: query building
// (researchQueries), cache-identity derivation (researchCacheIdentity),
// content hashing (hashResearch / researchFingerprint), and title /
// excerpt trimming (researchTitle / trimResearch). Extracted from
// source_resolver_research.go on 2026-08-07 to satisfy the strict
// per-file LOC cap (architecture/policy.yaml#max_lines_per_file_strict).
//
// All functions are package-private and shared with the resolver core
// (source_resolver_research.go) and its validation helpers
// (source_research_validate.go) in the same package.
package usecase

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// researchCacheIdentity derives the cache identity for a research source:
// the canonical topic, the query set, the raw cache policy, the research
// version, and the final cache key. Callers (preflight Validate and
// Resolve) must use this single derivation so the cache key is identical
// across the submission gate and the worker execution.
func researchCacheIdentity(src scriptpkg.SourceSpec, language, policyVersion string) (string, []string, scriptpkg.SourceCachePolicy, string, string) {
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
	queries := researchQueries(topic, src.Query, policy.MaxQueries, resolveRankingMetric(src, topic))
	lang := strings.TrimSpace(language)
	if lang == "" {
		lang = "it"
	}
	version := strings.TrimSpace(src.CachePolicy.Version)
	if version == "" {
		version = researchVersion
	}
	fingerprint := researchFingerprint(queries, policy, policyVersion)
	key := scriptpkg.ComputeResearchCacheKey(hashResearch(topic), lang, version, fingerprint, policy.MaxPages)
	return topic, queries, src.CachePolicy, version, key
}

func researchQueries(topic, explicit string, max int, metric scriptpkg.RankingMetric) []string {
	query := strings.TrimSpace(explicit)
	if query == "" {
		query = strings.TrimSpace(topic)
	}
	if query == "" {
		return nil
	}
	if max <= 1 {
		return []string{query}
	}

	// Delegate to the QueryPlanner for data-driven, metric-aware query
	// escalation. The planner uses the SubjectIdentity registry for
	// alias-aware query generation instead of hardcoded variants.
	resolver := NewSubjectIdentityResolver()
	identity := resolver.Resolve(query)
	planner := NewQueryPlannerForMetric(metric)
	plan := planner.FullPlan(identity, max)
	if len(plan) == 0 {
		// Fallback: if the planner returns nothing (unknown identity),
		// use subject-agnostic legacy queries. No domain qualifier is
		// hardcoded here: the subject filter owns relevance.
		plan = []string{
			query,
			query + " biography",
			query + " career",
			query + " achievements",
		}
	}
	if len(plan) > max {
		plan = plan[:max]
	}
	return plan
}

// researchFingerprint is the cache-fingerprint material for the research
// source. It folds in the semantic version tokens (research pipeline,
// identity registry, query planner) plus the injected provider-policy
// token (provider set + target pool): a SearXNG-only result and a
// SearXNG+DDG fallback result may produce different evidence, so they
// must never share a cache entry.
func researchFingerprint(queries []string, policy scriptpkg.ResearchPolicy, policyVersion string) string {
	return hashResearch(fmt.Sprintf(
		"research_version:%s\nidentity_version:%s\nquery_planner_version:%s\nprovider_policy:%s\n%s\nfreshness_days:%d\nresults_per_query:%d\nmax_pages:%d\nmin_sources:%d\nmin_full_page_sources:%d\nmin_evidence_score:%.4f\nrequire_citations:%t",
		researchVersion, identityVersion, queryPlannerVersion, policyVersion,
		strings.Join(queries, "\n"), policy.FreshnessDays, policy.ResultsPerQuery,
		policy.MaxPages, policy.MinSources, policy.MinFullPageSources,
		policy.MinEvidenceScore, policy.RequireCitations,
	))
}

func researchTitle(topic, title string) string {
	if strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	return topic
}

func hashResearch(s string) string {
	h := digest.SHA256Bytes([]byte(strings.ToLower(strings.TrimSpace(s))))
	return h
}

func trimResearch(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
