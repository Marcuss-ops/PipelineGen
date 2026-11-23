package adapters

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

// TestEntityImageCatalogLifecycleFailsClosedExceptBrokenDurable pins the
// catalog policy at the pool boundary: a verified materialization may keep a
// broken remote URL usable, but it must never resurrect a retired or
// unrecognized row. The Rust imagecatalog applies the same rule.
func TestEntityImageCatalogLifecycleFailsClosedExceptBrokenDurable(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name                string
		status              string
		lastSeen            time.Time
		materialized        bool
		wantCandidates      int
		wantGate            string
		wantSelection       string
		wantMaterialization string
	}{
		{
			name: "retired with verified materialization is never selectable", status: entitycatalog.CandidateStatusRetired,
			materialized: true, wantCandidates: 0, wantGate: "lifecycle_quality", wantSelection: "rejected", wantMaterialization: "materialized",
		},
		{
			name: "broken with verified materialization stays reusable", status: entitycatalog.CandidateStatusBroken,
			materialized: true, wantCandidates: 1, wantGate: "quality", wantSelection: "pool", wantMaterialization: "materialized",
		},
		{
			name: "empty legacy status fails closed", status: "", lastSeen: now,
			wantCandidates: 0, wantGate: "lifecycle_quality", wantSelection: "rejected", wantMaterialization: "missing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newIntegrationEntityImageCatalog()
			identity, candidateID := seedStatusCatalogCandidate(t, repo, tc.status, tc.lastSeen, tc.materialized)
			pool, err := entityImageCatalogCandidates(context.Background(), repo, identity, 10, nil)
			if err != nil {
				t.Fatalf("catalog pool: %v", err)
			}
			if len(pool.Candidates) != tc.wantCandidates {
				t.Fatalf("candidates = %d, want %d (decisions=%+v)", len(pool.Candidates), tc.wantCandidates, pool.Decisions)
			}
			decision, ok := catalogDecisionFor(pool.Decisions, candidateID)
			if !ok {
				t.Fatalf("no decision for candidate %d: %+v", candidateID, pool.Decisions)
			}
			if decision.Gate != tc.wantGate || decision.Selection != tc.wantSelection || decision.Materialization != tc.wantMaterialization {
				t.Fatalf("decision = %+v, want gate=%q selection=%q materialization=%q", decision, tc.wantGate, tc.wantSelection, tc.wantMaterialization)
			}
		})
	}
}

func seedStatusCatalogCandidate(t *testing.T, repo *integrationEntityImageCatalog, status string, lastSeen time.Time, materialized bool) (entitycatalog.PersonIdentity, int64) {
	t.Helper()
	identity, err := entitycatalog.CanonicalizePersonName("Michael Jordan")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.UpsertEntity(ctx, entitycatalog.Entity{
		CanonicalEntityID: identity.CanonicalEntityID, EntityType: entitycatalog.EntityTypePerson, CanonicalName: identity.CanonicalName,
	}); err != nil {
		t.Fatal(err)
	}
	candidateID, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{
		CanonicalEntityID: identity.CanonicalEntityID, Provider: "duckduckgo", Rank: 1,
		SourceURL: "https://images.example/michael-jordan/status.jpg",
		Width:     1200, Height: 800, Status: status, LastSeenAt: lastSeen,
		SemanticStatus: entitycatalog.CandidateSemanticAccepted, SemanticScore: 0.92, TechnicalScore: 0.88,
		VerifierVersion: entitycatalog.CandidateVerifierVersion,
		QualityReason:   "manual visual identity review confirmed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if materialized {
		if err := repo.UpsertMaterialization(ctx, entitycatalog.Materialization{
			CandidateID: candidateID, Status: entitycatalog.MaterializationStatusMaterialized,
			AssetID: "asset-1", DriveLink: "https://drive.example/asset-1",
			ContentSHA256: strings.Repeat("a", 64),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return identity, candidateID
}

func catalogDecisionFor(decisions []EntityImageCatalogDecision, candidateID int64) (EntityImageCatalogDecision, bool) {
	for _, decision := range decisions {
		if decision.CandidateID == candidateID {
			return decision, true
		}
	}
	return EntityImageCatalogDecision{}, false
}
