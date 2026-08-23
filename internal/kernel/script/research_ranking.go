package script

import "strings"

// RankingMetric is the editorial criterion used to order researched
// candidates. The metric is explicit because a "top N" script changes
// meaning when the ordering criterion silently changes (e.g. ranking
// "richest boxers" by sporting merit instead of wealth). The ranker
// prompt, its output contract and the deterministic fallback all resolve
// against the same metric.
type RankingMetric string

const (
	RankingMetricEstimatedNetWorth RankingMetric = "estimated_net_worth"
	RankingMetricCareerEarnings    RankingMetric = "career_earnings"
	RankingMetricAnnualEarnings    RankingMetric = "annual_earnings"
	RankingMetricFightPurse        RankingMetric = "fight_purse"
	RankingMetricSportsAchievement RankingMetric = "sports_achievement"
	// RankingMetricMostInfluential orders by cultural/societal influence and
	// impact beyond the sport. It is a semantic metric: there is no single
	// numeric value to sort on, so the LLM ranker is primary.
	RankingMetricMostInfluential RankingMetric = "most_influential"
	// RankingMetricMostControversial orders by controversy, scandal and
	// polarizing reputation. Like most_influential it is semantic with no
	// deterministic numeric ordering.
	RankingMetricMostControversial RankingMetric = "most_controversial"
	// RankingMetricGeneric is the fallback when no metric is requested or
	// inferable. It deliberately is NOT a financial metric: callers that
	// need a financial ordering must request one explicitly.
	RankingMetricGeneric RankingMetric = "generic"
)

// NormalizeRankingMetric canonicalizes user-supplied metric strings into
// the supported RankingMetric set. Unknown or empty values resolve to
// RankingMetricGeneric so the pipeline always has a defined criterion.
func NormalizeRankingMetric(raw string) RankingMetric {
	v := strings.ToLower(strings.TrimSpace(raw))
	v = strings.ReplaceAll(v, "-", "_")
	v = strings.ReplaceAll(v, " ", "_")
	v = strings.ReplaceAll(v, ".", "_")
	switch v {
	case "estimated_net_worth", "net_worth", "networth", "estimated_net_worth_usd", "net_worth_usd", "net_wealth", "wealth", "richest", "richer", "wealthiest":
		return RankingMetricEstimatedNetWorth
	case "career_earnings", "career_earning", "earnings", "total_earnings", "career_income", "lifetime_earnings", "total_career_earnings":
		return RankingMetricCareerEarnings
	case "annual_earnings", "annual_income", "highest_paid", "highest_paid_athlete", "single_year_earnings", "yearly_earnings", "per_year", "annual_pay":
		return RankingMetricAnnualEarnings
	case "fight_purse", "purse", "fight_purses", "payday", "guarantees", "fight_pay":
		return RankingMetricFightPurse
	case "sports_achievement", "achievement", "achievements", "greatest", "accomplishment", "accomplishments", "legacy", "sports_merit", "greatest_of_all_time":
		return RankingMetricSportsAchievement
	case "most_influential", "influential", "influence", "most_influence", "cultural_impact", "iconic", "most_iconic":
		return RankingMetricMostInfluential
	case "most_controversial", "controversial", "controversy", "polarizing", "most_scandalous", "scandalous":
		return RankingMetricMostControversial
	default:
		return RankingMetricGeneric
	}
}

// InferRankingMetricFromTopic derives a best-effort metric from topic
// wording. It is used only when the request does not declare a metric
// explicitly; explicit requests always win.
func InferRankingMetricFromTopic(topic string) RankingMetric {
	t := strings.ToLower(topic)
	switch {
	case containsAny(t, "net worth", "net wealth", "richest", "wealthiest", "wealth", "richer", "worth"):
		return RankingMetricEstimatedNetWorth
	case containsAny(t, "career earnings", "total earnings", "career income", "lifetime earnings", "most money"):
		return RankingMetricCareerEarnings
	case containsAny(t, "highest-paid", "highest paid", "annual earnings", "per year", "single year", "best paid"):
		return RankingMetricAnnualEarnings
	case containsAny(t, "purse", "payday", "fight purse"):
		return RankingMetricFightPurse
	case containsAny(t, "greatest", "best", "achievement", "accomplished", "legacy", "goat", "champion"):
		return RankingMetricSportsAchievement
	case containsAny(t, "most influential", "influential", "influence", "cultural impact", "icon", "most iconic"):
		return RankingMetricMostInfluential
	case containsAny(t, "most controversial", "controversial", "controversy", "polarizing", "scandal", "infamous"):
		return RankingMetricMostControversial
	default:
		return RankingMetricGeneric
	}
}

