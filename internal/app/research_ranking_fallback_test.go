package app

import (
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/stretchr/testify/require"
)

func TestExtractFinancialValues(t *testing.T) {
	cases := []struct {
		text string
		want []float64
	}{
		{"Pacquiao has an estimated net worth of $220 million as of 2026.", []float64{220e6}},
		{"Foreman is worth $1.2 billion after the grill deal.", []float64{1.2e9}},
		{"Mayweather earned $105M in a single year.", []float64{105e6}},
		{"Canelo career earnings exceed $743 million.", []float64{743e6}},
		{"worth 220 million dollars", []float64{220e6}},
		{"viewed by 3 million fans", nil}, // no $ and no dollars word
		{"paid $50 for a meal", nil},      // below $10K floor
		{"$2 trillion market", nil},       // above $1T cap
		{"$900 trillion nonsense", nil},   // above $1T cap
	}
	for _, tc := range cases {
		got := extractFinancialValues(tc.text)
		require.ElementsMatch(t, tc.want, got, "text %q", tc.text)
	}
}

func TestFinancialMetricScoreRequiresMetricPhrase(t *testing.T) {
	claims := []scriptpkg.ResearchClaim{
		{Text: "Canelo has career earnings of more than $743 million.", Verified: true},
		{Text: "Canelo's estimated net worth is $250 million.", Verified: true},
	}
	netWorth, netWorthClaims := financialMetricScore(claims, scriptpkg.RankingMetricEstimatedNetWorth)
	require.Equal(t, 250e6, netWorth, "career earnings must not count as net worth")
	require.Equal(t, 1, netWorthClaims, "only the net-worth claim matches the metric")

	career, careerClaims := financialMetricScore(claims, scriptpkg.RankingMetricCareerEarnings)
	require.Equal(t, 743e6, career)
	require.Equal(t, 1, careerClaims, "only the career-earnings claim matches the metric")
}

func TestMetricAwareFallbackNetWorthOrdersDescending(t *testing.T) {
	inputs := []scriptports.ResearchCandidateRankingInput{
		{CandidateID: "Sugar Ray Leonard", Label: "Sugar Ray Leonard", Claims: []scriptpkg.ResearchClaim{{Text: "Sugar Ray Leonard won world titles in five weight classes.", Verified: true}}},
		{CandidateID: "Manny Pacquiao", Label: "Manny Pacquiao", Claims: []scriptpkg.ResearchClaim{{Text: "Manny Pacquiao has an estimated net worth of $220 million as of 2026.", Verified: true}}},
		{CandidateID: "George Foreman", Label: "George Foreman", Claims: []scriptpkg.ResearchClaim{
			{Text: "George Foreman net worth estimated at $250 million.", Verified: true},
			{Text: "George Foreman net worth reported around $300 million after the grill business.", Verified: true},
		}},
	}
	ranking, info := metricAwareFallback(inputs, scriptpkg.RankingMetricEstimatedNetWorth)
	require.Len(t, ranking, 3)
	require.Equal(t, "George Foreman", ranking[0].CandidateID)
	require.Equal(t, "Manny Pacquiao", ranking[1].CandidateID)
	require.Equal(t, "Sugar Ray Leonard", ranking[2].CandidateID)
	require.Equal(t, 275e6, ranking[0].Score, "median of 250M/300M")
	require.Equal(t, 220e6, ranking[1].Score)
	require.Equal(t, 0.0, ranking[2].Score)
	require.Equal(t, 2, info.CandidatesWithEvidence)
	require.True(t, info.Uncertain, "fewer than minRankingCoverage candidates with evidence")
	require.Equal(t, rankStrategyFinancial, info.Strategy)
	require.Contains(t, ranking[2].Rationale, "no comparable estimated_net_worth evidence")
}

func TestMetricAwareFallbackSportsAchievementKeepsLegacyScoring(t *testing.T) {
	inputs := []scriptports.ResearchCandidateRankingInput{
		{CandidateID: "Ali", Label: "Ali", Claims: []scriptpkg.ResearchClaim{{Text: "world champion and olympic gold", Verified: true}}},
		{CandidateID: "Tyson", Label: "Tyson", Claims: []scriptpkg.ResearchClaim{{Text: "knockout artist", Verified: true}}},
	}
	ranking, info := metricAwareFallback(inputs, scriptpkg.RankingMetricSportsAchievement)
	require.Equal(t, "Ali", ranking[0].CandidateID)
	require.Equal(t, "Tyson", ranking[1].CandidateID)
	require.Equal(t, rankStrategyAchieve, info.Strategy)
	require.False(t, info.Uncertain, "sports achievement never marks financial uncertainty")
}

