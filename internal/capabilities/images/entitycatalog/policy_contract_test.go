package entitycatalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type catalogPolicyContractFixture struct {
	Version  int    `json:"version"`
	EntityID string `json:"entity_id"`
	Cases    []struct {
		Name            string  `json:"name"`
		Status          string  `json:"status"`
		SemanticStatus  string  `json:"semantic_status"`
		SemanticScore   float64 `json:"semantic_score"`
		TechnicalScore  float64 `json:"technical_score"`
		Width           int     `json:"width"`
		Height          int     `json:"height"`
		QualityReason   string  `json:"quality_reason"`
		VerifierVersion string  `json:"verifier_version"`
		WantEligible    bool    `json:"want_eligible"`
	} `json:"cases"`
}

// TestGoCatalogPolicyMatchesSharedContractFixture pins the accept/reject
// decision for each fixture case. The fixture was MOVED here from
// BrainRust/imagecatalog (retired 2026-11-18, TICKET-28) so the Go policy gate
// keeps the only cross-implementation contract check without a second planner.
func TestGoCatalogPolicyMatchesSharedContractFixture(t *testing.T) {
	path := filepath.Join("testdata", "catalog_policy_contract_rust.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared catalog policy fixture: %v", err)
	}
	var fixture catalogPolicyContractFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode shared catalog policy fixture: %v", err)
	}
	if fixture.Version != 1 || fixture.EntityID == "" || len(fixture.Cases) == 0 {
		t.Fatalf("invalid shared fixture header: version=%d entity=%q cases=%d", fixture.Version, fixture.EntityID, len(fixture.Cases))
	}
	now := time.Unix(0, 0).UTC()
	for _, tc := range fixture.Cases {
		candidate := Candidate{
			CanonicalEntityID: fixture.EntityID,
			Provider:          "contract",
			Rank:              1,
			SourceURL:         "https://example.com/contract.jpg",
			Width:             tc.Width,
			Height:            tc.Height,
			Status:            tc.Status,
			SemanticStatus:    tc.SemanticStatus,
			SemanticScore:     tc.SemanticScore,
			TechnicalScore:    tc.TechnicalScore,
			QualityReason:     tc.QualityReason,
			VerifierVersion:   tc.VerifierVersion,
		}
		eligible := IsUsableCandidateStatus(ClassifyCandidateStatus(now, candidate)) && IsUsableCandidateQuality(candidate)
		if eligible != tc.WantEligible {
			t.Errorf("%s: Go eligible=%v, fixture want=%v (candidate=%+v)", tc.Name, eligible, tc.WantEligible, candidate)
		}
	}
}
