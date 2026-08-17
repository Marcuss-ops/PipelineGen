package usecase

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/research"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

var testBoxers = []string{
	"Floyd Mayweather Jr.",
	"Canelo Alvarez",
	"Mike Tyson",
	"Manny Pacquiao",
	"Oscar De La Hoya",
	"Tyson Fury",
	"Anthony Joshua",
	"George Foreman",
	"Evander Holyfield",
	"Lennox Lewis",
}

// testSearcher returns synthetic search hits for each boxer.
type testSearcher struct{}

func (s *testSearcher) Search(_ context.Context, query string, _ int) ([]scriptports.WebSearchHit, error) {
	return []scriptports.WebSearchHit{
		{Title: query + " career earnings", URL: "https://example.com/" + strings.ReplaceAll(strings.ToLower(query), " ", "-"), Content: "documented career earnings and financial history"},
		{Title: query + " business profile", URL: "https://example.com/business/" + strings.ReplaceAll(strings.ToLower(query), " ", "-"), Content: "documented business ventures and financial history"},
	}, nil
}

// testFetcher returns synthetic page content for each boxer.
type testFetcher struct{}

func (f *testFetcher) Fetch(_ context.Context, url string, _ int) (scriptports.WebPage, error) {
	name := strings.ReplaceAll(strings.TrimPrefix(url, "https://example.com/"), "-", " ")
	return scriptports.WebPage{
		Title: name,
		Text:  fmt.Sprintf("Career earnings and business ventures for %s. Forbes and major publications documented significant wealth and financial outcomes.", name),
	}, nil
}

