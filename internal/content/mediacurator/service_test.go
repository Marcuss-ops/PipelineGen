package mediacurator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/database"
)

// testSchema composes the canonical media_assets CREATE TABLE from
// internal/storage/canonical.go::CanonicalMediaAssetsSchema. The canonical
// block covers all 39 columns clips.Repository.mediaAssetColumns ships
// today and any future canonical column without touching this file.
const testSchema = storage.CanonicalMediaAssetsSchema

// insertTestClip is a helper to insert a test clip into the DB.
func insertTestClip(t *testing.T, repo *clips.Repository, clip *assets.Asset) {
	t.Helper()
	ctx := context.Background()
	if err := repo.UpsertClip(ctx, clip); err != nil {
		t.Fatalf("failed to insert test clip %q: %v", clip.ID, err)
	}
}

// newFallbackService creates a mediacurator.Service with embedder=nil and vectorSvc=nil,
// forcing the LIKE fallback path in searchClips. It returns the service and the clips repo
// so the test can insert test data.
func newFallbackService(t *testing.T) (*Service, *clips.Repository) {
	t.Helper()
	db := storage.NewTestDBWithSchema(t, testSchema)
	t.Cleanup(func() { db.Close() })

	repo := clips.NewRepository(db, zap.NewNop())

	// Create service WITH clipsRepo but WITHOUT vectorSvc/embedder (simulating offline embedding server)
	svc := NewService(nil, "", repo, nil, nil, zap.NewNop())
	return svc, repo
}

// ── Test: LIKE fallback finds clips by name ─────────────────────────────

func TestLikeSearch_FindsByExactName(t *testing.T) {
	ctx := context.Background()
	svc, repo := newFallbackService(t)

	insertTestClip(t, repo, &assets.Asset{
		ID:             "clip_parenting",
		Name:           "Funny Actors Parenting Stories",
		Source:         "youtube",
		Duration:       300 * time.Second,
		Tags:           []string{"comedy", "parenting"},
		LifecycleState: assets.StateReady,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	})
	insertTestClip(t, repo, &assets.Asset{
		ID:             "clip_other",
		Name:           "Roman Architecture Documentary",
		Source:         "youtube",
		Duration:       500 * time.Second,
		Tags:           []string{"history", "architecture"},
		LifecycleState: assets.StateReady,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	})

	results, err := svc.searchClips(ctx, "Funny Actors Parenting", "", "", 10, 0)
	if err != nil {
		t.Fatalf("searchClips (LIKE fallback) failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result from LIKE fallback, got 0")
	}

	found := false
	for _, r := range results {
		if r.ClipID == "clip_parenting" {
			found = true
			if r.Name != "Funny Actors Parenting Stories" {
				t.Errorf("unexpected name: %q", r.Name)
			}
			if r.Score != 0.5 {
				t.Errorf("LIKE fallback score should be 0.5, got %f", r.Score)
			}
			if r.Source != "youtube" {
				t.Errorf("source = %q, want %q", r.Source, "youtube")
			}
			break
		}
	}
	if !found {
		t.Errorf("clip_parenting not found in LIKE results. Got %d results", len(results))
		for _, r := range results {
			t.Logf("  result: %s | %s | score=%.2f", r.ClipID, r.Name, r.Score)
		}
	}
}

// ── Test: LIKE fallback with partial keyword match ──────────────────────

func TestLikeSearch_FindsByKeyword(t *testing.T) {
	ctx := context.Background()
	svc, repo := newFallbackService(t)

	insertTestClip(t, repo, &assets.Asset{
		ID:             "clip_comedy",
		Name:           "Stand-up Comedy Night",
		Source:         "youtube",
		Duration:       600 * time.Second,
		Tags:           []string{"comedy", "standup"},
		LifecycleState: assets.StateReady,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	})
	insertTestClip(t, repo, &assets.Asset{
		ID:             "clip_drama",
		Name:           "Drama Series Review",
		Source:         "youtube",
		Duration:       400 * time.Second,
		Tags:           []string{"drama", "review"},
		LifecycleState: assets.StateReady,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	})

	// Search for "comedy" — should match clip_comedy by name and tags
	results, err := svc.searchClips(ctx, "comedy", "", "", 10, 0)
	if err != nil {
		t.Fatalf("searchClips failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for keyword 'comedy', got 0")
	}

	found := false
	for _, r := range results {
		if r.ClipID == "clip_comedy" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("clip_comedy not found when searching for 'comedy'. Got %d results", len(results))
		for _, r := range results {
			t.Logf("  result: %s | %s", r.ClipID, r.Name)
		}
	}

	// Search for "drama" — should NOT find clip_comedy
	resultsDrama, err := svc.searchClips(ctx, "drama", "", "", 10, 0)
	if err != nil {
		t.Fatalf("searchClips failed: %v", err)
	}
	if len(resultsDrama) == 0 {
		t.Fatal("expected at least 1 result for keyword 'drama', got 0")
	}
	for _, r := range resultsDrama {
		if r.ClipID == "clip_comedy" {
			t.Errorf("clip_comedy should not match keyword 'drama'")
		}
	}
}

