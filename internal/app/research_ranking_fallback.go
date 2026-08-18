// Package app — research_ranking_fallback.go.
//
// Deterministic ranking fallback that respects the requested metric. It
// never substitutes one financial metric for another: for a financial
// metric, only claims that state that specific quantity contribute, and
// candidates without comparable evidence are ranked last and flagged as
// uncertainty instead of being silently re-ranked by a different metric.
package app

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

const (
	rankStrategyFinancial   = "deterministic_verified_financial_evidence"
	rankStrategyAchieve     = "deterministic_verified_achievement_evidence"
	rankStrategyInfluence   = "deterministic_verified_influence_evidence"
	rankStrategyControversy = "deterministic_verified_controversy_evidence"
	rankStrategyGeneric     = "deterministic_verified_evidence"
	rankFallbackParse       = "MODEL_OUTPUT_INVALID"
	rankFallbackValidation  = "MODEL_OUTPUT_VALIDATION_FAILED"
)

// minRankingCoverage is the minimum number of candidates with comparable
// financial evidence before a fallback ranking is considered reliable.
const minRankingCoverage = 3

// financialAmountRe matches "$220 million", "$1.2 billion", "$105M", "$743m".
var financialAmountRe = regexp.MustCompile(`(?i)\$\s?([0-9]+(?:\.[0-9]+)?)\s*(trillion|billion|million|thousand|t|b|m|k)?\b`)

// unitWordAmountRe matches "220 million dollars", "1.2 billion USD". The
// amount itself is only trusted when the unit word is stated, so "3 million
// viewers" cannot poison a financial ranking.
var unitWordAmountRe = regexp.MustCompile(`(?i)\b([0-9]+(?:\.[0-9]+)?)\s*(trillion|billion|million)\s+(dollars|usd)\b`)

// metricPhrases are the claim-language markers that classify a claim as
// evidence for a specific financial metric. Order matters within a slice
// only for readability; every phrase is an OR match.
var metricPhrases = map[scriptpkg.RankingMetric][]string{
	scriptpkg.RankingMetricEstimatedNetWorth: {
		"net worth", "net wealth", "estimated net worth", "estimated worth",
		"worth of", "valued at", "is worth", "net value", "worth approximately",
		"current net worth",
	},
	scriptpkg.RankingMetricCareerEarnings: {
		"career earnings", "total earnings", "career income", "lifetime earnings",
		"earned in his career", "career pay", "earnings of",
	},
	scriptpkg.RankingMetricAnnualEarnings: {
		"per year", "a year", "annually", "annual earnings", "in a single year",
		"single year", "highest-paid", "highest paid", "annual income",
	},
	scriptpkg.RankingMetricFightPurse: {
		"purse", "payday", "fight purse", "guaranteed", "guarantee",
		"for the fight",
	},
}

// semanticKeywords are the claim-language markers that classify a claim as
// evidence for a specific semantic metric (no numeric value exists, so the
// deterministic fallback scores claim-keyword matches). Each semantic metric
// has its own signals: "most controversial" must never be scored by titles
// or championships, and "most influential" must never be scored by scandal.
var semanticKeywords = map[scriptpkg.RankingMetric]map[string]float64{
	scriptpkg.RankingMetricSportsAchievement: {
		"world champion": 5, "world title": 5, "olympic": 4, "championship": 3,
		"hall of fame": 3, "undefeated": 3, "knockout": 1, "title": 2, "won": 1,
	},
	scriptpkg.RankingMetricMostInfluential: {
		"influential": 5, "influence": 5, "icon": 4, "iconic": 4,
		"cultural impact": 5, "global icon": 5, "transcended": 4,
		"legacy": 3, "pioneered": 3, "activist": 3, "symbol": 3,
		"changed the sport": 4,
	},
	scriptpkg.RankingMetricMostControversial: {
		"controversial": 5, "controversy": 5, "scandal": 5, "banned": 4,
		"disqualified": 4, "infamous": 4, "outcry": 3, "polarizing": 4,
		"criticized": 3, "stripped": 3, "exile": 3, "pariah": 4,
	},
	// RankingMetricGeneric keeps the legacy sports-merit scoring so a
	// metric-agnostic ranking stays backward compatible.
	scriptpkg.RankingMetricGeneric: {
		"world champion": 5, "world title": 5, "olympic": 4, "championship": 3,
		"hall of fame": 3, "undefeated": 3, "knockout": 1, "title": 2, "won": 1,
	},
}

