package vidrush

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// seedVerifiedPortraitCatalog fills a catalog with the shape produced by a
// successful earlier run: accepted verdicts, current verifier version and a
// completed Drive materialization.
func seedVerifiedPortraitCatalog(t *testing.T, repo *integrationEntityImageCatalog, name string, count int) entitycatalog.PersonIdentity {
	t.Helper()
	ctx := context.Background()
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
	for i := 0; i < count; i++ {
		id, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{
			CanonicalEntityID: identity.CanonicalEntityID,
			Provider:          "duckduckgo",
			Rank:              i + 1,
			SourceURL:         fmt.Sprintf("https://images.example/%s/verified-%02d.jpg", nameSlug, i+1),
			Status:            entitycatalog.CandidateStatusFresh,
			SemanticStatus:    entitycatalog.CandidateSemanticAccepted,
			SemanticScore:     0.98,
			VerifierVersion:   entitycatalog.CandidateVerifierVersion,
			QualityReason:     "ollama visual identity verification confirmed",
			Width:             1200,
			Height:            800,
			ContentSHA256:     strings.Repeat("c", 64),
			LastSeenAt:        time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("seed verified row %d: %v", i+1, err)
		}
		if err := repo.UpsertMaterialization(ctx, entitycatalog.Materialization{
			CandidateID:   id,
			Status:        entitycatalog.MaterializationStatusMaterialized,
			AssetID:       fmt.Sprintf("drive-asset-%d", id),
			DriveLink:     fmt.Sprintf("https://drive.example/drive-asset-%d", id),
			ContentSHA256: strings.Repeat("d", 64),
		}); err != nil {
			t.Fatalf("seed materialization %d: %v", id, err)
		}
	}
	return identity
}

func forceRefreshCatalogPlan() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		Language: "it",
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy:     mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
			ForceRefreshAssets: true,
			Extraction: mediadomain.MediaExtractionPolicy{EntityImages: mediadomain.EntityImagePolicy{
				Enabled: true, EntityTypes: []string{"PERSON"}, Scope: "per_scene",
			}},
		},
	}
}

// TestForceRefreshAssetsKeepsVerifiedCatalogFallback covers the R22 regression
// where `force_refresh_assets: true` plus a degraded VLM (all new candidates
// "unknown") published zero images even though the catalog already held
// accepted + materialized portraits. Under force the catalog is not a cache
// hit, but it must remain the fallback for the refresh.
func TestForceRefreshAssetsKeepsVerifiedCatalogFallback(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	identity := seedVerifiedPortraitCatalog(t, repo, "Corrado Augias", 4)

	resolution, err := resolveEntityImageCatalogCache(context.Background(), entityImageResolverDeps{catalog: repo},
		forceRefreshCatalogPlan(), "Corrado Augias", "force-refresh-entity-key", identity, true, false)
	if err != nil {
		t.Fatalf("resolveEntityImageCatalogCache: %v", err)
	}
	if resolution.FromCache {
		t.Fatal("force_refresh_assets must not be satisfied by a cache hit")
	}
	if len(resolution.Candidates) != 0 {
		t.Fatalf("forced refresh returned %d pre-resolved candidates, want an empty discovery set", len(resolution.Candidates))
	}
	if len(resolution.CatalogFallback) != 4 {
		t.Fatalf("catalog fallback = %d candidates, want the 4 verified portraits retained across the refresh", len(resolution.CatalogFallback))
	}
	for _, candidate := range resolution.CatalogFallback {
		if candidate.DriveLink == "" || candidate.ContentSHA256 == "" {
			t.Fatalf("fallback candidate %q lost its materialization (drive=%q sha=%q)",
				candidate.AssetID, candidate.DriveLink, candidate.ContentSHA256)
		}
	}
}

// refreshImageSearcher is the degraded provider lane: it either fails
// (429/timeout/EOF) or returns no usable row because the VLM could not confirm
// any of them.
type refreshImageSearcher struct {
	err error
}

func (s refreshImageSearcher) SearchImages(context.Context, InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if s.err != nil {
		return nil, s.err
	}
	return nil, nil
}

