package entitycatalog

import (
	"encoding/json"
	"os"
	"testing"
)

type catalogPolicyContract struct {
	Schema          string                      `json:"schema"`
	VerifierVersion string                      `json:"verifier_version"`
	Cases           []catalogPolicyContractCase `json:"cases"`
}

type catalogPolicyContractCase struct {
	Name         string                 `json:"name"`
	WantEligible bool                   `json:"want_eligible"`
	Candidate    catalogPolicyCandidate `json:"candidate"`
}

type catalogPolicyCandidate struct {
	CandidateID       int64   `json:"candidate_id"`
	CanonicalEntityID string  `json:"canonical_entity_id"`
	Provider          string  `json:"provider"`
	Rank              int     `json:"rank"`
	SourceURL         string  `json:"source_url"`
	Width             int     `json:"width"`
	Height            int     `json:"height"`
	Status            string  `json:"status"`
	SemanticStatus    string  `json:"semantic_status"`
	SemanticScore     float64 `json:"semantic_score"`
	TechnicalScore    float64 `json:"technical_score"`
	QualityReason     string  `json:"quality_reason"`
}

func TestCatalogPolicyContractMatchesSharedFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/catalog_policy_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract catalogPolicyContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Schema != "entity_image_catalog_policy.v1" {
		t.Fatalf("schema = %q", contract.Schema)
	}
	if len(contract.Cases) < 7 {
		t.Fatalf("contract cases = %d, want at least 7", len(contract.Cases))
	}
	for _, testCase := range contract.Cases {
		candidate := Candidate{
			ID:                testCase.Candidate.CandidateID,
			CanonicalEntityID: testCase.Candidate.CanonicalEntityID,
			Provider:          testCase.Candidate.Provider,
			Rank:              testCase.Candidate.Rank,
			SourceURL:         testCase.Candidate.SourceURL,
			Width:             testCase.Candidate.Width,
			Height:            testCase.Candidate.Height,
			Status:            testCase.Candidate.Status,
			SemanticStatus:    testCase.Candidate.SemanticStatus,
			SemanticScore:     testCase.Candidate.SemanticScore,
			TechnicalScore:    testCase.Candidate.TechnicalScore,
			QualityReason:     testCase.Candidate.QualityReason,
			VerifierVersion:   contract.VerifierVersion,
		}
		if got := IsUsableCandidateQuality(candidate); got != testCase.WantEligible {
			t.Errorf("%s: IsUsableCandidateQuality = %t, want %t", testCase.Name, got, testCase.WantEligible)
		}
	}
}