type metricRankedInput struct {
	input      scriptports.ResearchCandidateRankingInput
	score      float64
	hasMetric  bool
	noEvidence bool
	claimCount int
	quality    string
}

// metricAwareFallback ranks candidates deterministically for the requested
// metric. Financial metrics score candidates by the median extracted amount
// across claims that mention the metric; candidates without comparable
// evidence keep score 0 and rank last with an explicit rationale.
func metricAwareFallback(inputs []scriptports.ResearchCandidateRankingInput, metric scriptpkg.RankingMetric) ([]scriptports.ResearchCandidateRanking, scriptpkg.ResearchRankingInfo) {
	items := make([]metricRankedInput, 0, len(inputs))
	for _, input := range inputs {
		item := metricRankedInput{input: input}
		if metric.IsFinancial() {
			item.score, item.claimCount = financialMetricScore(input.Claims, metric)
			item.hasMetric = item.score > 0
		} else {
			item.score = semanticScore(input.Claims, metric)
			item.hasMetric = item.score > 0
			item.claimCount = semanticClaimCount(input.Claims, metric)
		}
		item.noEvidence = !item.hasMetric
		item.quality = metricEvidenceQuality(item.claimCount)
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].input.CandidateID < items[j].input.CandidateID
		}
		return items[i].score > items[j].score
	})

	strategy := semanticStrategy(metric)
	ranking := make([]scriptports.ResearchCandidateRanking, 0, len(items))
	withEvidence := 0
	for index, item := range items {
		if item.hasMetric {
			withEvidence++
		}
		ranking = append(ranking, scriptports.ResearchCandidateRanking{
			CandidateID:           item.input.CandidateID,
			Rank:                  index + 1,
			Score:                 item.score,
			Rationale:             rankRationale(metric, item),
			MetricEvidenceQuality: item.quality,
			MetricClaimCount:      item.claimCount,
		})
	}
	info := scriptpkg.ResearchRankingInfo{
		ResolvedMetric:         metric.String(),
		Strategy:               strategy,
		CandidatesWithEvidence: withEvidence,
		Uncertain:              metric.IsFinancial() && withEvidence < minRankingCoverage,
		Confidence:             buildRankingConfidence(items),
	}
	return ranking, info
}

// metricEvidenceQuality buckets a candidate's metric claim count into a coarse
// confidence level: 0 → NONE, 1 → LOW, 2 → MEDIUM, 3+ → HIGH.
func metricEvidenceQuality(claimCount int) string {
	switch {
	case claimCount >= 3:
		return scriptpkg.MetricEvidenceQualityHigh
	case claimCount == 2:
		return scriptpkg.MetricEvidenceQualityMedium
	case claimCount == 1:
		return scriptpkg.MetricEvidenceQualityLow
	default:
		return scriptpkg.MetricEvidenceQualityNone
	}
}

// buildRankingConfidence aggregates per-candidate evidence into a granular
// ranking confidence: comparable candidates (those with a value to sort on),
// coverage, and the specific candidates that could not be ranked with
// confidence (no comparable value). A single weak-but-comparable candidate
// (e.g. Canelo with LOW quality) still counts as comparable; its weakness is
// visible in the per-candidate metric_evidence_quality.
func buildRankingConfidence(items []metricRankedInput) *scriptpkg.ResearchRankingConfidence {
	confidence := &scriptpkg.ResearchRankingConfidence{
		TotalCandidates:         len(items),
		LowConfidenceCandidates: []string{},
	}
	for _, item := range items {
		if item.hasMetric {
			confidence.ComparableCandidates++
		} else {
			confidence.LowConfidenceCandidates = append(confidence.LowConfidenceCandidates, item.input.CandidateID)
		}
	}
	if confidence.TotalCandidates > 0 {
		confidence.Coverage = float64(confidence.ComparableCandidates) / float64(confidence.TotalCandidates)
	}
	return confidence
}

func rankRationale(metric scriptpkg.RankingMetric, item metricRankedInput) string {
	if !item.hasMetric {
		return "no comparable " + metric.String() + " evidence in claims; ranked last"
	}
	if metric.IsFinancial() {
		return "deterministic " + metric.String() + " evidence: median extracted amount $" + formatUSD(item.score)
	}
	return "deterministic " + metric.String() + " evidence score " + strconv.FormatFloat(item.score, 'f', 1, 64)
}