func TestResearchFanoutAndRankingResolution(t *testing.T) {
	resolver := NewWebResearchResolver(&testSearcher{}, &testFetcher{})
	if err := resolver.SetResearchRanker(scriptports.ResearchRankerFunc(func(_ context.Context, _ string, inputs []scriptports.ResearchCandidateRankingInput) ([]scriptports.ResearchCandidateRanking, error) {
		out := make([]scriptports.ResearchCandidateRanking, len(inputs))
		for i, input := range inputs {
			out[i] = scriptports.ResearchCandidateRanking{CandidateID: input.CandidateID, Rank: i + 1, Score: float64(len(inputs) - i), Rationale: "test ranking"}
		}
		return out, nil
	})); err != nil {
		t.Fatal(err)
	}

	var packs []research.EvidencePack
	for _, boxer := range testBoxers {
		resolved, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
			Type:        scriptpkg.SourceResearch,
			Topic:       boxer + " boxing career earnings business",
			Search:      true,
			Research:    scriptpkg.ResearchPolicy{MaxQueries: 2, MinSources: 1, MaxPages: 2},
			CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModeDisabled},
		}, scriptpkg.SourceResolutionContext{Language: "en"})

		require.NoError(t, err, "research failed for %s", boxer)
		require.NotNil(t, resolved.ResearchReport, "missing report for %s", boxer)
		require.GreaterOrEqual(t, resolved.ResearchReport.AcceptedSources, 1, "insufficient sources for %s", boxer)

		pack := research.EvidencePack{
			Version:    research.EvidencePackVersion,
			Subject:    boxer,
			EntityType: research.EntityPerson,
			Sources: []research.EvidenceSource{
				{
					ID:          "S1",
					URL:         resolved.ResearchReport.Sources[0].URL,
					Title:       resolved.ResearchReport.Sources[0].Title,
					Publisher:   "Major Publication",
					SourceType:  "web",
					Credibility: research.CredibilityMajorPublisher,
					RetrievedAt: "2026-08-17T10:00:00Z",
				},
			},
			Facts: []research.EvidenceFact{
				{
					ID:         "fact-1",
					Claim:      fmt.Sprintf("%s had a significant boxing career with documented earnings.", boxer),
					Category:   research.FactAccomplishment,
					SourceIDs:  []string{"S1"},
					Confidence: 0.9,
				},
			},
			CareerEarnings: []research.FinancialEvidence{
				{
					ID:    "earnings-1",
					Label: "career earnings",
					Value: research.MoneyValue{
						Kind:         research.MoneyEstimate,
						ReportedText: "hundreds of millions",
						Currency:     "USD",
						Amount:       float64Ptr(500_000_000),
					},
					SourceIDs:  []string{"S1"},
					Confidence: 0.8,
				},
			},
		}
		pack.Sources = append(pack.Sources, research.EvidenceSource{
			ID: "S2", URL: "https://example.com/business/" + strings.ReplaceAll(strings.ToLower(boxer), " ", "-"),
			Title: boxer + " business profile", Publisher: "Major Publication", SourceType: "web",
			Credibility: research.CredibilityMajorPublisher, RetrievedAt: "2026-08-17T10:00:00Z",
		})

		switch boxer {
		case "Floyd Mayweather Jr.":
			pack.CareerEarnings[0].Value.Amount = float64Ptr(1_000_000_000)
			pack.CurrentWealthEstimates = []research.FinancialEvidence{{
				ID: "wealth-1", Label: "current wealth estimate",
				Value:     research.MoneyValue{Kind: research.MoneyEstimate, ReportedText: "$500 million", Currency: "USD", Amount: float64Ptr(500_000_000)},
				SourceIDs: []string{"S1"}, Confidence: 0.8,
			}}
			pack.FightPaydays = []research.FinancialEvidence{{
				ID: "payday-1", Label: "largest reported payday",
				Value:     research.MoneyValue{Kind: research.MoneyExact, ReportedText: "$275 million", Currency: "USD", Amount: float64Ptr(275_000_000)},
				SourceIDs: []string{"S1"}, Confidence: 0.9,
			}}
		case "Canelo Alvarez":
			pack.CareerEarnings[0].Value.Amount = float64Ptr(400_000_000)
		case "Mike Tyson":
			pack.CareerEarnings[0].Value.Amount = float64Ptr(400_000_000)
			pack.FinancialEvents = append(pack.FinancialEvents, research.FinancialEvent{
				ID:          "event-bankruptcy",
				Kind:        "bankruptcy",
				Description: "Filed for Chapter 11 bankruptcy",
				SourceIDs:   []string{"S1"},
				Confidence:  0.9,
				Impact:      &research.MoneyValue{Kind: research.MoneyEstimate, ReportedText: "significant loss", Currency: "USD", Amount: float64Ptr(300_000_000)},
			})
		case "Manny Pacquiao":
			pack.CareerEarnings[0].Value.Amount = float64Ptr(600_000_000)
		case "George Foreman":
			pack.Businesses = append(pack.Businesses, research.BusinessEvidence{
				ID:               "biz-grill",
				Name:             "George Foreman Grill",
				Description:      "Highly successful commercial product line",
				FinancialOutcome: &research.MoneyValue{Kind: research.MoneyEstimate, ReportedText: "hundreds of millions in sales", Currency: "USD", Amount: float64Ptr(200_000_000)},
				SourceIDs:        []string{"S1"},
				Confidence:       0.95,
			})
		}

		packs = append(packs, pack)
	}

	result, err := research.ResolveRanking(research.RankingRequest{
		Topic:             "The 10 Richest Boxers of All Time",
		InitialCandidates: testBoxers,
		ResearchedPacks:   packs,
	})
	require.NoError(t, err)
	require.Len(t, result.Entries, 10)
	require.Len(t, result.FinalPacks, 10)

	t.Logf("Final Ranking:")
	for _, entry := range result.Entries {
		t.Logf("Rank %d: %s (Score: %.4f)", entry.Rank, entry.Subject, entry.Score)
	}

	require.Equal(t, "Floyd Mayweather Jr.", result.Entries[0].Subject, "Mayweather should be #1")
	require.Equal(t, "George Foreman", result.Entries[1].Subject, "Foreman should be #2 due to massive business evidence")
	require.Equal(t, "Manny Pacquiao", result.Entries[2].Subject, "Pacquiao should be #3")

	var tysonRank int
	for _, entry := range result.Entries {
		if entry.Subject == "Mike Tyson" {
			tysonRank = entry.Rank
			break
		}
	}
	require.Greater(t, tysonRank, 3, "Tyson should be ranked lower due to bankruptcy penalty")
}

func float64Ptr(f float64) *float64 {
	return &f
}
