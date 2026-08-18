package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeRankClient struct {
	responses []string
	calls     int
}

func (f *fakeRankClient) SimpleGenerate(_ context.Context, _ string, _ string, _ time.Duration, _ map[string]any) (string, error) {
	if f.calls >= len(f.responses) {
		return "", fmt.Errorf("no scripted response")
	}
	out := f.responses[f.calls]
	f.calls++
	return out, nil
}

func rankerInputs() []scriptports.ResearchCandidateRankingInput {
	return []scriptports.ResearchCandidateRankingInput{
		{CandidateID: "George Foreman", Label: "George Foreman", Claims: []scriptpkg.ResearchClaim{{Text: "George Foreman estimated net worth of $300 million built through the grill business.", Verified: true}}},
		{CandidateID: "Manny Pacquiao", Label: "Manny Pacquiao", Claims: []scriptpkg.ResearchClaim{{Text: "Manny Pacquiao has an estimated net worth of $220 million as of 2026.", Verified: true}}},
		{CandidateID: "Sugar Ray Leonard", Label: "Sugar Ray Leonard", Claims: []scriptpkg.ResearchClaim{{Text: "Sugar Ray Leonard won world titles in five weight classes.", Verified: true}}},
	}
}

func validRankerResponse() string {
	return `{"ranking_metric":"estimated_net_worth","items":[
		{"candidate_id":"George Foreman","rank":1,"score":300000000,"evidence_claim_ids":["c1"],"rationale":"highest net worth"},
		{"candidate_id":"Manny Pacquiao","rank":2,"score":220000000,"evidence_claim_ids":["c1"],"rationale":"second"},
		{"candidate_id":"Sugar Ray Leonard","rank":3,"score":0,"evidence_claim_ids":[],"rationale":"no net worth evidence"}]}`
}

func validSportsRankerResponse() string {
	return `{"ranking_metric":"sports_achievement","items":[
		{"candidate_id":"Sugar Ray Leonard","rank":1,"score":0,"evidence_claim_ids":["c1"],"rationale":"five weight class world champion"},
		{"candidate_id":"George Foreman","rank":2,"score":0,"evidence_claim_ids":["c1"],"rationale":"heavyweight champion"},
		{"candidate_id":"Manny Pacquiao","rank":3,"score":0,"evidence_claim_ids":["c1"],"rationale":"eight division champion"}]}`
}

func newRanker(client *fakeRankClient) *ollamaResearchRanker {
	return &ollamaResearchRanker{client: client, model: "test-model", logger: zap.NewNop()}
}

func TestParseResearchRankingOutput_FencedJSON(t *testing.T) {
	output := "```json\n" + validRankerResponse() + "\n```"
	ranking, metric, err := parseResearchRankingOutput(output)
	require.NoError(t, err)
	require.Len(t, ranking, 3)
	require.Equal(t, "George Foreman", ranking[0].CandidateID)
	require.Equal(t, 1, ranking[0].Rank)
	require.Equal(t, "estimated_net_worth", metric)
}

func TestParseResearchRankingOutput_ProseAroundJSON(t *testing.T) {
	output := "Here is the ranking:\n" + validRankerResponse() + "\nThat's all."
	ranking, _, err := parseResearchRankingOutput(output)
	require.NoError(t, err)
	require.Len(t, ranking, 3)
}

func TestParseResearchRankingOutput_RejectsLegacyArrayAndProse(t *testing.T) {
	_, _, err := parseResearchRankingOutput(`[{"candidate_id":"x","rank":1}]`)
	require.Error(t, err, "legacy bare array must fail parsing")
	_, _, err = parseResearchRankingOutput("no json here")
	require.Error(t, err)
}

func TestRank_NonFinancialUsesModelRanking(t *testing.T) {
	client := &fakeRankClient{responses: []string{validSportsRankerResponse()}}
	ranker := newRanker(client)
	result, err := ranker.Rank(context.Background(), "greatest boxers", scriptpkg.RankingMetricSportsAchievement, rankerInputs())
	require.NoError(t, err)
	require.Len(t, result.Ranking, 3)
	require.False(t, result.Info.FallbackUsed)
	require.Equal(t, "llm_verified_evidence", result.Info.Strategy)
	require.Equal(t, "sports_achievement", result.Info.ResolvedMetric)
	require.Equal(t, 1, client.calls, "no retry on valid output")
}