// String returns the canonical metric identifier.
func (m RankingMetric) String() string { return string(m) }

// IsFinancial reports whether the metric orders candidates by monetary
// evidence. The deterministic fallback must never substitute a financial
// metric for another: each financial metric only consumes claims that
// state that specific quantity.
func (m RankingMetric) IsFinancial() bool {
	switch m {
	case RankingMetricEstimatedNetWorth, RankingMetricCareerEarnings, RankingMetricAnnualEarnings, RankingMetricFightPurse:
		return true
	default:
		return false
	}
}

// Description is the human-readable definition injected into the ranker
// prompt so the model and the deterministic fallback agree on meaning.
func (m RankingMetric) Description() string {
	switch m {
	case RankingMetricEstimatedNetWorth:
		return "estimated net worth (the individual's total current wealth, not career earnings)"
	case RankingMetricCareerEarnings:
		return "documented career earnings (the cumulative money the individual earned over their career)"
	case RankingMetricAnnualEarnings:
		return "documented annual or single-year earnings (the most money earned in a single year)"
	case RankingMetricFightPurse:
		return "documented fight purses and paydays (money received per fight)"
	case RankingMetricSportsAchievement:
		return "sporting achievements, titles and legacy"
	case RankingMetricMostInfluential:
		return "cultural and societal influence and impact beyond the sport"
	case RankingMetricMostControversial:
		return "controversy, scandal and polarizing reputation"
	default:
		return "documented financial prominence and business success"
	}
}

// Metric evidence quality levels for per-candidate ranking confidence. A
// candidate with a single metric claim is LOW confidence; three or more
// independent metric claims is HIGH. These are coarse buckets that feed the
// per-candidate metric_evidence_quality and the aggregate ranking_confidence.
const (
	MetricEvidenceQualityHigh   = "HIGH"
	MetricEvidenceQualityMedium = "MEDIUM"
	MetricEvidenceQualityLow    = "LOW"
	MetricEvidenceQualityNone   = "NONE"
)

// ResearchRankingConfidence granularizes ranking reliability so a single
// weak candidate does not mark the whole ranking uncertain. It records how
// many candidates had comparable evidence, the coverage ratio, and the
// specific candidates whose evidence was too weak to rank with confidence.
type ResearchRankingConfidence struct {
	ComparableCandidates    int      `json:"comparable_candidates"`
	TotalCandidates         int      `json:"total_candidates"`
	Coverage                float64  `json:"coverage"`
	LowConfidenceCandidates []string `json:"low_confidence_candidates"`
}

// ResearchRankingInfo makes ranking degradation observable. A fallback is
// never invisible: it records which metric was requested, which strategy
// produced the order, and whether the ranking is uncertain because too few
// candidates had comparable evidence.
type ResearchRankingInfo struct {
	RequestedMetric string `json:"requested_metric"`
	ResolvedMetric  string `json:"resolved_metric"`
	// Strategy is llm_verified_evidence, deterministic_verified_financial_evidence,
	// deterministic_verified_achievement_evidence or deterministic_verified_evidence.
	Strategy string `json:"strategy"`
	// FallbackUsed is true when the model output was unusable and the
	// deterministic resolver produced the order.
	FallbackUsed bool `json:"fallback_used"`
	// FallbackReason is MODEL_OUTPUT_INVALID or MODEL_OUTPUT_VALIDATION_FAILED.
	FallbackReason string `json:"fallback_reason,omitempty"`
	// CandidatesWithEvidence is the number of candidates whose claims
	// yielded comparable evidence for the requested metric.
	CandidatesWithEvidence int `json:"candidates_with_evidence"`
	// Uncertain marks a fallback ranking where several candidates lacked
	// comparable evidence for the requested metric.
	Uncertain bool `json:"uncertain,omitempty"`
	// Confidence is the granular per-ranking reliability breakdown. It is
	// populated for deterministic rankings (financial and non-financial
	// fallbacks); nil for pure LLM semantic rankings.
	Confidence *ResearchRankingConfidence `json:"ranking_confidence,omitempty"`
}

func containsAny(value string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(value, sub) {
			return true
		}
	}
	return false
}
