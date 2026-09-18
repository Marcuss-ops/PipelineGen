package finalizer

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// ── P0 stock-acquisition certification (September 2026) ──────────────────
//
// The stock job finalizer's single-TX spine write is one of TWO producers that
// commit the same stock clip into media_assets (the other is the stock
// pipeline's own post-publication commit). These tests pin the producer half of
// that convergence: the spine write must commit the clip under its ACQUISITION
// PROVIDER and must resolve the taxonomy the producer DECLARED, so that both
// producers derive the same provider-scoped outbox event key and the same
// asset_kind / semantic_role.

const taxonomyTestSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestBuildCommitRequest_StockChunkKeepsProviderAndDeclaredTaxonomy pins the
// corrected projection for a YouTube-acquired stock clip.
//
// Before the fix the spine write hardcoded Source="stock", which (a) scoped the
// canonical asset.index.requested event_key away from the post-publication
// commit's provider-scoped key, so one asset produced TWO index events, and
// (b) let the later write clobber media_assets.source, dropping the YouTube
// provenance. The declared asset_kind / semantic_role were also discarded, so
// semantic_role was re-derived from the provider default ("discovery").
func TestBuildCommitRequest_StockChunkKeepsProviderAndDeclaredTaxonomy(t *testing.T) {
	artifact := finalization.PublishedArtifact{
		ArtifactID: "planner:6638386361363531:0",
		Kind:       finalization.KindVideo,
		Filename:   "clip_001.mp4",
		MIMEType:   "video/mp4",
		SizeBytes:  4096,
		SHA256:     taxonomyTestSHA,
		Source:     "youtube",
		ArtifactMetadata: map[string]any{
			"asset_kind":      "stock_video",
			"semantic_role":   "stock",
			"title":           "George Foreman vs Muhammad Ali",
			"description":     "heavyweight title fight",
			"source_provider": "youtube",
			"source_video_id": "55AasOJZzDE",
			"source_url":      "https://www.youtube.com/watch?v=55AasOJZzDE",
			"start_sec":       5.0,
			"end_sec":         12.0,
		},
		Location: finalization.AssetLocation{
			Provider:     "drive",
			FileID:       "drive-file-1",
			WebViewLink:  "https://drive.google.com/file/d/drive-file-1/view",
			DownloadLink: "https://drive.google.com/uc?id=drive-file-1",
			FolderID:     "folder-17wg1g9w",
			Action:       finalization.PublishCreated,
		},
	}

	req := NewAssetTxFinalizer(nil, nil).buildCommitRequest(artifact)

	require.Equal(t, "youtube", req.Source,
		"the spine write must commit the clip under its acquisition provider, not the stock family label")
	require.Equal(t, "55AasOJZzDE", req.SourceVideoID)
	require.Equal(t, "youtube", req.SourceProvider)
	require.Equal(t, "folder-17wg1g9w", req.FolderID,
		"the spine write must carry the published Drive folder: media_assets.folder_id is written unconditionally, so an empty value blanks it")

	require.False(t, req.Taxonomy.IsZero(), "a declared taxonomy must be resolved, not discarded")
	require.Equal(t, capregistry.AssetStockVideo, req.Taxonomy.AssetKind)
	require.Equal(t, "stock", req.Taxonomy.SemanticRole,
		"semantic_role must be the declared stock role, not the provider default (discovery)")
	require.Equal(t, "youtube", req.Taxonomy.SourceType)
	require.Equal(t, "planner:6638386361363531:0", req.Taxonomy.AssetID)

	require.True(t, req.EmitIndexEvent)
	require.Equal(t, persistence.IndexPriorityHigh, req.IndexPriority,
		"the high priority must follow the stock FAMILY, not one particular acquisition provider")
}

// TestBuildCommitRequest_UndeclaredTaxonomyStaysZero pins the godlike/07
// minimum-blast-radius side: a producer that declares no asset_kind /
// semantic_role keeps the zero taxonomy, so the media upsert's COALESCE-keep
// semantics preserve the stored dimensions and every legacy finalizer caller's
// projection is unchanged.
func TestBuildCommitRequest_UndeclaredTaxonomyStaysZero(t *testing.T) {
	artifact := finalization.PublishedArtifact{
		ArtifactID: "vo_6013c537ccd2dcc7",
		Kind:       finalization.KindVideo,
		Filename:   "voiceover.mp4",
		MIMEType:   "video/mp4",
		SizeBytes:  1024,
		SHA256:     taxonomyTestSHA,
		Source:     "voiceover",
		ArtifactMetadata: map[string]any{
			"title": "narration",
		},
		Location: finalization.AssetLocation{
			Provider: "drive", FileID: "f", WebViewLink: "https://drive/f",
			FolderID: "folder-vo", Action: finalization.PublishCreated,
		},
	}

	req := NewAssetTxFinalizer(nil, nil).buildCommitRequest(artifact)

	require.Equal(t, "voiceover", req.Source)
	require.True(t, req.Taxonomy.IsZero(),
		"a producer that declares no taxonomy must not have one invented for it")
	require.Zero(t, req.IndexPriority)
}
