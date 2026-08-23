package script

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func testResearchEvidencePack() *ResearchEvidencePack {
	return &ResearchEvidencePack{
		Version: ResearchEvidenceVersion,
		Topic:   "richest boxers",
		Candidates: []RankedResearchCandidate{
			{
				CandidateID: "tyson", Label: "Mike Tyson", Rank: 2,
				Fingerprint: "candidate-tyson", Sources: []ResearchWebSource{{ID: "tyson:S1", URL: "https://example.com/tyson"}},
				Claims: []ResearchClaim{{Text: "Tyson had documented earnings.", Verified: true, SourceIDs: []string{"tyson:S1"}}},
			},
			{
				CandidateID: "ali", Label: "Muhammad Ali", Rank: 1,
				Fingerprint: "candidate-ali", Sources: []ResearchWebSource{{ID: "ali:S1", URL: "https://example.com/ali"}},
				Claims: []ResearchClaim{{Text: "Ali had documented earnings.", Verified: true, SourceIDs: []string{"ali:S1"}}},
			},
		},
	}
}

func TestResearchEvidencePackValidatesNamespacesAndDerivedText(t *testing.T) {
	pack := testResearchEvidencePack()
	require.NoError(t, pack.Validate())

	text := pack.ModelSourceText()
	require.True(t, strings.Index(text, "RANK 1") < strings.Index(text, "RANK 2"))
	require.Contains(t, text, "Ali had documented earnings.")
	require.Contains(t, text, "Tyson had documented earnings.")

	fingerprint, err := pack.ComputeFingerprint()
	require.NoError(t, err)
	pack.Fingerprint = fingerprint
	clone := pack.Clone()
	require.Equal(t, pack, clone)

	pack.Candidates[1].Claims[0].SourceIDs = []string{"ali:S2"}
	require.Error(t, pack.Validate())
}

func TestResearchEvidencePackRejectsDuplicateOrMissingRanks(t *testing.T) {
	pack := testResearchEvidencePack()
	pack.Candidates[1].Rank = 2
	require.Error(t, pack.Validate())

	pack = testResearchEvidencePack()
	pack.Candidates[1].Rank = 3
	require.Error(t, pack.Validate())
}
