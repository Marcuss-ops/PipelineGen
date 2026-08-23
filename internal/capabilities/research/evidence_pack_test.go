package research

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvidencePackValidateAcceptsCompleteBoxerPack(t *testing.T) {
	amount := 500_000_000.0
	payday := 275_000_000.0
	pack := completePack("Floyd Mayweather Jr.")
	pack.CareerEarnings = []FinancialEvidence{{
		ID: "earnings-career", Label: "career boxing earnings",
		Value:     MoneyValue{Kind: MoneyEstimate, ReportedText: "approximately $500 million", Currency: "USD", Amount: &amount},
		SourceIDs: []string{"S1", "S2"}, Confidence: 0.95,
	}}
	pack.FightPaydays = []FinancialEvidence{{
		ID: "payday-mcgregor", Label: "McGregor fight payday",
		Value:     MoneyValue{Kind: MoneyExact, ReportedText: "$275 million", Currency: "USD", Amount: &payday},
		SourceIDs: []string{"S1"}, Confidence: 0.99,
	}}
	pack.CurrentWealthEstimates = []FinancialEvidence{{
		ID: "wealth-estimate", Label: "current wealth estimate",
		Value:     MoneyValue{Kind: MoneyEstimate, ReportedText: "wealth estimate varies by source", Currency: "USD", Amount: &amount},
		SourceIDs: []string{"S2"}, Confidence: 0.70,
	}}
	pack.Businesses = []BusinessEvidence{{
		ID: "promotion", Name: "Mayweather Promotions", Role: "owner",
		Description: "Owns and operates a boxing promotion business.",
		SourceIDs:   []string{"S2"}, Confidence: 0.90,
	}}
	pack.Endorsements = []EndorsementEvidence{{
		ID: "endorsement-1", Brand: "Brand example",
		Description: "Reported commercial endorsement activity.",
		SourceIDs:   []string{"S2"}, Confidence: 0.80,
	}}
	pack.FinancialEvents = []FinancialEvent{{
		ID: "loss-1", Kind: "loss", Description: "A reported financial setback relevant to retained wealth.",
		SourceIDs: []string{"S2"}, Confidence: 0.85,
	}}

	require.NoError(t, pack.Validate())
}

func TestEvidencePackValidateRejectsDanglingCitation(t *testing.T) {
	pack := completePack("Mike Tyson")
	pack.Facts[0].SourceIDs = []string{"missing-source"}

	err := pack.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidEvidencePack)
	require.Contains(t, err.Error(), "unknown source citation")
}

func TestMoneyValueValidatePreservesRangeAndUndisclosedSemantics(t *testing.T) {
	low, high := 10.0, 20.0
	require.NoError(t, (MoneyValue{
		Kind: MoneyRange, ReportedText: "$10–20 million", Currency: "USD", Low: &low, High: &high,
	}).Validate())
	require.NoError(t, (MoneyValue{
		Kind: MoneyUndisclosed, ReportedText: "amount not disclosed",
	}).Validate())

	bad := MoneyValue{Kind: MoneyUndisclosed, ReportedText: "not disclosed", Currency: "USD", Low: &low, High: &high}
	require.Error(t, bad.Validate())
}

func TestEvidencePackSetValidateRequiresTenUniquePacks(t *testing.T) {
	packs := make([]EvidencePack, 0, TenBoxerPackCount)
	for i := 0; i < TenBoxerPackCount; i++ {
		pack := completePack("Boxer " + string(rune('A'+i)))
		pack.CandidateOrdinal = i + 1
		packs = append(packs, pack)
	}

	require.NoError(t, (EvidencePackSet{
		Version: EvidencePackVersion,
		Topic:   "richest boxers",
		Packs:   packs,
	}).Validate())

	packs[9].Subject = packs[0].Subject
	err := (EvidencePackSet{Version: EvidencePackVersion, Topic: "richest boxers", Packs: packs}).Validate()
	require.ErrorIs(t, err, ErrInvalidEvidencePack)
}

func TestEvidencePackValidateRejectsConfidenceOutsideRange(t *testing.T) {
	pack := completePack("Canelo Alvarez")
	pack.Facts[0].Confidence = 1.01

	err := pack.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidEvidencePack)
}

func completePack(subject string) EvidencePack {
	return EvidencePack{
		Version:    EvidencePackVersion,
		Subject:    subject,
		EntityType: EntityPerson,
		Sources: []EvidenceSource{
			{ID: "S1", URL: "https://www.forbes.com/example", Title: "Financial profile", Publisher: "Forbes", SourceType: "financial_publication", Credibility: CredibilityMajorPublisher, RetrievedAt: "2026-08-17T00:00:00Z"},
			{ID: "S2", URL: "https://www.reuters.com/example", Title: "Career report", Publisher: "Reuters", SourceType: "wire_service", Credibility: CredibilityMajorPublisher, RetrievedAt: "2026-08-17T00:00:00Z"},
		},
		Facts: []EvidenceFact{{
			ID: "identity", Claim: subject + " is a professional boxer.", Category: FactIdentity,
			SourceIDs: []string{"S1"}, Confidence: 0.99,
		}},
	}
}

func TestEvidencePackErrorIsStable(t *testing.T) {
	err := (EvidencePack{Version: EvidencePackVersion}).Validate()
	require.True(t, errors.Is(err, ErrInvalidEvidencePack))
}
