package script

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeRankingMetricSemanticAliases(t *testing.T) {
	cases := []struct {
		raw  string
		want RankingMetric
	}{
		{"most_influential", RankingMetricMostInfluential},
		{"most influential", RankingMetricMostInfluential},
		{"influential", RankingMetricMostInfluential},
		{"influence", RankingMetricMostInfluential},
		{"cultural-impact", RankingMetricMostInfluential},
		{"most_controversial", RankingMetricMostControversial},
		{"most controversial", RankingMetricMostControversial},
		{"controversial", RankingMetricMostControversial},
		{"controversy", RankingMetricMostControversial},
		{"polarizing", RankingMetricMostControversial},
		{"scandalous", RankingMetricMostControversial},
		{"sports_achievement", RankingMetricSportsAchievement},
		{"greatest", RankingMetricSportsAchievement},
		{"estimated_net_worth", RankingMetricEstimatedNetWorth},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, NormalizeRankingMetric(tc.raw), "raw %q", tc.raw)
	}
}

func TestInferRankingMetricFromTopicSemantic(t *testing.T) {
	cases := []struct {
		topic string
		want  RankingMetric
	}{
		{"The most influential boxers of all time", RankingMetricMostInfluential},
		{"boxers ranked by cultural impact", RankingMetricMostInfluential},
		{"the most controversial athletes in history", RankingMetricMostControversial},
		{"polarizing boxers ranked", RankingMetricMostControversial},
		{"the greatest boxers of all time", RankingMetricSportsAchievement},
		{"the richest boxers by net worth", RankingMetricEstimatedNetWorth},
		{"boxers ranked", RankingMetricGeneric},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, InferRankingMetricFromTopic(tc.topic), "topic %q", tc.topic)
	}
}

func TestSemanticMetricsAreNotFinancial(t *testing.T) {
	require.False(t, RankingMetricMostInfluential.IsFinancial())
	require.False(t, RankingMetricMostControversial.IsFinancial())
	require.False(t, RankingMetricSportsAchievement.IsFinancial())
	require.False(t, RankingMetricGeneric.IsFinancial())
	require.True(t, RankingMetricEstimatedNetWorth.IsFinancial())
	require.True(t, RankingMetricCareerEarnings.IsFinancial())
}

func TestSemanticMetricDescriptionsNonEmpty(t *testing.T) {
	for _, metric := range []RankingMetric{
		RankingMetricSportsAchievement,
		RankingMetricMostInfluential,
		RankingMetricMostControversial,
		RankingMetricGeneric,
	} {
		require.NotEmpty(t, metric.Description(), "metric %s", metric)
	}
}
