package usecase

import (
	"sort"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ResearchPlan is the immutable execution plan produced before any external
// search or page fetch. It makes policy decisions visible to the I/O stages.
type ResearchPlan struct {
	Topic            string
	Language         string
	Queries          []string
	MaxPages         int
	MinSources       int
	MinFullPage      int
	MinEvidenceScore float64
	SearchEnabled    bool
	CacheMode        string
	ForceRefresh     bool
}

// ResearchEvidence is the normalized, provider-independent evidence unit.
type ResearchEvidence struct {
	Source scriptpkg.ResearchWebSource
	Claim  scriptpkg.ResearchClaim
}

func buildResearchPlan(src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) ResearchPlan {
	topic := strings.TrimSpace(src.Topic)
	if topic == "" {
		topic = strings.TrimSpace(src.Query)
	}
	policy := src.Research
	if policy.MaxQueries <= 0 { policy.MaxQueries = researchDefaultMaxQueries }
	if policy.MaxPages <= 0 { policy.MaxPages = researchDefaultMaxPages }
	if policy.MinSources <= 0 { policy.MinSources = researchDefaultMinSources }
	if policy.MinFullPageSources <= 0 && policy.MinSources >= 3 { policy.MinFullPageSources = 1 }
	if policy.MinEvidenceScore <= 0 { policy.MinEvidenceScore = float64(policy.MinSources) * 0.7 }
	language := resCtx.Language
	if language == "" { language = "it" }
	metric := resolveRankingMetric(src, topic)
	return ResearchPlan{
		Topic: topic, Language: language,
		Queries: researchQueries(topic, src.Query, maxResearchQueries(policy.MaxQueries), metric),
		MaxPages: policy.MaxPages, MinSources: policy.MinSources,
		MinFullPage: policy.MinFullPageSources, MinEvidenceScore: policy.MinEvidenceScore,
		SearchEnabled: src.Search, CacheMode: normalizeCacheMode(src.CachePolicy.Mode),
		ForceRefresh: src.ForceRefresh || normalizeCacheMode(src.CachePolicy.Mode) == scriptpkg.SourceCacheModeForceRefresh,
	}
}

func maxResearchQueries(value int) int {
	if value <= 0 { return researchDefaultMaxQueries }
	return value
}

func normalizeResearchEvidence(source scriptpkg.ResearchWebSource, claimText string, access scriptpkg.EvidenceAccessMode, confidence float64) ResearchEvidence {
	source.Title = strings.TrimSpace(source.Title)
	source.URL = strings.TrimSpace(source.URL)
	source.Excerpt = strings.TrimSpace(source.Excerpt)
	source.AccessMode = access
	source.Confidence = confidence
	sourceID := strings.TrimSpace(source.ID)
	return ResearchEvidence{Source: source, Claim: scriptpkg.ResearchClaim{Text: strings.TrimSpace(claimText), SourceIDs: []string{sourceID}, Verified: true}}
}

func deduplicateResearchEvidence(in []ResearchEvidence) []ResearchEvidence {
	out := make([]ResearchEvidence, 0, len(in))
	seenURLs := make(map[string]struct{}, len(in))
	for _, item := range in {
		key := strings.ToLower(strings.TrimSpace(item.Source.URL))
		if key == "" { continue }
		if _, exists := seenURLs[key]; exists { continue }
		seenURLs[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func rankResearchEvidence(in []ResearchEvidence) []ResearchEvidence {
	out := append([]ResearchEvidence(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Source.Confidence != out[j].Source.Confidence {
			return out[i].Source.Confidence > out[j].Source.Confidence
		}
		return out[i].Source.URL < out[j].Source.URL
	})
	return out
}

func applyResearchGroundingPolicy(plan ResearchPlan, evidence []ResearchEvidence) ([]ResearchEvidence, error) {
	selected := rankResearchEvidence(deduplicateResearchEvidence(evidence))
	if len(selected) < plan.MinSources {
		return nil, ErrResearchInsufficientSources
	}
	fullPages := 0
	score := 0.0
	for _, item := range selected {
		if item.Source.AccessMode == scriptpkg.EvidenceAccessFullPage { fullPages++ }
		if item.Source.AccessMode == scriptpkg.EvidenceAccessSnippet { score += 0.55 } else { score += 1 }
	}
	if fullPages < plan.MinFullPage || score < plan.MinEvidenceScore {
		return nil, ErrResearchInsufficientSources
	}
	return selected, nil
}

func researchRankingInputs(evidence []ResearchEvidence) []scriptports.ResearchCandidateRankingInput {
	inputs := make([]scriptports.ResearchCandidateRankingInput, 0, len(evidence))
	for _, item := range evidence {
		inputs = append(inputs, scriptports.ResearchCandidateRankingInput{
			CandidateID: item.Source.ID, Label: item.Source.Title,
			Sources: []scriptpkg.ResearchWebSource{item.Source}, Claims: []scriptpkg.ResearchClaim{item.Claim},
		})
	}
	return inputs
}