func forceRefreshFanout(t *testing.T, repo *integrationEntityImageCatalog, identity entitycatalog.PersonIdentity, searcher InternetImageSearcher) vidRushProviderOutcome {
	t.Helper()
	plan := forceRefreshCatalogPlan()
	plan.ImagesPerScene = 1
	fanout := NewVidRushProviderFanoutWithCatalog(searcher, nil, repo)
	worker := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1", SceneID: "scene-1", TextHash: "force-refresh-text-hash",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{{
				Value: "Corrado Augias", Type: "PERSON", CanonicalID: identity.CanonicalEntityID,
				SearchIntents: []string{"Corrado Augias"},
			}},
			ImageEntityCanonicalIDs: map[string]string{"Corrado Augias": identity.CanonicalEntityID},
		},
	}
	outcomes := make(chan vidRushProviderOutcome, 1)
	fanout.resolveInternetImageCandidates(context.Background(), plan, vidRushFanoutPlan{
		segmentID: "scene-1", textHash: worker.TextHash, imagesEnabled: true,
		imageQueries: []string{"Corrado Augias portrait"},
	}, worker, outcomes)
	return <-outcomes
}

// TestForceRefreshWithFailedVLMStillPublishesCatalogPortraits covers the exact
// R22 shape at the end of the discovery lane: `force_refresh_assets: true` and
// a refresh that returns nothing usable (VLM offline/throttled) must still
// hand the already verified, materialized portraits to the materializer.
func TestForceRefreshWithFailedVLMStillPublishesCatalogPortraits(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	identity := seedVerifiedPortraitCatalog(t, repo, "Corrado Augias", 3)

	outcome := forceRefreshFanout(t, repo, identity, refreshImageSearcher{})
	if outcome.err != nil {
		t.Fatalf("empty refresh should not be fatal: %v", outcome.err)
	}
	if len(outcome.candidates) != 3 {
		t.Fatalf("candidates after an unusable refresh = %d, want the 3 verified catalog portraits: %+v",
			len(outcome.candidates), outcome.candidates)
	}
	for _, candidate := range outcome.candidates {
		if candidate.DriveLink == "" {
			t.Fatalf("fallback candidate %q reached the materializer without its Drive copy", candidate.AssetID)
		}
	}
}

// TestForceRefreshProviderOutageKeepsCatalogPortraits covers the other
// degradation shape (429/timeout): the provider error is reported, but the
// verified portraits still travel with the outcome so the scene is not empty.
func TestForceRefreshProviderOutageKeepsCatalogPortraits(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	identity := seedVerifiedPortraitCatalog(t, repo, "Corrado Augias", 3)

	outcome := forceRefreshFanout(t, repo, identity, refreshImageSearcher{err: errors.New("provider reset by peer")})
	if outcome.err == nil {
		t.Fatal("provider outage must still be reported as a degradation")
	}
	if len(outcome.candidates) != 3 {
		t.Fatalf("candidates after a provider outage = %d, want the 3 verified catalog portraits kept as fallback",
			len(outcome.candidates))
	}
}

// TestForceRefreshCatalogFallbackUnavailableIsExplicit keeps the failure
// honest: when the catalog itself cannot be read, the resolver must surface
// the error instead of pretending the refresh started with an empty fallback.
func TestForceRefreshCatalogFallbackUnavailableIsExplicit(t *testing.T) {
	resetEntityImageCatalogCaches()
	_, err := resolveEntityImageCatalogCache(context.Background(), entityImageResolverDeps{catalog: &erroringCatalog{integrationEntityImageCatalog: newIntegrationEntityImageCatalog()}},
		forceRefreshCatalogPlan(), "Corrado Augias", "force-refresh-error-key", entitycatalog.PersonIdentity{
			CanonicalEntityID: "person:corrado-augias", CanonicalName: "Corrado Augias",
		}, true, false)
	if err == nil || !errors.Is(err, errCatalogUnavailable) {
		t.Fatalf("error = %v, want the catalog read failure surfaced", err)
	}
}

var errCatalogUnavailable = errors.New("catalog unavailable")

type erroringCatalog struct{ *integrationEntityImageCatalog }

func (*erroringCatalog) ListCandidates(context.Context, string, int) ([]entitycatalog.Candidate, error) {
	return nil, errCatalogUnavailable
}
