// Merged 2026-10-10 with research_cache.go: the research cache is the
// persistence half of the research contract defined here.
package evidence

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/spec"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// SubjectIdentity is the canonical identity for a research subject. It
// replaces the hardcoded diacritics switch in research_subject_filter.go
// with a data-driven lookup that supports aliases, required context terms,
// and excluded terms for disambiguation (e.g. Floyd Mayweather vs George
// Floyd).
type SubjectIdentity struct {
	ID            string   `json:"id"`
	CanonicalName string   `json:"canonical_name"`
	Aliases       []string `json:"aliases,omitempty"`
	RequiredTerms []string `json:"required_terms,omitempty"`
	ExcludedTerms []string `json:"excluded_terms,omitempty"`
	SubjectType   string   `json:"subject_type,omitempty"`
}

// EvidenceAccessMode records how a web source was obtained. Search snippets
// are useful resilience fallbacks, but they are weaker than a fetched page
// and must not be silently treated as equivalent evidence.
type EvidenceAccessMode string

const (
	EvidenceAccessFullPage EvidenceAccessMode = "full_page"
	EvidenceAccessSnippet  EvidenceAccessMode = "search_snippet"
)

// ResearchWebSource is a bounded, sanitized web source retained as provenance.
type ResearchWebSource struct {
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	URL         string             `json:"url"`
	Publisher   string             `json:"publisher,omitempty"`
	PublishedAt string             `json:"published_at,omitempty"`
	Excerpt     string             `json:"excerpt,omitempty"`
	AccessMode  EvidenceAccessMode `json:"access_mode,omitempty"`
	Confidence  float64            `json:"confidence,omitempty"`
}

type ResearchClaim struct {
	Text      string   `json:"text"`
	SourceIDs []string `json:"source_ids"`
	Verified  bool     `json:"verified"`
}

// DroppedResearchCandidate records a candidate that failed the research
// quality gate and was excluded from the ranking instead of failing the
// whole fanout. A non-empty list means the resulting ranking is partial
// and must be surfaced as uncertain.
type DroppedResearchCandidate struct {
	CandidateID string `json:"candidate_id"`
	Reason      string `json:"reason"`
}

type ResearchReport struct {
	Status            string                `json:"status"`
	Mode              string                `json:"mode,omitempty"`
	SearchEnabled     bool                  `json:"search_enabled"`
	Searched          bool                  `json:"searched"`
	CacheSaved        bool                  `json:"cache_saved"`
	CacheKey          string                `json:"cache_key,omitempty"`
	ResearchVersion   string                `json:"research_version,omitempty"`
	Queries           []string              `json:"queries,omitempty"`
	Sources           []ResearchWebSource   `json:"sources,omitempty"`
	Claims            []ResearchClaim       `json:"claims,omitempty"`
	PagesRequested    int                   `json:"pages_requested"`
	PagesFetched      int                   `json:"pages_fetched"`
	PagesFailed       int                   `json:"pages_failed"`
	AcceptedSources   int                   `json:"accepted_sources"`
	FullPageSources   int                   `json:"full_page_sources"`
	SnippetSources    int                   `json:"snippet_sources"`
	EvidenceScore     float64               `json:"evidence_score"`
	RejectedSources   int                   `json:"rejected_sources"`
	QualityGatePassed bool                  `json:"quality_gate_passed"`
	CacheHit          bool                  `json:"cache_hit"`
	Evidence          *ResearchEvidencePack `json:"evidence,omitempty"`
	// Ranking records the metric and strategy that produced the candidate
	// order, including any deterministic fallback. It makes degradation
	// observable instead of silently changing the ranking criterion.
	Ranking *ResearchRankingInfo `json:"ranking,omitempty"`
	// DroppedCandidates lists candidates excluded at research time (below
	// the evidence gate). When non-empty the aggregate is partial and the
	// ranking must be treated as uncertain.
	DroppedCandidates []DroppedResearchCandidate `json:"dropped_candidates,omitempty"`
}

// ResearchCacheRecord is owned by kernel/script/spec; the alias keeps the
// evidence package naming stable.
type ResearchCacheRecord = spec.ResearchCacheRecord

// ComputeResearchCacheKey returns the canonical SHA-256 hex key for a
// research_cache entry. The keyspace is defined as:
//
//	SHA256(topic_fingerprint + ":" + language + ":" + research_version + ":" + source_fingerprint + ":" + max_steps)
//
// The separators prevent collisions between adjacent components (e.g.
// topic_fingerprint="ab" + language="c" vs topic_fingerprint="a" +
// language="bc"). max_steps is represented as a decimal integer so the
// same source material researched with different depths produces
// distinct keys.
func ComputeResearchCacheKey(topicFingerprint, language, researchVersion, sourceFingerprint string, maxSteps int) string {
	topicFingerprint = strings.TrimSpace(topicFingerprint)
	language = strings.TrimSpace(language)
	researchVersion = strings.TrimSpace(researchVersion)
	sourceFingerprint = strings.TrimSpace(sourceFingerprint)

	payload := fmt.Sprintf(
		"%s:%s:%s:%s:%d",
		topicFingerprint,
		language,
		researchVersion,
		sourceFingerprint,
		maxSteps,
	)

	sum := digest.SHA256Bytes([]byte(payload))
	return sum
}