// financialMetricScore returns the median USD value extracted from claims
// that mention the requested metric, and the number of such claims. A
// non-zero score means the candidate has a comparable value; claimCount is the
// granular evidence signal used for metric_evidence_quality (a claim can
// mention the metric without yielding an extractable amount).
func financialMetricScore(claims []scriptpkg.ResearchClaim, metric scriptpkg.RankingMetric) (score float64, claimCount int) {
	phrases := metricPhrases[metric]
	var values []float64
	for _, claim := range claims {
		if !claim.Verified {
			continue
		}
		if !containsAny(strings.ToLower(claim.Text), phrases...) {
			continue
		}
		claimCount++
		values = append(values, extractFinancialValues(claim.Text)...)
	}
	if len(values) == 0 {
		return 0, claimCount
	}
	return median(values), claimCount
}

// semanticStrategy maps a metric to its deterministic fallback strategy id.
// Financial metrics are handled separately (metric.IsFinancial), so this only
// needs to cover the semantic metrics plus the generic fallback.
func semanticStrategy(metric scriptpkg.RankingMetric) string {
	switch metric {
	case scriptpkg.RankingMetricSportsAchievement:
		return rankStrategyAchieve
	case scriptpkg.RankingMetricMostInfluential:
		return rankStrategyInfluence
	case scriptpkg.RankingMetricMostControversial:
		return rankStrategyControversy
	case scriptpkg.RankingMetricEstimatedNetWorth, scriptpkg.RankingMetricCareerEarnings, scriptpkg.RankingMetricAnnualEarnings, scriptpkg.RankingMetricFightPurse:
		return rankStrategyFinancial
	default:
		return rankStrategyGeneric
	}
}

// semanticKeywordWeights returns the keyword→weight map for a semantic metric,
// falling back to the generic sports-merit map for unknown metrics.
func semanticKeywordWeights(metric scriptpkg.RankingMetric) map[string]float64 {
	if weights, ok := semanticKeywords[metric]; ok {
		return weights
	}
	return semanticKeywords[scriptpkg.RankingMetricGeneric]
}

// semanticClaimCount counts verified claims that mention any keyword for the
// given semantic metric. It mirrors semanticScore and feeds the per-candidate
// quality for semantic (sports achievement / influence / controversy / generic)
// rankings.
func semanticClaimCount(claims []scriptpkg.ResearchClaim, metric scriptpkg.RankingMetric) int {
	weights := semanticKeywordWeights(metric)
	count := 0
	for _, claim := range claims {
		if !claim.Verified {
			continue
		}
		text := strings.ToLower(claim.Text)
		for phrase := range weights {
			if strings.Contains(text, phrase) {
				count++
				break
			}
		}
	}
	return count
}

func semanticScore(claims []scriptpkg.ResearchClaim, metric scriptpkg.RankingMetric) float64 {
	weights := semanticKeywordWeights(metric)
	score := 0.0
	for _, claim := range claims {
		if !claim.Verified {
			continue
		}
		text := strings.ToLower(claim.Text)
		for phrase, weight := range weights {
			if strings.Contains(text, phrase) {
				score += weight
			}
		}
	}
	return score
}

// extractFinancialValues parses normalized USD values from a claim text.
// Values below $10K or above $1T are treated as noise and dropped.
func extractFinancialValues(text string) []float64 {
	seen := make(map[float64]struct{})
	var out []float64
	for _, match := range financialAmountRe.FindAllStringSubmatch(text, -1) {
		if value, ok := parseAmountValue(match[1], match[2]); ok {
			if _, dup := seen[value]; !dup {
				seen[value] = struct{}{}
				out = append(out, value)
			}
		}
	}
	for _, match := range unitWordAmountRe.FindAllStringSubmatch(text, -1) {
		if value, ok := parseAmountValue(match[1], match[2]); ok {
			if _, dup := seen[value]; !dup {
				seen[value] = struct{}{}
				out = append(out, value)
			}
		}
	}
	return out
}

func parseAmountValue(number, unit string) (float64, bool) {
	number = strings.ReplaceAll(number, ",", "")
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "trillion", "t":
		value *= 1e12
	case "billion", "b":
		value *= 1e9
	case "million", "m":
		value *= 1e6
	case "thousand", "k":
		value *= 1e3
	}
	if value < 1e4 || value > 1e12 {
		return 0, false
	}
	return value, true
}

func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func containsAny(value string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(value, sub) {
			return true
		}
	}
	return false
}

func formatUSD(value float64) string {
	rounded := int64(value)
	var out []byte
	digits := strconv.FormatInt(rounded, 10)
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}