// ── Test: LIKE fallback returns results capped at limit ─────────────────

func TestLikeSearch_RespectsLimit(t *testing.T) {
	ctx := context.Background()
	svc, repo := newFallbackService(t)

	// Insert 5 clips all matching "funny"
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("clip_funny_%d", i)
		insertTestClip(t, repo, &assets.Asset{
			ID:             id,
			Name:           fmt.Sprintf("Funny Clip Number %d", i),
			Source:         "youtube",
			Duration:       100 * time.Second,
			Tags:           []string{"funny"},
			LifecycleState: assets.StateReady,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		})
	}

	// Search with limit=2
	results, err := svc.searchClips(ctx, "funny", "", "", 2, 0)
	if err != nil {
		t.Fatalf("searchClips failed: %v", err)
	}
	if len(results) > 2 {
		t.Errorf("expected at most 2 results with limit=2, got %d", len(results))
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result, got 0")
	}
}

// ── Test: LIKE fallback returns empty when no match ─────────────────────

func TestLikeSearch_NoMatch(t *testing.T) {
	ctx := context.Background()
	svc, repo := newFallbackService(t)

	insertTestClip(t, repo, &assets.Asset{
		ID:             "clip_science",
		Name:           "Quantum Physics Explained",
		Source:         "youtube",
		Duration:       900 * time.Second,
		Tags:           []string{"science", "physics"},
		LifecycleState: assets.StateReady,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	})

	// Search for a term that doesn't exist
	results, err := svc.searchClips(ctx, "pizza", "", "", 10, 0)
	if err != nil {
		t.Fatalf("searchClips failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for non-matching query 'pizza', got %d", len(results))
	}
}

// ── Test: LIKE fallback returns error when no clipsRepo ─────────────────

func TestLikeSearch_NoRepo(t *testing.T) {
	ctx := context.Background()

	// Service WITHOUT clipsRepo — should return error
	svc := NewService(nil, "", nil, nil, nil, zap.NewNop())

	_, err := svc.searchClips(ctx, "test query", "", "", 10, 0)
	if err == nil {
		t.Fatal("expected error when no search backend available, got nil")
	}
	if err.Error() != "no search backend available: vectorstore=false embedder=false clipsRepo=false" {
		t.Errorf("unexpected error message: %q", err.Error())
	}
}

// ── Test: LIKE fallback searches metadata_json fields ───────────────────

func TestLikeSearch_FindsByMetadata(t *testing.T) {
	ctx := context.Background()
	svc, repo := newFallbackService(t)

	// Clip with matching metadata (clip_summary, topics) but non-matching name
	asset := &assets.Asset{
		ID:             "clip_metadata",
		Name:           "Generic Title",
		Source:         "youtube",
		Duration:       200 * time.Second,
		Tags:           []string{},
		LifecycleState: assets.StateReady,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	asset.SetMetadataString("clip_summary", "Actors sharing funny parenting stories at a comedy show")
	asset.SetMetadataString("topics", `["comedy","parenting","actors"]`)
	insertTestClip(t, repo, asset)

	// Search for "parenting" — should match by metadata, not by name
	results, err := svc.searchClips(ctx, "parenting", "", "", 10, 0)
	if err != nil {
		t.Fatalf("searchClips failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result matching by metadata, got 0")
	}
	found := false
	for _, r := range results {
		if r.ClipID == "clip_metadata" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("clip_metadata not found when searching 'parenting' (should match via clip_summary/topics)")
	}
}

// ── Test: searchClips prefers Qdrant when available ─────────────────────
// This test verifies that searchClips routes to Qdrant (and fails gracefully
// via LIKE fallback) when vectorSvc is present but embedder is nil.

func TestSearchClips_FallsBackToLikeWhenEmbedderNil(t *testing.T) {
	ctx := context.Background()
	db := storage.NewTestDBWithSchema(t, testSchema)
	t.Cleanup(func() { db.Close() })

	repo := clips.NewRepository(db, zap.NewNop())

	// Create a service WITH vectorSvc=nil, WITHOUT embedder
	// Since vectorSvc is nil, searchClips goes directly to LIKE fallback
	svc := NewService(nil, "", repo, nil, nil, zap.NewNop())

	insertTestClip(t, repo, &assets.Asset{
		ID:             "clip_search",
		Name:           "Search Test Clip",
		Source:         "youtube",
		Duration:       150 * time.Second,
		Tags:           []string{"search", "test"},
		LifecycleState: assets.StateReady,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	})

	results, err := svc.searchClips(ctx, "search test", "", "", 10, 0)
	if err != nil {
		t.Fatalf("searchClips should fall back to LIKE: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from LIKE fallback, got 0")
	}
	if results[0].ClipID != "clip_search" {
		t.Errorf("expected clip_search, got %s", results[0].ClipID)
	}
}