func TestRank_RetrySucceedsAfterParseFailure(t *testing.T) {
	client := &fakeRankClient{responses: []string{"not json at all", validSportsRankerResponse()}}
	ranker := newRanker(client)
	result, err := ranker.Rank(context.Background(), "greatest boxers", scriptpkg.RankingMetricSportsAchievement, rankerInputs())
	require.NoError(t, err)
	require.False(t, result.Info.FallbackUsed, "retry output was valid")
	require.Len(t, result.Ranking, 3)
	require.Equal(t, 2, client.calls)
}

func TestRank_MetricMismatchFallsBackWithReason(t *testing.T) {
	wrongMetric := strings.Replace(validSportsRankerResponse(), `"ranking_metric":"sports_achievement"`, `"ranking_metric":"estimated_net_worth"`, 1)
	client := &fakeRankClient{responses: []string{wrongMetric}}
	ranker := newRanker(client)
	result, err := ranker.Rank(context.Background(), "greatest boxers", scriptpkg.RankingMetricSportsAchievement, rankerInputs())
	require.NoError(t, err)
	require.True(t, result.Info.FallbackUsed)
	require.Equal(t, rankFallbackValidation, result.Info.FallbackReason)
	require.Equal(t, rankStrategyAchieve, result.Info.Strategy)
	require.Equal(t, "sports_achievement", result.Info.ResolvedMetric)
	require.Equal(t, 1, client.calls, "validation failure falls back immediately without retry")
}

func TestRank_DuplicateRankFallsBack(t *testing.T) {
	// The model returns valid JSON but assigns the same rank to two candidates.
	// The strict contract rejects it and falls back deterministically.
	dupRank := `{"ranking_metric":"sports_achievement","items":[
		{"candidate_id":"Sugar Ray Leonard","rank":1,"score":0,"evidence_claim_ids":["c1"],"rationale":"five weight classes"},
		{"candidate_id":"George Foreman","rank":1,"score":0,"evidence_claim_ids":["c1"],"rationale":"duplicate rank"},
		{"candidate_id":"Manny Pacquiao","rank":3,"score":0,"evidence_claim_ids":["c1"],"rationale":"third"}]}`
	client := &fakeRankClient{responses: []string{dupRank}}
	ranker := newRanker(client)
	result, err := ranker.Rank(context.Background(), "greatest boxers", scriptpkg.RankingMetricSportsAchievement, rankerInputs())
	require.NoError(t, err)
	require.True(t, result.Info.FallbackUsed)
	require.Equal(t, rankFallbackValidation, result.Info.FallbackReason)
	require.Equal(t, rankStrategyAchieve, result.Info.Strategy)
}

func TestRank_InvalidJSONTwiceFallsBackWithParseReason(t *testing.T) {
	client := &fakeRankClient{responses: []string{"garbage", "more garbage"}}
	ranker := newRanker(client)
	result, err := ranker.Rank(context.Background(), "greatest boxers", scriptpkg.RankingMetricSportsAchievement, rankerInputs())
	require.NoError(t, err)
	require.True(t, result.Info.FallbackUsed)
	require.Equal(t, rankFallbackParse, result.Info.FallbackReason)
	require.Equal(t, 2, client.calls)
}

func TestRank_FinancialMetricUsesDeterministicOrdering(t *testing.T) {
	// Financial metrics never ask the LLM to order: the deterministic numeric
	// sort is primary. The single LLM call below is only the rationale
	// enricher, and its failure must not affect the ranking.
	client := &fakeRankClient{responses: []string{"garbage rationale"}}
	ranker := newRanker(client)
	result, err := ranker.Rank(context.Background(), "richest boxers", scriptpkg.RankingMetricEstimatedNetWorth, rankerInputs())
	require.NoError(t, err)
	require.False(t, result.Info.FallbackUsed, "deterministic sort is primary, not a fallback")
	require.Equal(t, rankStrategyFinancial, result.Info.Strategy)
	require.Equal(t, "estimated_net_worth", result.Info.ResolvedMetric)
	require.Equal(t, "estimated_net_worth", result.Info.RequestedMetric)
	// Foreman $300M > Pacquiao $220M > Leonard (no net-worth evidence).
	require.Equal(t, "George Foreman", result.Ranking[0].CandidateID)
	require.Equal(t, 300e6, result.Ranking[0].Score)
	require.Equal(t, "Manny Pacquiao", result.Ranking[1].CandidateID)
	require.Equal(t, 220e6, result.Ranking[1].Score)
	require.Equal(t, "Sugar Ray Leonard", result.Ranking[2].CandidateID)
	require.Equal(t, 0.0, result.Ranking[2].Score)
	require.Equal(t, 1, client.calls, "one rationale-enrichment attempt")
	require.Contains(t, result.Ranking[2].Rationale, "no comparable estimated_net_worth evidence")
}

