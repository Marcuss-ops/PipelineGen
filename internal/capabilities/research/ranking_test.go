package research

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveRankingReplacesUnsupportedCandidate(t *testing.T) {
	names := []string{"Floyd Mayweather Jr.", "Canelo Alvarez", "Mike Tyson", "Manny Pacquiao", "Oscar De La Hoya", "Tyson Fury", "Anthony Joshua", "George Foreman", "Evander Holyfield", "Lennox Lewis"}
	packs := make([]EvidencePack, 0, len(names))
	for _, name := range names {
		pack := completePack(name)
		if name == "Mike Tyson" {
			pack.Sources = pack.Sources[:1]
		}
		packs = append(packs, pack)
	}
	replacement := completePack("Oleksandr Usyk")

	result, err := ResolveRanking(RankingRequest{
		Topic:                 namesTopic,
		InitialCandidates:     names,
		ResearchedPacks:       packs,
		ReplacementCandidates: []EvidencePack{replacement},
	})
	require.NoError(t, err)
	require.Len(t, result.Entries, TenBoxerPackCount)
	require.Len(t, result.FinalPacks, TenBoxerPackCount)
	require.Len(t, result.Replacements, 1)
	require.Equal(t, "Mike Tyson", result.Replacements[0].OriginalSubject)
	require.Equal(t, "Oleksandr Usyk", result.Replacements[0].ReplacementSubject)
	require.NotEmpty(t, result.Replacements[0].OriginalError)
}

func TestResolveRankingPreservesEstimateConflicts(t *testing.T) {
	packs := rankingPacks()
	first, second := 100_000_000.0, 300_000_000.0
	packs[0].CurrentWealthEstimates = []FinancialEvidence{
		{ID: "wealth-low", Label: "current wealth estimate", Value: MoneyValue{Kind: MoneyEstimate, ReportedText: "$100 million", Currency: "USD", Amount: &first}, SourceIDs: []string{"S1"}, Confidence: 0.75},
		{ID: "wealth-high", Label: "current wealth estimate", Value: MoneyValue{Kind: MoneyEstimate, ReportedText: "$300 million", Currency: "USD", Amount: &second}, SourceIDs: []string{"S2"}, Confidence: 0.70},
	}

	result, err := ResolveRanking(RankingRequest{Topic: namesTopic, InitialCandidates: rankingNames(), ResearchedPacks: packs})
	require.NoError(t, err)
	require.Len(t, result.Conflicts, 1)
	require.Equal(t, "current_wealth", result.Conflicts[0].Category)
	require.Equal(t, []float64{first, second}, result.Conflicts[0].ValuesUSD)
	require.Greater(t, result.Conflicts[0].SpreadRatio, 1.20)
	require.Contains(t, result.Entries[0].Rationale, "conflicting estimates")
}

func TestResolveRankingFailsClosedWithoutReplacement(t *testing.T) {
	names := rankingNames()
	packs := rankingPacks()
	packs[0].CareerEarnings = []FinancialEvidence{{
		ID: "unsupported", Label: "career earnings",
		Value:     MoneyValue{Kind: MoneyExact, ReportedText: "an amount was reported but not disclosed"},
		SourceIDs: []string{"S1"}, Confidence: 0.80,
	}}

	_, err := ResolveRanking(RankingRequest{Topic: namesTopic, InitialCandidates: names, ResearchedPacks: packs})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrInsufficientRankingEvidence))
}

func TestResolveRankingUsesDeterministicTieBreak(t *testing.T) {
	result, err := ResolveRanking(RankingRequest{Topic: namesTopic, InitialCandidates: rankingNames(), ResearchedPacks: rankingPacks()})
	require.NoError(t, err)
	for i := 1; i < len(result.Entries); i++ {
		require.LessOrEqual(t, result.Entries[i-1].Subject, result.Entries[i].Subject)
	}
}

func rankingPacks() []EvidencePack {
	names := rankingNames()
	packs := make([]EvidencePack, 0, len(names))
	for _, name := range names {
		packs = append(packs, completePack(name))
	}
	return packs
}

func rankingNames() []string {
	return []string{"Boxer A", "Boxer B", "Boxer C", "Boxer D", "Boxer E", "Boxer F", "Boxer G", "Boxer H", "Boxer I", "Boxer J"}
}

const namesTopic = "the richest boxers"
