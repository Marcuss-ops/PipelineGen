// Package assets — asset_committer_taxonomy_test.go
//
// Pins the taxonomy half of the canonical commit boundary: a non-zero
// mediaregistry.AssetTaxonomy on persistence.CommitRequest MUST be validated
// and its namespace / asset_kind / source_type / semantic_role dimensions
// persisted in the SAME media_assets UPSERT as the asset row (godlike/06
// SSOT — no separate taxonomy UPDATE, no second writer).
package assets

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// validTaxonomy is a canonical YouTube clip taxonomy with every dimension
// populated (SemanticRole is optional in mediaregistry.AssetTaxonomy.Validate).
func validTaxonomy(assetID string) mediaregistry.AssetTaxonomy {
	return mediaregistry.AssetTaxonomy{
		AssetID:      assetID,
		Namespace:    "youtube",
		MediaType:    mediaregistry.MediaVideo,
		AssetKind:    mediaregistry.AssetClip,
		SourceType:   "youtube",
		SemanticRole: "discovery",
	}
}

func baseTaxonomyCommitRequest() persistence.CommitRequest {
	return persistence.CommitRequest{
		AssetID:        "yt_abc123_10_60_v1",
		Source:         "youtube",
		Name:           "Funny Moment",
		Filename:       "clip.mp4",
		MediaType:      "video",
		ContentHash:    "sha256:content",
		LifecycleState: "ACTIVE",
		IndexState:     "DISCOVERED",
		EmitIndexEvent: true,
		Taxonomy:       validTaxonomy("yt_abc123_10_60_v1"),
	}
}

// TestSQLiteAssetCommitter_PersistsTaxonomyInSameUpsert proves the four
// taxonomy dimensions land in media_assets from a single CommitAndIndex
// call — no separate registry UPDATE is involved.
func TestSQLiteAssetCommitter_PersistsTaxonomyInSameUpsert(t *testing.T) {
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	committer := NewSQLiteAssetCommitter(db, box, nil)

	req := baseTaxonomyCommitRequest()
	if _, err := committer.CommitAndIndex(context.Background(), req); err != nil {
		t.Fatalf("CommitAndIndex: %v", err)
	}

	var namespace, assetKind, sourceType, semanticRole string
	err := db.QueryRow(`SELECT namespace, asset_kind, source_type, semantic_role FROM media_assets WHERE id = ?`, req.AssetID).
		Scan(&namespace, &assetKind, &sourceType, &semanticRole)
	require.NoError(t, err)
	require.Equal(t, "youtube", namespace)
	require.Equal(t, "clip", assetKind)
	require.Equal(t, "youtube", sourceType)
	require.Equal(t, "discovery", semanticRole)
}

// TestSQLiteAssetCommitter_TaxonomyEmptyAssetIDInherits proves the
// validation fills an empty Taxonomy.AssetID from the request and accepts
// the commit (the resolver builds taxonomy without AssetID; the committer
// binds it).
func TestSQLiteAssetCommitter_TaxonomyEmptyAssetIDInherits(t *testing.T) {
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	committer := NewSQLiteAssetCommitter(db, box, nil)

	req := baseTaxonomyCommitRequest()
	req.Taxonomy.AssetID = ""
	if _, err := committer.CommitAndIndex(context.Background(), req); err != nil {
		t.Fatalf("CommitAndIndex with empty Taxonomy.AssetID: %v", err)
	}

	var namespace, sourceType string
	err := db.QueryRow(`SELECT namespace, source_type FROM media_assets WHERE id = ?`, req.AssetID).
		Scan(&namespace, &sourceType)
	require.NoError(t, err)
	require.Equal(t, "youtube", namespace)
	require.Equal(t, "youtube", sourceType)
}

// TestSQLiteAssetCommitter_RejectsMismatchedTaxonomyAssetID pins the
// fail-closed contract: a taxonomy bound to a different asset must not
// silently commit.
func TestSQLiteAssetCommitter_RejectsMismatchedTaxonomyAssetID(t *testing.T) {
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	committer := NewSQLiteAssetCommitter(db, box, nil)

	req := baseTaxonomyCommitRequest()
	req.Taxonomy.AssetID = "asset-b"
	_, err := committer.CommitAndIndex(context.Background(), req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Taxonomy.AssetID must be empty or match AssetID")
}

// TestSQLiteAssetCommitter_RejectsInvalidTaxonomyKind pins the
// media_type↔asset_kind validity check at the commit boundary: an audio-only
// kind on a video asset must fail closed.
func TestSQLiteAssetCommitter_RejectsInvalidTaxonomyKind(t *testing.T) {
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	committer := NewSQLiteAssetCommitter(db, box, nil)

	req := baseTaxonomyCommitRequest()
	req.Taxonomy.AssetKind = mediaregistry.AssetVoiceover // audio-only kind
	_, err := committer.CommitAndIndex(context.Background(), req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid for media_type")
}

// TestSQLiteAssetCommitter_ZeroTaxonomyIsNormalized proves the compatibility
// bridge: a legacy producer may omit taxonomy at the call site, but the
// canonical writer derives and persists it before emitting an index event.
func TestSQLiteAssetCommitter_ZeroTaxonomyStillCommitsLegacy(t *testing.T) {
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	committer := NewSQLiteAssetCommitter(db, box, nil)

	req := baseTaxonomyCommitRequest()
	req.Taxonomy = mediaregistry.AssetTaxonomy{}
	if _, err := committer.CommitAndIndex(context.Background(), req); err != nil {
		t.Fatalf("CommitAndIndex with zero taxonomy: %v", err)
	}

	var namespace, assetKind, sourceType string
	err := db.QueryRow(`SELECT namespace, asset_kind, source_type FROM media_assets WHERE id = ?`, req.AssetID).
		Scan(&namespace, &assetKind, &sourceType)
	require.NoError(t, err)
	require.Equal(t, "youtube", namespace)
	require.Equal(t, "clip", assetKind)
	require.Equal(t, "youtube", sourceType)
}