func TestRank_FinancialMetricEnrichesRationaleOrderSafe(t *testing.T) {
	// The rationale response deliberately lists candidates in a different
	// order than the ranking. The merge must only replace text, never the
	// deterministic order.
	client := &fakeRankClient{responses: []string{`{"rationales":[
		{"candidate_id":"Sugar Ray Leonard","rationale":"No net-worth figure available."},
		{"candidate_id":"Manny Pacquiao","rationale":"$220M as of 2026."},
		{"candidate_id":"George Foreman","rationale":"Grill business drove his fortune."}]}`}}
	ranker := newRanker(client)
	result, err := ranker.Rank(context.Background(), "richest boxers", scriptpkg.RankingMetricEstimatedNetWorth, rankerInputs())
	require.NoError(t, err)
	require.Equal(t, rankStrategyFinancial, result.Info.Strategy)
	// Order is untouched: Foreman > Pacquiao > Leonard.
	require.Equal(t, "George Foreman", result.Ranking[0].CandidateID)
	require.Equal(t, 300e6, result.Ranking[0].Score)
	require.Equal(t, "Manny Pacquiao", result.Ranking[1].CandidateID)
	require.Equal(t, "Sugar Ray Leonard", result.Ranking[2].CandidateID)
	// Rationales are rewritten, matched by candidate_id.
	require.Equal(t, "Grill business drove his fortune.", result.Ranking[0].Rationale)
	require.Equal(t, "$220M as of 2026.", result.Ranking[1].Rationale)
	require.Equal(t, "No net-worth figure available.", result.Ranking[2].Rationale)
	require.Equal(t, 1, client.calls)
}

func TestValidateResearchRankingOutput(t *testing.T) {
	inputs := rankerInputs()
	valid := []scriptports.ResearchCandidateRanking{
		{CandidateID: "George Foreman", Rank: 1, Score: 300e6},
		{CandidateID: "Manny Pacquiao", Rank: 2, Score: 220e6},
		{CandidateID: "Sugar Ray Leonard", Rank: 3},
	}
	require.NoError(t, validateResearchRankingOutput(scriptpkg.RankingMetricEstimatedNetWorth, "estimated_net_worth", inputs, valid))

	// A financial ranking where most scores are zero must fail validation.
	mostlyUnscored := []scriptports.ResearchCandidateRanking{
		{CandidateID: "George Foreman", Rank: 1, Score: 300e6},
		{CandidateID: "Manny Pacquiao", Rank: 2},
		{CandidateID: "Sugar Ray Leonard", Rank: 3},
	}
	require.ErrorContains(t, validateResearchRankingOutput(scriptpkg.RankingMetricEstimatedNetWorth, "estimated_net_worth", inputs, mostlyUnscored), "numeric scores for only 1/3")

	wrongMetric := append([]scriptports.ResearchCandidateRanking(nil), valid...)
	require.ErrorContains(t, validateResearchRankingOutput(scriptpkg.RankingMetricEstimatedNetWorth, "sports_achievement", inputs, wrongMetric), "does not match requested metric")

	duplicate := append([]scriptports.ResearchCandidateRanking(nil), valid...)
	duplicate[2] = scriptports.ResearchCandidateRanking{CandidateID: "George Foreman", Rank: 3}
	require.ErrorContains(t, validateResearchRankingOutput(scriptpkg.RankingMetricEstimatedNetWorth, "estimated_net_worth", inputs, duplicate), "duplicated candidate")

	dupRank := append([]scriptports.ResearchCandidateRanking(nil), valid...)
	dupRank[2] = scriptports.ResearchCandidateRanking{CandidateID: "Sugar Ray Leonard", Rank: 1}
	require.ErrorContains(t, validateResearchRankingOutput(scriptpkg.RankingMetricEstimatedNetWorth, "estimated_net_worth", inputs, dupRank), "duplicate rank")

	unknown := append([]scriptports.ResearchCandidateRanking(nil), valid...)
	unknown[2] = scriptports.ResearchCandidateRanking{CandidateID: "Rocky Marciano", Rank: 3}
	require.ErrorContains(t, validateResearchRankingOutput(scriptpkg.RankingMetricEstimatedNetWorth, "estimated_net_worth", inputs, unknown), "unknown candidate")

	short := valid[:2]
	require.ErrorContains(t, validateResearchRankingOutput(scriptpkg.RankingMetricEstimatedNetWorth, "estimated_net_worth", inputs, short), "expected 3")
}
