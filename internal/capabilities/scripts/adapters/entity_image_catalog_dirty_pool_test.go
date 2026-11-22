package adapters

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

// TestEntityImageCatalogDirtyHeadKeepsAcceptedMaterializedPortraits replays the
// R21/R23 catalog shape seen in media.db.sqlite: the best-ranked rows are
// rejected, unknown or broken, and only ranks 11..15 are accepted + already
// materialized. The candidate pool must be filled from the accepted rows, not
// from the dirty head, otherwise usableCount stays 0 while verified portraits
// exist and every scene re-runs provider search + VLM.
//
// The test is pure: it asserts on entityImageCatalogCandidates only, so it
// proves the catalog decision without any provider, network or Ollama call.
func TestEntityImageCatalogDirtyHeadKeepsAcceptedMaterializedPortraits(t *testing.T) {
	resetEntityImageCatalogCaches()
	ctx := context.Background()
	repo := newIntegrationEntityImageCatalog()

	const name = "Corrado Augias"
	identity, err := entitycatalog.CanonicalizePersonName(name)
	if err != nil {
		t.Fatalf("canonicalize %q: %v", name, err)
	}
	if err := repo.UpsertEntity(ctx, entitycatalog.Entity{
		CanonicalEntityID: identity.CanonicalEntityID,
		EntityType:        entitycatalog.EntityTypePerson,
		CanonicalName:     identity.CanonicalName,
	}); err != nil {
		t.Fatalf("upsert entity: %v", err)
	}
	nameSlug := strings.Join(strings.FieldsFunc(normalizeEntityMatch(identity.CanonicalName), func(r rune) bool {
		return r < 'a' || r > 'z'
	}), "-")
	sourceURL := func(tag string) string {
		return fmt.Sprintf("https://images.example/%s/%s.jpg", nameSlug, tag)
	}

	// Ranks 1..6: the dirty head. These are the rows that legitimately enter
	// the pool without a usable verdict (layout recertification resets the
	// semantic state to unknown). With a rank-first scan they consume the whole
	// bounded pool and usableCount drops to 0 even though verified portraits
	// exist further down the ranking — the R21/R23 failure.
	const recertifyRows = 6
	for i := 0; i < recertifyRows; i++ {
		if _, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{
			CanonicalEntityID: identity.CanonicalEntityID,
			Provider:          "duckduckgo",
			Rank:              i + 1,
			SourceURL:         sourceURL(fmt.Sprintf("recertify-%02d", i+1)),
			Status:            entitycatalog.CandidateStatusFresh,
			SemanticStatus:    entitycatalog.CandidateSemanticAccepted,
			SemanticScore:     1,
			VerifierVersion:   entitycatalog.CandidateVerifierVersion,
			QualityReason:     "Ollama visual verification did not find one clear person",
			Width:             1200,
			Height:            800,
			LastSeenAt:        time.Now().UTC(),
		}); err != nil {
			t.Fatalf("seed recertify row %d: %v", i+1, err)
		}
	}
	// Ranks 7..10: rejected, broken, retired and legacy rows. They must never be
	// admitted to the pool, but they still occupy the best ranks.
	dirtyHead := []struct {
		status   string
		semantic string
	}{
		{entitycatalog.CandidateStatusFresh, entitycatalog.CandidateSemanticRejected},
		{entitycatalog.CandidateStatusBroken, entitycatalog.CandidateSemanticAccepted},
		{entitycatalog.CandidateStatusFresh, entitycatalog.CandidateSemanticUnknown},
		{"", entitycatalog.CandidateSemanticUnknown}, // legacy row with no stored status
	}
	for i, row := range dirtyHead {
		if _, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{
			CanonicalEntityID: identity.CanonicalEntityID,
			Provider:          "duckduckgo",
			Rank:              recertifyRows + i + 1,
			SourceURL:         sourceURL(fmt.Sprintf("dirty-%02d", i+1)),
			Status:            row.status,
			SemanticStatus:    row.semantic,
			SemanticScore:     0.2,
			VerifierVersion:   entitycatalog.CandidateVerifierVersion,
			QualityReason:     "rejected by the visual identity gate",
			Width:             1200,
			Height:            800,
			LastSeenAt:        time.Now().UTC(),
		}); err != nil {
			t.Fatalf("seed dirty row %d: %v", i+1, err)
		}
	}

	// Ranks 11..15: accepted, fresh and materialized on Drive. These are the
	// only rows that can serve the scene without a provider/VLM round-trip.
	const acceptedRows = 5
	acceptedIDs := make([]int64, 0, acceptedRows)
	for i := 0; i < acceptedRows; i++ {
		id, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{
			CanonicalEntityID: identity.CanonicalEntityID,
			Provider:          "duckduckgo",
			Rank:              recertifyRows + len(dirtyHead) + 1 + i,
			SourceURL:         sourceURL(fmt.Sprintf("accepted-%02d", i+1)),
			Status:            entitycatalog.CandidateStatusActive,
			SemanticStatus:    entitycatalog.CandidateSemanticAccepted,
			SemanticScore:     0.97,
			VerifierVersion:   entitycatalog.CandidateVerifierVersion,
			QualityReason:     "manual visual identity review confirmed",
			Width:             1200,
			Height:            800,
			ContentSHA256:     strings.Repeat("a", 64),
			LastSeenAt:        time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("seed accepted row %d: %v", i+1, err)
		}
		acceptedIDs = append(acceptedIDs, id)
		if err := repo.UpsertMaterialization(ctx, entitycatalog.Materialization{
			CandidateID:   id,
			Status:        entitycatalog.MaterializationStatusMaterialized,
			AssetID:       fmt.Sprintf("asset-%d", id),
			DriveLink:     fmt.Sprintf("https://drive.example/asset-%d", id),
			ContentSHA256: strings.Repeat("b", 64),
		}); err != nil {
			t.Fatalf("seed materialization %d: %v", id, err)
		}
	}

	const limit = 6 // entityImageCatalogPoolSize shrunk to a per-scene request
	pool, err := entityImageCatalogCandidates(ctx, repo, identity, limit)
	if err != nil {
		t.Fatalf("entityImageCatalogCandidates: %v", err)
	}

	if pool.KnownCount != recertifyRows+len(dirtyHead)+acceptedRows {
		t.Fatalf("KnownCount = %d, want %d (one count per scanned row)", pool.KnownCount, len(dirtyHead)+acceptedRows)
	}
	if len(pool.Candidates) != limit {
		t.Fatalf("pool size = %d, want the bounded limit %d", len(pool.Candidates), limit)
	}
	if want := entityImageCatalogPoolTarget(limit); pool.UsableCount < want {
		t.Fatalf("UsableCount = %d, want >= reusable target %d: the dirty head crowded the accepted portraits out of the pool", pool.UsableCount, want)
	}
	if !pool.Sufficient {
		t.Fatalf("pool reported insufficient (usable=%d of %d known) even though %d accepted materialized portraits exist",
			pool.UsableCount, pool.KnownCount, acceptedRows)
	}

	// Every accepted row must arrive already hydrated from its Drive
	// materialization: that is what lets the materializer skip download/upload.
	materialized := 0
	seenAccepted := make(map[int64]bool, acceptedRows)
	for _, candidate := range pool.Candidates {
		if !strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), entitycatalog.CandidateSemanticAccepted) {
			continue
		}
		if candidate.DriveLink == "" || candidate.ContentSHA256 == "" {
			t.Fatalf("accepted candidate %q reached the pool without its materialization (drive=%q sha=%q)",
				candidate.AssetID, candidate.DriveLink, candidate.ContentSHA256)
		}
		if candidate.AcquisitionStatus != "acquired" || candidate.IndexStatus != "indexed" {
			t.Fatalf("accepted candidate %q not hydrated: acquisition=%q index=%q",
				candidate.AssetID, candidate.AcquisitionStatus, candidate.IndexStatus)
		}
		materialized++
		for _, id := range acceptedIDs {
			if candidate.AssetID == fmt.Sprintf("asset-%d", id) {
				seenAccepted[id] = true
			}
		}
	}
	if materialized == 0 {
		t.Fatalf("no accepted materialized candidate survived the dirty head; decisions=%v", pool.Decisions)
	}
	if len(seenAccepted) != materialized {
		t.Fatalf("hydrated candidates = %d but only %d map back to seeded accepted IDs: %v",
			materialized, len(seenAccepted), pool.Decisions)
	}
	if materialized != acceptedRows {
		t.Fatalf("accepted materialized portraits in pool = %d, want all %d", materialized, acceptedRows)
	}

	// The dirty head must be auditable: the recertification row that survives
	// the cut is still carried as a retry state (unknown), never silently
	// dropped, and the rejected rows are traced as lifecycle/quality
	// rejections.
	// The pool stops at the request limit, so rows below the cut are not walked
	// (the warm-replay invariant: at most `limit` admissions per lookup).
	unknownInPool := 0
	for _, candidate := range pool.Candidates {
		if strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), entitycatalog.CandidateSemanticUnknown) {
			unknownInPool++
		}
	}
	if unknownInPool == 0 {
		t.Fatalf("no recertification row reached the pool as a retry state, decisions=%v", pool.Decisions)
	}
}