func TestMetricAwareFallbackGranularConfidence(t *testing.T) {
	inputs := []scriptports.ResearchCandidateRankingInput{
		{CandidateID: "Mayweather", Label: "Mayweather", Claims: []scriptpkg.ResearchClaim{
			{Text: "Mayweather net worth estimated at $250 million.", Verified: true},
			{Text: "Mayweather net worth reported at $270 million.", Verified: true},
			{Text: "Mayweather net worth now $260 million.", Verified: true},
		}},
		{CandidateID: "Pacquiao", Label: "Pacquiao", Claims: []scriptpkg.ResearchClaim{
			{Text: "Pacquiao net worth $220 million.", Verified: true},
			{Text: "Pacquiao is worth $200 million.", Verified: true},
		}},
		{CandidateID: "Canelo", Label: "Canelo", Claims: []scriptpkg.ResearchClaim{
			{Text: "Canelo estimated net worth is $150 million.", Verified: true},
		}},
		{CandidateID: "Leonard", Label: "Leonard", Claims: []scriptpkg.ResearchClaim{
			{Text: "Leonard won world titles in five weight classes.", Verified: true},
		}},
	}
	ranking, info := metricAwareFallback(inputs, scriptpkg.RankingMetricEstimatedNetWorth)

	// Order is numeric descending; Leonard (no net-worth value) ranks last.
	require.Equal(t, []string{"Mayweather", "Pacquiao", "Canelo", "Leonard"}, []string{ranking[0].CandidateID, ranking[1].CandidateID, ranking[2].CandidateID, ranking[3].CandidateID})
	require.Equal(t, 260e6, ranking[0].Score)
	require.Equal(t, 210e6, ranking[1].Score)
	require.Equal(t, 150e6, ranking[2].Score)
	require.Equal(t, 0.0, ranking[3].Score)

	// A single weak candidate (Canelo: LOW, 1 claim) must NOT mark the whole
	// ranking uncertain: three candidates have comparable evidence.
	require.False(t, info.Uncertain)
	require.Equal(t, 3, info.CandidatesWithEvidence)

	// Per-candidate quality and claim count are granular.
	require.Equal(t, scriptpkg.MetricEvidenceQualityHigh, ranking[0].MetricEvidenceQuality)
	require.Equal(t, 3, ranking[0].MetricClaimCount)
	require.Equal(t, scriptpkg.MetricEvidenceQualityMedium, ranking[1].MetricEvidenceQuality)
	require.Equal(t, 2, ranking[1].MetricClaimCount)
	require.Equal(t, scriptpkg.MetricEvidenceQualityLow, ranking[2].MetricEvidenceQuality)
	require.Equal(t, 1, ranking[2].MetricClaimCount)
	require.Equal(t, scriptpkg.MetricEvidenceQualityNone, ranking[3].MetricEvidenceQuality)
	require.Equal(t, 0, ranking[3].MetricClaimCount)

	// The aggregate confidence records comparable candidates, coverage, and
	// only the candidate with NO comparable value as low-confidence (Canelo
	// still has a value, so its weakness stays visible per-candidate).
	require.NotNil(t, info.Confidence)
	require.Equal(t, 3, info.Confidence.ComparableCandidates)
	require.Equal(t, 4, info.Confidence.TotalCandidates)
	require.InDelta(t, 0.75, info.Confidence.Coverage, 1e-9)
	require.Equal(t, []string{"Leonard"}, info.Confidence.LowConfidenceCandidates)
}

func TestBuildRankingConfidenceEmptyIsZeroCoverage(t *testing.T) {
	confidence := buildRankingConfidence(nil)
	require.NotNil(t, confidence)
	require.Equal(t, 0, confidence.TotalCandidates)
	require.Equal(t, 0, confidence.ComparableCandidates)
	require.Equal(t, 0.0, confidence.Coverage)
	require.Empty(t, confidence.LowConfidenceCandidates)
}

func TestMetricAwareFallbackMostInfluentialUsesInfluenceEvidence(t *testing.T) {
	inputs := []scriptports.ResearchCandidateRankingInput{
		{CandidateID: "Champion", Label: "Champion", Claims: []scriptpkg.ResearchClaim{{Text: "won the world title and entered the hall of fame.", Verified: true}}},
		{CandidateID: "Icon", Label: "Icon", Claims: []scriptpkg.ResearchClaim{{Text: "became a global icon whose cultural impact transcended the sport.", Verified: true}}},
	}
	ranking, info := metricAwareFallback(inputs, scriptpkg.RankingMetricMostInfluential)
	// Influence keywords (icon, cultural impact, transcended) must beat titles
	// and hall-of-fame: the metric must never be scored by sports merit.
	require.Equal(t, "Icon", ranking[0].CandidateID)
	require.Equal(t, "Champion", ranking[1].CandidateID)
	require.Equal(t, rankStrategyInfluence, info.Strategy)
	require.Equal(t, "most_influential", info.ResolvedMetric)
}

func TestMetricAwareFallbackMostControversialUsesControversyEvidence(t *testing.T) {
	inputs := []scriptports.ResearchCandidateRankingInput{
		{CandidateID: "Champion", Label: "Champion", Claims: []scriptpkg.ResearchClaim{{Text: "won the world title and entered the hall of fame.", Verified: true}}},
		{CandidateID: "Scandal", Label: "Scandal", Claims: []scriptpkg.ResearchClaim{{Text: "banned for a doping scandal and became a polarizing figure.", Verified: true}}},
	}
	ranking, info := metricAwareFallback(inputs, scriptpkg.RankingMetricMostControversial)
	// Controversy keywords (banned, scandal, polarizing) must beat titles:
	// controversy evidence is the only signal for this metric.
	require.Equal(t, "Scandal", ranking[0].CandidateID)
	require.Equal(t, "Champion", ranking[1].CandidateID)
	require.Equal(t, rankStrategyControversy, info.Strategy)
	require.Equal(t, "most_controversial", info.ResolvedMetric)
}

func TestMetricAwareFallbackGenericUsesAchievementEvidence(t *testing.T) {
	inputs := []scriptports.ResearchCandidateRankingInput{
		{CandidateID: "A", Label: "A", Claims: []scriptpkg.ResearchClaim{{Text: "won the world title", Verified: true}}},
		{CandidateID: "B", Label: "B", Claims: []scriptpkg.ResearchClaim{{Text: "career earnings of $50 million", Verified: true}}},
	}
	ranking, info := metricAwareFallback(inputs, scriptpkg.RankingMetricGeneric)
	require.Equal(t, "A", ranking[0].CandidateID, "generic must not rank by financial evidence")
	require.Equal(t, rankStrategyGeneric, info.Strategy)
}
