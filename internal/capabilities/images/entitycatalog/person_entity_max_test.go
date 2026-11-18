package entitycatalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
)

// TestPersonEntityMaxCanonicalParityGo pins the canonical PERSON derivation on
// the surviving Go owner. The Rust imagecatalog crate was retired by TICKET-28;
// this table is the reachable replacement for its canonical parity test.
func TestPersonEntityMaxCanonicalParityGo(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		wantID string
	}{
		{name: "plain", input: "Michael Jordan", wantID: "person:michael-jordan"},
		{name: "possessive straight", input: "Donald Trump's", wantID: "person:donald-trump"},
		{name: "possessive curly", input: "Donald Trump’s", wantID: "person:donald-trump"},
		{name: "accented", input: "José", wantID: "person:jos"},
		{name: "curly apostrophe", input: "O’Connor", wantID: "person:o-connor"},
		{name: "middle initial", input: "Michael B. Jordan", wantID: "person:michael-b--jordan"},
		{name: "cjk has no ascii slug", input: "李小龙", wantID: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			identity, err := CanonicalizePersonName(tc.input)
			if tc.wantID == "" {
				if err == nil || identity.CanonicalEntityID != "" {
					t.Fatalf("CanonicalizePersonName(%q) = %+v, %v; want no minted id", tc.input, identity, err)
				}
				if shared := capabilityentities.CanonicalEntityID(EntityTypePerson, tc.input); shared != "" {
					t.Fatalf("shared canonical owner minted %q for %q, want empty", shared, tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("CanonicalizePersonName(%q): %v", tc.input, err)
			}
			if identity.CanonicalEntityID != tc.wantID {
				t.Fatalf("CanonicalizePersonName(%q) = %q, want %q", tc.input, identity.CanonicalEntityID, tc.wantID)
			}
			if shared := capabilityentities.CanonicalEntityID(EntityTypePerson, tc.input); shared != identity.CanonicalEntityID {
				t.Fatalf("entitycatalog id %q != shared owner id %q for %q", identity.CanonicalEntityID, shared, tc.input)
			}
		})
	}
}

// TestPersonEntityMaxSevenDayBoundary pins the exact timestamps named by the
// retired Rust fixture: 2026-11-03T00:00:00Z is still fresh and one second
// earlier is stale at the 2026-11-10T00:00:00Z evaluation instant.
func TestPersonEntityMaxSevenDayBoundary(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		lastSeen time.Time
		want     string
		refresh  bool
	}{
		{
			name:     "exactly seven days is fresh",
			lastSeen: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC),
			want:     CandidateStatusFresh,
		},
		{
			name:     "one second past seven days is stale",
			lastSeen: time.Date(2026, 11, 2, 23, 59, 59, 0, time.UTC),
			want:     CandidateStatusStale,
			refresh:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := Candidate{Status: CandidateStatusFresh, LastSeenAt: tc.lastSeen}
			if got := ClassifyCandidateStatus(now, candidate); got != tc.want {
				t.Fatalf("ClassifyCandidateStatus = %q, want %q", got, tc.want)
			}
			assessment := AssessCandidateState(now, candidate)
			if assessment.State != tc.want || !assessment.Usable || assessment.RefreshRecommended != tc.refresh {
				t.Fatalf("assessment = %+v, want state=%q usable=true refresh=%v", assessment, tc.want, tc.refresh)
			}
		})
	}
}

// TestPersonEntityMaxPolicyContractKeepsElevenCases pins the shared fixture
// count and the two unknown-state cases that must stay ineligible. The
// accept/reject decisions themselves are exercised by
// TestGoCatalogPolicyMatchesSharedContractFixture.
func TestPersonEntityMaxPolicyContractKeepsElevenCases(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "catalog_policy_contract_rust.json"))
	if err != nil {
		t.Fatalf("read shared catalog policy fixture: %v", err)
	}
	var fixture catalogPolicyContractFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode shared catalog policy fixture: %v", err)
	}
	if fixture.Version != 1 || len(fixture.Cases) != 11 {
		t.Fatalf("fixture version/cases = %d/%d, want 1/11", fixture.Version, len(fixture.Cases))
	}
	wantNames := []string{
		"fresh accepted with current verifier",
		"stale accepted remains eligible",
		"unknown semantic status fails closed",
		"rejected semantic status fails closed",
		"semantic score below threshold",
		"technical score below threshold",
		"dimensions below catalog minimum",
		"broken remote URL without durable materialization",
		"retired status is terminal",
		"unknown stored status fails closed",
		"empty stored status fails closed",
	}
	byName := make(map[string]int, len(fixture.Cases))
	for index, tc := range fixture.Cases {
		byName[tc.Name] = index
	}
	for _, name := range wantNames {
		if _, ok := byName[name]; !ok {
			t.Fatalf("shared fixture is missing case %q", name)
		}
	}
	now := time.Unix(0, 0).UTC()
	for _, name := range []string{"unknown stored status fails closed", "empty stored status fails closed"} {
		index, ok := byName[name]
		if !ok {
			t.Fatalf("shared fixture is missing case %q", name)
		}
		tc := fixture.Cases[index]
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
		if got := ClassifyCandidateStatus(now, candidate); got != CandidateStatusUnknown {
			t.Fatalf("%s classified as %q, want %q", name, got, CandidateStatusUnknown)
		}
		if IsUsableCandidateStatus(ClassifyCandidateStatus(now, candidate)) {
			t.Fatalf("%s is usable, want fail-closed", name)
		}
	}
}
